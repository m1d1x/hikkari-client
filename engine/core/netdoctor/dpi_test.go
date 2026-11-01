package netdoctor

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestClassifyDPIServiceJudgesTheBinaryAlone(t *testing.T) {
	cases := []struct {
		name      string
		imagePath string
		driver    bool
		want      string
	}{
		{"quoted with args", `"C:\zapret\bin\winws.exe" --wf-tcp=80,443 --dpi-desync=fake`, false, DPIClassWinws},
		{"unquoted with spaces", `C:\Program Files\zapret\winws.exe --wf-tcp=80`, false, DPIClassWinws},
		{"case", `"C:\TOOLS\GoodbyeDPI.EXE" -5`, false, DPIClassGoodbyeDPI},
		{"no extension", `C:\tools\goodbyedpi -5`, false, DPIClassGoodbyeDPI},
		{"driver", `\??\C:\zapret\bin\WinDivert64.sys`, true, DPIClassWinDivert},
		{"driver in a path with spaces", `\??\C:\Program Files\x\WinDivert.sys`, true, DPIClassWinDivert},
		{"system32 driver", `system32\drivers\windivert.sys`, true, DPIClassWinDivert},

		{"tool only in the folder", `"C:\winws.exe\app.exe"`, false, ""},
		{"tool only in an argument", `C:\Windows\system32\svchost.exe -k winws.exe`, false, ""},
		{"wrapper", `C:\Windows\system32\cmd.exe /c C:\zapret\winws.exe`, false, ""},
		{"longer name", `C:\tools\winws2.exe`, false, ""},
		{"windivert folder, other binary", `"C:\windivert\app.exe"`, false, ""},
		{"windivert folder, other driver", `\??\C:\windivert\other.sys`, true, ""},
		{"windivert named user-mode binary", `C:\tools\windivert-gui.exe`, false, ""},
		{"tool name on a driver", `\??\C:\x\winws.exe`, true, ""},
		{"empty", ``, false, ""},
	}
	for _, c := range cases {
		if got := ClassifyDPIService(c.imagePath, c.driver); got != c.want {
			t.Errorf("%s: ClassifyDPIService(%q) = %q, want %q", c.name, c.imagePath, got, c.want)
		}
	}
}

func TestClassifyDPIProcessIsAnExactName(t *testing.T) {
	for exe, want := range map[string]string{
		"winws.exe":                DPIClassWinws,
		`C:\zapret\WINWS.exe`:      DPIClassWinws,
		"goodbyedpi.exe":           DPIClassGoodbyeDPI,
		"winws-helper.exe":         "",
		`C:\winws.exe\app.exe`:     "",
		"windivert.exe":            "",
		"mygoodbyedpi.exe":         "",
		`C:\Program Files\app.exe`: "",
	} {
		if got := ClassifyDPIProcess(exe); got != want {
			t.Errorf("ClassifyDPIProcess(%q) = %q, want %q", exe, got, want)
		}
	}
}

// fakeDPI records the mutating calls in order and models their effect, so a
// second run sees what the first one left.
type fakeDPI struct {
	services  []DPIService
	processes []DPIProcess
	backup    []byte

	listErr, processErr, writeErr error
	manualErr, stopErr            map[string]error

	calls []string
}

func (f *fakeDPI) Services() ([]DPIService, error) { return f.services, f.listErr }
func (f *fakeDPI) Processes() ([]DPIProcess, error) {
	return f.processes, f.processErr
}
func (f *fakeDPI) service(name string) *DPIService {
	for i := range f.services {
		if f.services[i].Name == name {
			return &f.services[i]
		}
	}
	return nil
}
func (f *fakeDPI) SetManualStart(name string) error {
	f.calls = append(f.calls, "manual "+name)
	if err := f.manualErr[name]; err != nil {
		return err
	}
	f.service(name).StartType = 3
	return nil
}
func (f *fakeDPI) StopService(name string) error {
	f.calls = append(f.calls, "stop "+name)
	if err := f.stopErr[name]; err != nil {
		return err
	}
	f.service(name).Running = false
	return nil
}
func (f *fakeDPI) KillProcess(pid uint32, class string) error {
	f.calls = append(f.calls, "kill "+class)
	f.processes = slices.DeleteFunc(f.processes, func(p DPIProcess) bool { return p.PID == pid })
	return nil
}
func (f *fakeDPI) ReadBackup() ([]byte, error) { return f.backup, nil }
func (f *fakeDPI) WriteBackup(content []byte) error {
	f.calls = append(f.calls, "backup")
	if f.writeErr != nil {
		return f.writeErr
	}
	f.backup = content
	return nil
}

