package configfile

import (
	"regexp"
	"testing"
)

// Every mode and the master switch: torrents go direct, by program and by
// protocol, once, after the sniff that tells them, and ahead of every rule
// that could send them into the tunnel.
func TestTorrentsGoDirectInEveryMode(t *testing.T) {
	for _, state := range []RoutingState{
		{Enabled: false, Mode: ModeBypass},
		{Enabled: true, Mode: ModeBypass, Bypass: RouteList{Processes: []string{"steam.exe"}}},
		// A user who sent a torrent client through the VPN on purpose: still direct.
		{Enabled: true, Mode: ModeProxy, Proxy: RouteList{Processes: []string{"qbittorrent.exe"}}},
	} {
		for _, head := range []string{SelectorTag, AutoTag} {
			cfg := DefaultConfig()
			ensureICMPDirectRule(cfg, true)
			for i := 0; i < 2; i++ { // the second pass checks idempotency
				ApplyRouting(cfg, state, head)
			}
			rules := cfg["route"].(map[string]interface{})["rules"].([]interface{})
			sniff, program, proto := -1, -1, -1
			for i, r := range rules {
				m := r.(map[string]interface{})
				if !isTorrentRule(m) {
					continue
				}
				switch {
				case m["action"] == "sniff":
					if sniff >= 0 {
						t.Fatalf("%+v: two torrent sniffs", state)
					}
					if m["port"] != nil {
						t.Fatalf("%+v: the torrent sniff is limited to ports %v", state, m["port"])
					}
					sniff = i
				case m["process_path_regex"] != nil:
					if program >= 0 {
						t.Fatalf("%+v: two torrent program rules", state)
					}
					program = i
				default:
					if proto >= 0 {
						t.Fatalf("%+v: two torrent protocol rules", state)
					}
					proto = i
				}
				if m["action"] != "sniff" && m["outbound"] != "direct" {
					t.Fatalf("%+v: torrents go to %v", state, m["outbound"])
				}
			}
			if sniff < 0 || program < 0 || proto < 0 || sniff > proto {
				t.Fatalf("%+v: sniff at %d, program at %d, protocol at %d", state, sniff, program, proto)
			}
			// Ahead of the torrent rules only the probes may go elsewhere
			// than direct: they match their own addresses, no torrent's.
			for i, r := range rules[:min(program, proto)] {
				m := r.(map[string]interface{})
				if m["process_name"] != nil || m["domain_suffix"] != nil || m["network"] == "udp" && m["outbound"] == AutoUDPTag {
					t.Fatalf("%+v: rule %d %v comes ahead of the torrent rules", state, i, m)
				}
			}
		}
	}
}

// A config written by an older build keeps its general sniff limited to the
// ports it was, and the torrent sniff is not taken for it.
func TestTheGeneralSniffKeepsItsPortsBesideTheTorrentSniff(t *testing.T) {
	cfg := DefaultConfig()
	ApplyRouting(cfg, RoutingState{Enabled: true, Mode: ModeBypass}, SelectorTag)
	rewriteRouteRefs(cfg, SelectorTag, nil)
	ensureICMPDirectRule(cfg, true)
	ApplyRouting(cfg, RoutingState{Enabled: true, Mode: ModeBypass}, SelectorTag)
	rules := cfg["route"].(map[string]interface{})["rules"].([]interface{})
	general, torrent := 0, 0
	for _, r := range rules {
		m := r.(map[string]interface{})
		if m["action"] != "sniff" {
			continue
		}
		if isTorrentRule(m) {
			torrent++
			if m["port"] != nil {
				t.Fatalf("the torrent sniff got ports %v", m["port"])
			}
			continue
		}
		if _, scoped := m["ip_cidr"]; !scoped {
			general++
			if m["port"] == nil {
				t.Fatal("the general sniff lost its ports")
			}
		}
	}
	if general != 1 || torrent != 1 {
		t.Fatalf("%d general sniffs, %d torrent sniffs", general, torrent)
	}
	if i := ruleIndex(rules, isQUICReject); i < 0 || i < ruleIndex(rules, func(m map[string]interface{}) bool { return m["network"] == "icmp" }) {
		t.Fatal("the system rules moved")
	}
}

func TestTorrentClientsByProgram(t *testing.T) {
	re := regexp.MustCompile(torrentClients)
	for _, path := range []string{
		`C:\Program Files\qBittorrent\qbittorrent.exe`,
		`C:\Users\a\AppData\Roaming\uTorrent\uTorrent.exe`,
		`C:\Users\a\AppData\Roaming\BitTorrent\BitTorrent.exe`,
		`C:\Program Files\Transmission\transmission-qt.exe`,
		`C:\Program Files\Deluge\deluge.exe`,
		`C:\Program Files\MediaGet2\mediaget.exe`,
		`C:\Users\a\AppData\Roaming\Zona\Zona.exe`,
		`C:\TorrServer\TorrServer-windows-amd64.exe`,
		`/Applications/qbittorrent.app/Contents/MacOS/qbittorrent`,
		`/Applications/Transmission.app/Contents/MacOS/Transmission`,
	} {
		if !re.MatchString(path) {
			t.Errorf("%s is not taken for a torrent client", path)
		}
	}
	for _, path := range []string{
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Users\a\AppData\Local\Discord\app-1.0.9200\Discord.exe`,
		`C:\Program Files (x86)\Steam\steam.exe`,
		`C:\aria2\aria2c.exe`,
		`C:\Program Files\Arizona\arizona.exe`,
		`C:\qbittorrent\notes.exe`,
		`/Applications/Telegram.app/Contents/MacOS/Telegram`,
	} {
		if re.MatchString(path) {
			t.Errorf("%s is taken for a torrent client", path)
		}
	}
}
