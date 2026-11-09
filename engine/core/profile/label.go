package profile

import (
	"net/url"
	"strings"
)

// CleanLabel removes emoji flags while preserving other symbols in a title.
func CleanLabel(label string) string {
	runes := []rune(label)
	var result strings.Builder
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r >= 0x1f1e6 && r <= 0x1f1ff && i+1 < len(runes) && runes[i+1] >= 0x1f1e6 && runes[i+1] <= 0x1f1ff {
			i++
			continue
		}
		if r == 0x1f3f4 || r == 0x1f3f3 || r == 0x1f6a9 || r == 0x1f38c {
			for i+1 < len(runes) {
				next := runes[i+1]
				if next == 0xfe0f || next == 0x200d || next >= 0xe0020 && next <= 0xe007f || next == 0x1f308 || next == 0x26a7 || next == 0x2620 {
					i++
				} else {
					break
				}
			}
			continue
		}
		result.WriteRune(r)
	}
	return strings.TrimSpace(result.String())
}

// LabelCountry is the country a provider names with a flag at the head of the
// link's own label (#🇳🇱 …), lowercase ISO 3166-1 alpha-2, or "" without one.
// It is where the traffic leaves, which the dialled address need not be: a
// cascade link dials an entry in Russia and exits abroad, so a flag resolved
// from the address would show Russia. The stored label has its flags cleaned
// (CleanLabel), so the flag is read from the link itself.
func LabelCountry(p Profile) string {
	if p.Connection == nil {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(p.Connection.URI))
	if err != nil {
		return ""
	}
	runes := []rune(strings.TrimSpace(u.Fragment))
	if len(runes) < 2 || !regionalIndicator(runes[0]) || !regionalIndicator(runes[1]) {
		return ""
	}
	return string([]rune{'a' + runes[0] - 0x1f1e6, 'a' + runes[1] - 0x1f1e6})
}

func regionalIndicator(r rune) bool { return r >= 0x1f1e6 && r <= 0x1f1ff }
