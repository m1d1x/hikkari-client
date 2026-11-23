package configfile

// BitTorrent never leaves through our exit, in any mode and whatever the user's
// lists say (owner's decision of 16.11, plans/ROADMAP.md, Э4): a rights
// holder's complaint about a torrent goes to the hoster of the exit, the box
// that holds the users' keys, and one person's swarm takes the exit's port.
// In the mode where only the blocked goes through the VPN a torrent goes direct
// on its own; where everything does, these rules send it direct.
//
// Two ways to know a torrent, ahead of every other rule of ours:
//
//   - by the program: the known torrent clients, every connection of theirs,
//     the encrypted peer connections (MSE) included, which no sniffer reads;
//   - by the protocol: a sniff of every flow's first bytes for the BitTorrent
//     handshake on TCP, uTP and the UDP tracker protocol, for a client not on
//     the list. The general sniff reads ports 80, 443, 8080 and 8443 only;
//     peers listen anywhere.
//
// The sniff reads the first packet of a UDP flow, which is already there: a
// call or a game waits for nothing. Before uTP it tries the shapes uTP's check
// would take for a torrent (WireGuard's handshake among them, core SPEC 078),
// so a user's own VPN stays in the tunnel. On TCP it waits for the client's
// first bytes at most torrentSniffTimeout: a protocol where the server speaks
// first (SMTP, IMAP and POP3 the core skips by port) waits that once per
// connection.

// torrentSniffTimeout bounds the wait for a TCP client's first bytes. A
// torrent client sends its handshake right after connecting.
const torrentSniffTimeout = "100ms"

// torrentSniffers are tried in this order: the stricter shapes first, then
// BitTorrent (TCP handshake, uTP, UDP tracker).
var torrentSniffers = []string{"wireguard", "openvpn", "ike", "tailscale", "bittorrent"}

// torrentClients matches the executables of torrent clients by name on
// Windows and macOS, any case: qBittorrent, µTorrent, BitTorrent, Transmission,
// Deluge, Tixati, BiglyBT, PicoTorrent, FrostWire, and the clients common in
// Russia: MediaGet, Zona, TorrServer. General downloaders that also fetch
// torrents (aria2, Free Download Manager) are not here: through them goes the
// web too; their torrents the sniff catches.
const torrentClients = `(?i)(^|[\\/])(qbittorrent|utorrent|bittorrent|transmission(-qt|-gtk|-daemon)?|deluged?|tixati|biglybt|picotorrent|frostwire|mediaget|zona|torrserver[^\\/]*)(\.exe)?$`

// torrentRules are the sniff and the two direct rules, in this order.
func torrentRules() []interface{} {
	return []interface{}{
		map[string]interface{}{
			"action":  "sniff",
			"sniffer": toAnySlice(torrentSniffers),
			"timeout": torrentSniffTimeout,
		},
		map[string]interface{}{
			"process_path_regex": []interface{}{torrentClients},
			"outbound":           "direct",
		},
		map[string]interface{}{
			"protocol": []interface{}{"bittorrent"},
			"outbound": "direct",
		},
	}
}

// isTorrentRule recognises a rule torrentRules compiles: ApplyRouting drops
// them with the rest of its own and compiles them again.
func isTorrentRule(m map[string]interface{}) bool {
	if action, _ := m["action"].(string); action == "sniff" {
		names, _ := m["sniffer"].([]interface{})
		for _, n := range names {
			if n == "bittorrent" {
				return true
			}
		}
		return false
	}
	if p, _ := m["protocol"].([]interface{}); len(p) == 1 && p[0] == "bittorrent" {
		return true
	}
	if re, _ := m["process_path_regex"].([]interface{}); len(re) == 1 && re[0] == torrentClients {
		return true
	}
	return false
}
