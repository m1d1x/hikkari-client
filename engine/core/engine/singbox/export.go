package singbox

import (
	"NekkoVPN/core/profile"
	"NekkoVPN/core/profile/legacy"
)

func ExportURI(p profile.Profile) (string, error) { return legacy.ExportURI(p) }
