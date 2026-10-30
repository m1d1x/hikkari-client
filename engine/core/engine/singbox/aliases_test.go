package singbox

import "NekkoVPN/core/profile"

type Profile = profile.Profile

const (
	KindOutbound = profile.KindOutbound
	KindEndpoint = profile.KindEndpoint
)

var NewStore = profile.New
