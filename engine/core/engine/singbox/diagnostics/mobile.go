package diag

import (
	"net"
	"strings"
)

// loadBurstMobile is the burst of a load round on a mobile network. Parallel
// connections there can block the whole network until it reconnects (NTC Party
// 26426), which would cost the tester the phone's internet for a measurement.
const loadBurstMobile = 8

// mobileASNs are the networks of Russian mobile operators. Some carry home lines
// too; a home line of theirs then gets the small burst as well, the safe side of
// the mistake. Holders checked against RIPEstat on 2026-11-04.
var mobileASNs = map[string]bool{
	"AS8359": true, "AS13174": true, "AS28884": true, // МТС
	"AS31133": true, "AS25159": true, "AS47395": true, // МегаФон, Yota (Scartel)
	"AS16345": true, "AS3216": true, // Билайн
	"AS15378": true, "AS12958": true, "AS41330": true, // T2
}

// loadSelfNet names the network the card's address is seen from; a test stands
// in for it.
var loadSelfNet = func(local net.IP, stun []string) string {
	return netOf(publicIP(local, stun))
}

// onMobile says the card is on a mobile network: by the provider's network, or
// by the name Windows gives a cellular adapter. A network that cannot be named
// reads as not mobile: that is most lines of the current build, and the small
// burst would quietly change what every tester's rows measure. A phone shared
// over Wi-Fi is found only by the network.
func onMobile(iface, network string) bool {
	if mobileASNs[network] {
		return true
	}
	name := strings.ToLower(iface)
	return strings.Contains(name, "cellular") || strings.Contains(name, "wwan") || strings.Contains(name, "мобильн")
}
