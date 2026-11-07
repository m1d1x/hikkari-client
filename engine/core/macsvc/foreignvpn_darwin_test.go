package macsvc

import (
	"reflect"
	"testing"

	"NekkoVPN/core/foreignvpn"
)

func TestParseNCList(t *testing.T) {
	out := `Available network connection services in the current set (*=enabled):
* (Disconnected)   8C035054-3565-4388-B7FA-EEEB925C058B VPN (su.ffg.happ)  "Happ"                           [VPN:su.ffg.happ]
* (Connected)      7A3C0B11-0000-4000-8000-1F2E3D4C5B6A VPN (com.wireguard.macos) "Дом (wg)"   [VPN:com.wireguard.macos]
  (Connecting)     11111111-2222-3333-4444-555555555555 IPSec "Работа"   [IPSec]
`
	want := []foreignvpn.SystemVPN{
		{ID: "7A3C0B11-0000-4000-8000-1F2E3D4C5B6A", Name: "Дом (wg)"},
		{ID: "11111111-2222-3333-4444-555555555555", Name: "Работа"},
	}
	if got := parseNCList(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestParseProcesses(t *testing.T) {
	out := "    1 /sbin/launchd\n50668 /Applications/AmneziaVPN.app/Contents/MacOS/amneziawg-go\n  581 /Applications/Amnezia VPN.app/Contents/MacOS/AmneziaVPN\nbad line\n"
	want := []foreignvpn.Process{
		{PID: 1, Path: "/sbin/launchd"},
		{PID: 50668, Path: "/Applications/AmneziaVPN.app/Contents/MacOS/amneziawg-go"},
		{PID: 581, Path: "/Applications/Amnezia VPN.app/Contents/MacOS/AmneziaVPN"},
	}
	if got := parseProcesses(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

// The real ps and scutil of this machine parse; nothing is changed.
func TestForeignSystemReadsThisMachine(t *testing.T) {
	procs, err := foreignSystem{}.Processes()
	if err != nil || len(procs) == 0 {
		t.Fatalf("processes: %d, %v", len(procs), err)
	}
	if _, err := (foreignSystem{}).ConnectedVPNs(); err != nil {
		t.Fatal(err)
	}
}
