package diag

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"NekkoVPN/core/netinfo"
)

// A flow's destination address never leaves the machine (logging.SanitizeLog
// strips it), yet a silent flow can only be explained by knowing which
// destination stays silent: the same game relay round after round, or a
// different one each time, one network or several. destLabel names it
// without the address: the network it belongs to and a stable id.
//
// The id is an HMAC of the address under a key kept next to the config and
// never sent anywhere. Without the key the id cannot be mapped back to an
// address, even by trying every IPv4 one; with the same key it is the same
// across rounds and restarts, so rounds can be compared.

// destKeyFile holds the key, next to the core config.
const destKeyFile = "dest-id.key"

// destKey loads the install's key, creating it on first use. A key that
// cannot be stored lives for this process only: ids then hold within a run.
type destKey struct {
	once sync.Once
	key  []byte
}

func (k *destKey) get(dir string) []byte {
	k.once.Do(func() {
		path := filepath.Join(dir, destKeyFile)
		if b, err := os.ReadFile(path); err == nil && len(b) == 32 {
			k.key = b
			return
		}
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return
		}
		k.key = b
		if dir != "" {
			_ = os.WriteFile(path, b, 0o600)
		}
	})
	return k.key
}

var (
	// Shared address space (RFC 6598): carrier NAT, never a destination on
	// the internet.
	cgnatNet = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}
	// sing-box fake-ip range: the real destination is the host name.
	fakeIPNet = &net.IPNet{IP: net.IPv4(198, 18, 0, 0), Mask: net.CIDRMask(15, 32)}
)

// destLabel classifies addr: "-" (none), "loopback", "private", "fakeip", or
// the network and id: "AS8359#1a2b3c4d" for a Russian network (the embedded
// database holds only those), "ext#1a2b3c4d" for any other.
func destLabel(key []byte, addr string) string {
	ip := net.ParseIP(addr)
	switch {
	case ip == nil:
		return "-"
	case ip.IsLoopback():
		return "loopback"
	case ip.IsPrivate() || ip.IsLinkLocalUnicast() || cgnatNet.Contains(ip):
		return "private"
	case fakeIPNet.Contains(ip):
		return "fakeip"
	}
	as := "ext"
	if info := netinfo.Lookup(ip); info.ASN != 0 {
		as = fmt.Sprintf("AS%d", info.ASN)
	}
	if len(key) == 0 {
		return as
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(ip.To16())
	return as + "#" + hex.EncodeToString(mac.Sum(nil)[:4])
}

// dest labels a destination for this install.
func (r *Runner) dest(addr string) string {
	dir := ""
	if r.configPath != "" {
		dir = filepath.Dir(r.configPath)
	}
	return destLabel(r.destKey.get(dir), addr)
}
