package routing

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
)

func TestASwitchedOffRuleStaysInItsPlaceAndIsNotRouted(t *testing.T) {
	state := DefaultRoutingState()
	state.Mode = ModeProxy
	for _, p := range []string{"Discord.exe", "helldivers2.exe", "steam.exe"} {
		if err := state.ChangeRule(ModeProxy, "processes", p, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.SetRuleOn(ModeProxy, "processes", "helldivers2.exe", false); err != nil {
		t.Fatal(err)
	}

	if want := []string{"Discord.exe", "helldivers2.exe", "steam.exe"}; !reflect.DeepEqual(state.Proxy.Processes, want) {
		t.Fatalf("stored list %v, want %v: a switched-off rule keeps its place", state.Proxy.Processes, want)
	}
	if got, want := state.Active().Proxy.Processes, []string{"Discord.exe", "steam.exe"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("routed %v, want %v", got, want)
	}
	if state.Active().Proxy.Off != nil {
		t.Fatal("the routed policy carries no switched-off rules")
	}
	doc := ManualDocument(state, "windows")
	if got := doc.Sources[0].Proxy.Processes["windows"]; !reflect.DeepEqual(got, []string{"Discord.exe", "steam.exe"}) {
		t.Fatalf("document routes %v", got)
	}

	if err := state.SetRuleOn(ModeProxy, "processes", "helldivers2.exe", true); err != nil {
		t.Fatal(err)
	}
	if state.Proxy.Off != nil {
		t.Fatalf("nothing is off, Off = %+v", state.Proxy.Off)
	}
	if got := state.Active().Proxy.Processes; len(got) != 3 {
		t.Fatalf("switched back on, routed %v", got)
	}
}

func TestSwitchingOffARuleThatIsNotThereFails(t *testing.T) {
	state := DefaultRoutingState()
	if err := state.SetRuleOn(ModeProxy, "domains", "example.com", false); err == nil {
		t.Fatal("a rule that is not in the list cannot be switched off")
	}
}

func TestRemovingOrReaddingASwitchedOffRule(t *testing.T) {
	state := DefaultRoutingState()
	_ = state.ChangeRule(ModeBypass, "domains", "example.com", false)
	_ = state.SetRuleOn(ModeBypass, "domains", "example.com", false)
	if err := state.ChangeRule(ModeBypass, "domains", "example.com", false); err != nil {
		t.Fatal(err)
	}
	if state.Bypass.Off != nil {
		t.Fatal("adding a switched-off rule again switches it back on")
	}

	_ = state.SetRuleOn(ModeBypass, "domains", "example.com", false)
	if err := state.ChangeRule(ModeBypass, "domains", "example.com", true); err != nil {
		t.Fatal(err)
	}
	if len(state.Bypass.Domains) != 0 || state.Bypass.Off != nil {
		t.Fatalf("a removed rule leaves nothing behind: %+v", state.Bypass)
	}
}

func TestSwitchedOffRulesSurviveTheFile(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "routing.json"))
	state := DefaultRoutingState()
	_ = state.ChangeRule(ModeProxy, "processes", "helldivers2.exe", false)
	_ = state.SetRuleOn(ModeProxy, "processes", "helldivers2.exe", false)
	if err := store.WriteRouting(state); err != nil {
		t.Fatal(err)
	}
	read, err := store.ReadRouting()
	if err != nil {
		t.Fatal(err)
	}
	if read.Proxy.Off == nil || !reflect.DeepEqual(read.Proxy.Off.Processes, []string{"helldivers2.exe"}) {
		t.Fatalf("Off after a round trip: %+v", read.Proxy.Off)
	}

	// A file with nothing switched off is written exactly as before.
	raw, _ := json.Marshal(DefaultRoutingState().Sanitize())
	var fields map[string]map[string]any
	_ = json.Unmarshal(raw, &fields)
	if _, ok := fields["proxy"]["off"]; ok {
		t.Fatal("an empty Off is not written")
	}
}

func TestAStaleOffEntryIsDropped(t *testing.T) {
	state := DefaultRoutingState()
	state.Proxy.Processes = []string{"Discord.exe"}
	state.Proxy.Off = &RouteList{Processes: []string{"gone.exe", "Discord.exe", "Discord.exe"}}
	got := state.Sanitize().Proxy.Off
	if got == nil || !reflect.DeepEqual(got.Processes, []string{"Discord.exe"}) {
		t.Fatalf("Off after sanitize: %+v", got)
	}
}