func (f *fakeDPI) savedBackup(t *testing.T) DPIBackup {
	t.Helper()
	var b DPIBackup
	if err := json.Unmarshal(f.backup, &b); err != nil {
		t.Fatalf("backup %q: %v", f.backup, err)
	}
	return b
}

func newFakeDPI() *fakeDPI {
	return &fakeDPI{
		services: []DPIService{
			{Name: "WinDivert", ImagePath: `\??\C:\z\WinDivert64.sys`, Driver: true, StartType: 3, Running: true},
			{Name: "zapret", ImagePath: `"C:\z\winws.exe" --wf-tcp=80`, StartType: 2, DelayedAutoStart: true, Running: true},
			{Name: "Dnscache", ImagePath: `C:\Windows\system32\svchost.exe -k NetworkService`, StartType: 2, Running: true},
		},
		processes: []DPIProcess{
			{PID: 10, Path: `C:\Windows\explorer.exe`},
			{PID: 20, Path: `C:\t\goodbyedpi.exe`},
		},
	}
}

func TestStopDPIToolsBacksUpFirstAndLeavesTheDriverForLast(t *testing.T) {
	sys := newFakeDPI()
	out, err := StopDPITools(sys)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"backup", "manual zapret", "stop zapret", "kill goodbyedpi", "stop WinDivert"}
	if !slices.Equal(sys.calls, want) {
		t.Fatalf("calls = %v, want %v", sys.calls, want)
	}
	if !slices.Equal(out.Stopped, []string{DPIClassGoodbyeDPI, DPIClassWinDivert, DPIClassWinws}) || len(out.Failed) != 0 {
		t.Fatalf("%+v", out)
	}
	b := sys.savedBackup(t)
	if len(b.Services) != 1 || b.Services[0] != (DPIBackupEntry{Name: "zapret", Class: DPIClassWinws, StartType: 2, DelayedAutoStart: true}) {
		t.Fatalf("backup: %+v", b)
	}
}

func TestStopDPIToolsNeverTouchesAnythingElse(t *testing.T) {
	sys := newFakeDPI()
	if _, err := StopDPITools(sys); err != nil {
		t.Fatal(err)
	}
	for _, call := range sys.calls {
		if strings.Contains(call, "Dnscache") || strings.Contains(call, "explorer") {
			t.Fatalf("touched a foreign service or process: %v", sys.calls)
		}
	}
	if s := sys.service("Dnscache"); s.StartType != 2 || !s.Running {
		t.Fatalf("%+v", s)
	}
}

// The reset is meant to be repeatable: a second run sees the service manual
// and must not record "manual" as its original.
func TestASecondResetKeepsTheOriginalStartType(t *testing.T) {
	sys := newFakeDPI()
	if _, err := StopDPITools(sys); err != nil {
		t.Fatal(err)
	}
	sys.service("zapret").Running = true // started by hand since
	sys.calls = nil
	out, err := StopDPITools(sys)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sys.calls, []string{"stop zapret"}) || !slices.Equal(out.Stopped, []string{DPIClassWinws}) {
		t.Fatalf("calls=%v out=%+v", sys.calls, out)
	}
	if b := sys.savedBackup(t); len(b.Services) != 1 || b.Services[0].StartType != 2 {
		t.Fatalf("backup: %+v", b)
	}
}

// The user turned autostart back on after a reset: that is the original now,
// and other services' entries stay.
func TestBackupFollowsAUserChangeAndKeepsOtherEntries(t *testing.T) {
	sys := newFakeDPI()
	sys.backup = []byte(`{"services":[{"name":"ZAPRET","class":"winws","startType":0},{"name":"old","class":"goodbyedpi","startType":2}]}`)
	if _, err := StopDPITools(sys); err != nil {
		t.Fatal(err)
	}
	b := sys.savedBackup(t)
	if len(b.Services) != 2 || b.Services[0].Name != "zapret" || b.Services[0].StartType != 2 || b.Services[1].Name != "old" {
		t.Fatalf("backup: %+v", b)
	}
}

