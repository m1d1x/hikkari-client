package netdoctor

import (
	"slices"
	"strings"
	"testing"
)

func TestForeignWFPProvidersLeaveOutWindowsInAnyLanguage(t *testing.T) {
	got := ForeignWFPProviders([]WFPProvider{
		{Key: "{10AD9216-CCDE-456C-8B16-E9F04E60A90B}", Name: "IKEEXT"},
		{Key: "{4B153735-1049-4480-AAB4-D1B9BDC03710}", Name: "Брандмауэр Windows"},
		{Key: "{00000000-0000-0000-0000-000000000001}", Name: "Что-то своё", ServiceName: "MpsSvc"},
		{Key: "{00000000-0000-0000-0000-000000000002}", Name: "Microsoft Windows WFP Built-in TCP Templates provider."},
		{Key: "{00000000-0000-0000-0000-000000000003}", Name: "Windows Defender Firewall"},
		{Key: "{00000000-0000-0000-0000-000000000004}", Name: "ProtonVPN", ServiceName: "ProtonVPN Service"},
		{Key: "{00000000-0000-0000-0000-000000000005}", Name: "Mullvad VPN"},
		{Key: "{00000000-0000-0000-0000-000000000006}", Name: "OpenVPN for Windows"},
		{Key: "{00000000-0000-0000-0000-000000000007}", Name: "Not Microsoft VPN"},
	})
	want := []string{"Mullvad VPN", "Not Microsoft VPN", "OpenVPN for Windows", "ProtonVPN"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestForeignWFPProviderNamesAreSafeForOneLogLine(t *testing.T) {
	got := ForeignWFPProviders([]WFPProvider{
		{Name: "Evil\r\n[service] forged  line\x00"},
		{Name: "  "},
		{Name: strings.Repeat("я", 200)},
		{Name: "Dup"},
		{Name: "Dup"},
	})
	want := []string{"Dup", "Evil [service] forged line", "unnamed", strings.Repeat("я", wfpNameLimit)}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestForeignWFPProvidersTellNoneFromUnknown(t *testing.T) {
	if got := ForeignWFPProviders(nil); got == nil || len(got) != 0 {
		t.Fatalf("%#v", got)
	}
}
