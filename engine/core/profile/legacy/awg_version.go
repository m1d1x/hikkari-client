package legacy

import (
	"fmt"
	"strings"
)

// ValidateAWGField rejects fields which the portable import/export contract
// cannot preserve, even when a particular native core understands them.
// Only field names are inspected; key material must never appear in errors.
func ValidateAWGField(name string) error {
	switch strings.ToLower(strings.ReplaceAll(name, "_", "")) {
	case "headerprotectionkey", "contentpaddingaddition", "rekeyaftertime",
		"rekeytimeout", "rejectaftertime", "keepalivetimeout",
		"maxhandshakeattempts", "randomtrailers", "disablecookies":
		return fmt.Errorf("AmneziaWG 3.x пока не поддерживается форматом профилей Hikkari: новые параметры нельзя отбрасывать")
	}
	return nil
}
