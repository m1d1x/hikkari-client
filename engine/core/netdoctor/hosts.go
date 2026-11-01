package netdoctor

import (
	"bytes"
	"strings"
)

// hostsProtected are the domains whose override in the hosts file breaks
// what users come to hikkari for: Steam and Discord on the direct path,
// YouTube and Telegram through the tunnel, and hikkari itself. Ad blockers
// and old DPI tools pin them to 0.0.0.0 or to a dead mirror.
var hostsProtected = []string{
	"steampowered.com", "steamcommunity.com", "steamstatic.com", "steamcontent.com", "steamserver.net",
	"discord.com", "discord.gg", "discordapp.com", "discordapp.net", "discord.media",
	"youtube.com", "googlevideo.com", "ytimg.com", "youtu.be",
	"telegram.org", "t.me",
	"hikkari.net", "zovfy.ru",
}

// HostsMark prefixes every line FilterHosts disables, so a person (or a
// later restore) can tell them from the user's own comments.
const HostsMark = "# hikkari: "

func protectedHost(h string) bool {
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	for _, d := range hostsProtected {
		if h == d || strings.HasSuffix(h, "."+d) {
			return true
		}
	}
	return false
}

// FilterHosts comments out the entries of a hosts file that override a
// protected domain, and returns the new content and how many lines it
// disabled. Everything else — comments, blank lines, line endings, other
// entries — is kept byte for byte.
func FilterHosts(content []byte) ([]byte, int) {
	lines := bytes.SplitAfter(content, []byte("\n"))
	var out bytes.Buffer
	n := 0
	for _, line := range lines {
		body := string(line)
		entry := body
		if i := strings.IndexByte(entry, '#'); i >= 0 {
			entry = entry[:i]
		}
		fields := strings.Fields(entry)
		hit := false
		if len(fields) >= 2 {
			for _, h := range fields[1:] {
				if protectedHost(h) {
					hit = true
					break
				}
			}
		}
		if hit {
			out.WriteString(HostsMark)
			n++
		}
		out.WriteString(body)
	}
	return out.Bytes(), n
}
