//go:build darwin

package macsvc

import (
	"path/filepath"
	"reflect"
	"testing"
)

type fakeSettings map[string][]string

func (f fakeSettings) NetworkServices() ([]string, error) {
	return []string{"Wi-Fi", "iPhone USB"}, nil
}
func (f fakeSettings) ManualDNS(service string) ([]string, error) { return f[service], nil }
func (f fakeSettings) SetManualDNS(service string, servers ...string) error {
	if len(servers) == 1 && servers[0] == "Empty" {
		delete(f, service)
		return nil
	}
	f[service] = servers
	return nil
}

func TestOverrideAndRestoreGiveBackTheUsersSetting(t *testing.T) {
	net := fakeSettings{"Wi-Fi": {"9.9.9.9"}}
	d := systemDNS{dir: filepath.Join(t.TempDir(), "dns"), net: net}
	if err := d.Override("172.19.0.2"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(net["Wi-Fi"], []string{"172.19.0.2"}) || !reflect.DeepEqual(net["iPhone USB"], []string{"172.19.0.2"}) {
		t.Fatalf("override: %v", net)
	}
	if err := d.Restore(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(net, fakeSettings{"Wi-Fi": {"9.9.9.9"}}) {
		t.Fatalf("restore: %v", net)
	}
}

// A second Override, or a new daemon after a crash, must not take the core's
// address for the user's setting.
func TestBackupSurvivesASecondOverrideAndACrash(t *testing.T) {
	net := fakeSettings{"Wi-Fi": {"9.9.9.9"}}
	dir := filepath.Join(t.TempDir(), "dns")
	for _, addr := range []string{"172.31.7.1", "172.19.0.2"} {
		if err := (systemDNS{dir: dir, net: net}).Override(addr); err != nil {
			t.Fatal(err)
		}
	}
	if err := (systemDNS{dir: dir, net: net}).Restore(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(net, fakeSettings{"Wi-Fi": {"9.9.9.9"}}) {
		t.Fatalf("after crash: %v", net)
	}
}

func TestRestoreWithNothingOverriddenChangesNothing(t *testing.T) {
	net := fakeSettings{"Wi-Fi": {"9.9.9.9"}}
	if err := (systemDNS{dir: filepath.Join(t.TempDir(), "dns"), net: net}).Restore(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(net, fakeSettings{"Wi-Fi": {"9.9.9.9"}}) {
		t.Fatalf("%v", net)
	}
}