func TestDisabledAndIdleManualServicesStayAsTheyAre(t *testing.T) {
	sys := &fakeDPI{services: []DPIService{
		{Name: "idle", ImagePath: `C:\z\winws.exe`, StartType: 3},
		{Name: "off", ImagePath: `C:\z\goodbyedpi.exe`, StartType: 4},
		{Name: "offButRunning", ImagePath: `C:\z\winws.exe`, StartType: 4, Running: true},
	}}
	out, err := StopDPITools(sys)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sys.calls, []string{"stop offButRunning"}) || !slices.Equal(out.Stopped, []string{DPIClassWinws}) {
		t.Fatalf("calls=%v out=%+v", sys.calls, out)
	}
	if sys.service("offButRunning").StartType != 4 {
		t.Fatal("a disabled service was moved to manual")
	}
}

func TestNoBackupMeansNothingIsTouched(t *testing.T) {
	sys := newFakeDPI()
	sys.writeErr = errors.New("disk full")
	if _, err := StopDPITools(sys); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v", err)
	}
	if !slices.Equal(sys.calls, []string{"backup"}) {
		t.Fatalf("acted without a backup: %v", sys.calls)
	}
}

func TestADamagedBackupIsNeitherOverwrittenNorActedOn(t *testing.T) {
	sys := newFakeDPI()
	sys.backup = []byte(`{"services":[{"na`)
	if _, err := StopDPITools(sys); err == nil {
		t.Fatal("no error")
	}
	if len(sys.calls) != 0 || string(sys.backup) != `{"services":[{"na` {
		t.Fatalf("calls=%v backup=%q", sys.calls, sys.backup)
	}
}

func TestAnUnlistableSystemIsNotActedOn(t *testing.T) {
	sys := newFakeDPI()
	sys.listErr = errors.New("access denied")
	if _, err := StopDPITools(sys); err == nil {
		t.Fatal("no error")
	}
	if len(sys.calls) != 0 {
		t.Fatalf("calls=%v", sys.calls)
	}
}

// One tool refusing does not stop the others, and the report says which.
func TestAFailureIsReportedByClassAndTheRestStillStops(t *testing.T) {
	sys := newFakeDPI()
	sys.stopErr = map[string]error{"WinDivert": errors.New("dependent services running")}
	out, err := StopDPITools(sys)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(out.Failed, []string{DPIClassWinDivert}) || !slices.Equal(out.Stopped, []string{DPIClassGoodbyeDPI, DPIClassWinws}) {
		t.Fatalf("%+v", out)
	}
	if len(out.Errors) != 1 || !strings.HasPrefix(out.Errors[0], "windivert: ") {
		t.Fatalf("%v", out.Errors)
	}
}

// Autostart stayed on: the tool would return at the reboot the reset needs,
// so a stopped service with a failed start type change still counts as failed.
func TestAStartTypeFailureIsReportedEvenIfTheStopWorked(t *testing.T) {
	sys := newFakeDPI()
	sys.manualErr = map[string]error{"zapret": errors.New("denied")}
	out, err := StopDPITools(sys)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(out.Failed, DPIClassWinws) || !slices.Contains(sys.calls, "stop zapret") {
		t.Fatalf("out=%+v calls=%v", out, sys.calls)
	}
}

func TestAProcessListFailureIsReportedAndTheDriverStillTried(t *testing.T) {
	sys := newFakeDPI()
	sys.processErr = errors.New("snapshot failed")
	out, err := StopDPITools(sys)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(out.Failed, []string{DPIClassGoodbyeDPI, DPIClassWinws}) || !slices.Contains(sys.calls, "stop WinDivert") {
		t.Fatalf("out=%+v calls=%v", out, sys.calls)
	}
}

func TestScanIsReadOnlyAndNamesOnlyWhatTheResetWouldActOn(t *testing.T) {
	sys := newFakeDPI()
	sys.services = append(sys.services, DPIService{Name: "leftover", ImagePath: `C:\z\goodbyedpi.exe`, StartType: 3})
	sys.processes = sys.processes[:1]
	got, err := ScanDPITools(sys)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{DPIClassWinDivert, DPIClassWinws}) {
		t.Fatalf("got %v", got)
	}
	if len(sys.calls) != 0 {
		t.Fatalf("scan changed something: %v", sys.calls)
	}
}

func TestScanTellsNothingFoundFromUnknown(t *testing.T) {
	got, err := ScanDPITools(&fakeDPI{})
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("got %#v err %v", got, err)
	}
	if got, err := ScanDPITools(&fakeDPI{processErr: errors.New("x")}); err == nil || got != nil {
		t.Fatalf("got %#v err %v", got, err)
	}
}
