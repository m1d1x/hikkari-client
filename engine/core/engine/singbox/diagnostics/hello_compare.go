package diag

import (
	"fmt"
	"sort"
	"strings"
)

// A core fingerprint goes stale when the browser it copies moves on: uTLS's
// chrome stayed Chrome 133 while Chrome itself added ML-DSA to its signature
// algorithms (plan knob-truth W4, research 2026-11-07-knob-truth-wire.md). hk
// says only whether two hellos are the same; helloDiff says in what, in the
// fields a censor's classifier can see without decrypting anything: the set of
// extensions (Chrome shuffles their order on every connection), the groups and
// the key shares with their lengths, the cipher suites, the signature
// algorithms and the versions in order, and the ALPN offer. Two connections of
// one browser build give no diff and the same hk (Chromium 152, 07.11).

// helloField is one field of a hello as helloDiff compares it.
type helloField struct {
	Name string
	A, B string
}

func helloFields(h *clientHello) map[string]string {
	list := func(vs []uint16) string {
		out := make([]string, len(vs))
		for i, v := range vs {
			out[i] = hex4(grease(v))
		}
		return strings.Join(out, ",")
	}
	var exts []string
	for _, e := range h.Exts {
		exts = append(exts, hex4(grease(e.Type)))
	}
	sort.Strings(exts)
	shares := make([]string, len(h.Shares))
	for i, s := range h.Shares {
		n := s.Len
		if isGREASE(s.Group) {
			n = 0 // a GREASE share's body is any length the client likes
		}
		shares[i] = fmt.Sprintf("%s:%d", groupName(s.Group), n)
	}
	return map[string]string{
		"extensions": strings.Join(exts, ","),
		"groups":     groupList(h.Groups),
		"shares":     strings.Join(shares, ","),
		"ciphers":    list(h.Ciphers),
		"sigalgs":    list(h.SigAlgs),
		"versions":   list(h.Versions),
		"alpn":       wireALPN(h.ALPN),
	}
}

// helloFieldOrder is the order a diff is reported in: what changed first in
// past fingerprint updates comes first.
var helloFieldOrder = []string{"groups", "shares", "sigalgs", "extensions", "ciphers", "versions", "alpn"}

// helloDiff lists the fields in which a differs from b; none when the two
// would look the same to a classifier of ClientHellos.
func helloDiff(a, b *clientHello) []helloField {
	fa, fb := helloFields(a), helloFields(b)
	var out []helloField
	for _, name := range helloFieldOrder {
		if fa[name] != fb[name] {
			out = append(out, helloField{Name: name, A: fa[name], B: fb[name]})
		}
	}
	return out
}

// extsOnlyIn are the extension types (hex) one side has and the other lacks.
func extsOnlyIn(a, b *clientHello) (onlyA, onlyB []string) {
	set := func(h *clientHello) map[string]bool {
		m := map[string]bool{}
		for _, e := range h.Exts {
			m[hex4(grease(e.Type))] = true
		}
		return m
	}
	sa, sb := set(a), set(b)
	for k := range sa {
		if !sb[k] {
			onlyA = append(onlyA, k)
		}
	}
	for k := range sb {
		if !sa[k] {
			onlyB = append(onlyB, k)
		}
	}
	sort.Strings(onlyA)
	sort.Strings(onlyB)
	return onlyA, onlyB
}
