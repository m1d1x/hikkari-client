package profile

import "strings"

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
