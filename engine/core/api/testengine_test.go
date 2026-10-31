package api

import "NekkoVPN/core/engine/singbox"

// testEngine is sing-box with a local process manager that the tests never
// start. Tests must not reach the installed HikkariService: on a developer's
// machine it is real, and Shutdown would stop their VPN.
var testEngine = singbox.New
