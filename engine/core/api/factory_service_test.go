//go:build windows || darwin

package api

import (
	"reflect"
	"testing"

	"NekkoVPN/core/logging"
)

// The GUI runs unelevated on Windows and macOS: the default engine must hand
// the core to HikkariService, never fork sing-box itself. v1.0.5 shipped with
// the default wired to a local process and every StartVPN failed with
// "fork/exec …/Application Support/nekkobox/sing-box".
func TestDefaultEngineDrivesTheCoreThroughTheService(t *testing.T) {
	dir := t.TempDir()
	backend, err := DefaultEngineFactory(dir, logging.New(dir+"/test.log"))
	if err != nil {
		t.Fatal(err)
	}
	process := reflect.ValueOf(backend).Elem().FieldByName("process")
	if !process.IsValid() {
		t.Fatal("sing-box backend has no process field; update this test with the adapter")
	}
	if got := process.Elem().Type().String(); got != "*vpn.ServiceManager" {
		t.Fatalf("default engine runs the core through %s", got)
	}
}
