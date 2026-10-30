package profile

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
)

// Subscription is authoritative source state. It is never projected to the UI:
// URL usually contains a bearer credential. Servers carry only public metadata.
type Subscription struct {
	ID          string           `json:"id"`
	Revision    uint64           `json:"revision,omitempty"`
	URL         string           `json:"url"`
	Info        SubscriptionInfo `json:"info"`
	LastAttempt int64            `json:"lastAttempt,omitempty"`
	LastSuccess int64            `json:"lastSuccess,omitempty"`
	NextAttempt int64            `json:"nextAttempt,omitempty"`
	Failures    int              `json:"failures,omitempty"`
	Hidden      []ServerIdentity `json:"hidden,omitempty"`
}

type ServerIdentity struct {
	Key      string `json:"key"`
	Endpoint string `json:"endpoint"`
	Label    string `json:"label"`
}

// Identity ignores a URI's display fragment; endpoint/label fallbacks allow
// unambiguous credential/address rotations without losing local profile IDs.
func Identity(p Profile) ServerIdentity {
	if p.Connection == nil {
		return ServerIdentity{}
	}
	uri := p.Connection.URI
	if u, err := url.Parse(uri); err == nil && u.Scheme != "" {
		u.Fragment = ""
		u.RawFragment = ""
		uri = u.String()
	}
	hash := sha256.Sum256([]byte(uri))
	label := p.ProviderLabel
	if label == "" {
		label = p.Label
	}
	return ServerIdentity{
		Key:      hex.EncodeToString(hash[:]),
		Endpoint: fmt.Sprintf("%s|%s|%d", p.Connection.Protocol, strings.ToLower(p.Connection.Host), p.Connection.Port),
		Label:    p.Connection.Protocol + "|" + CleanLabel(label),
	}
}

// ReconcileSubscription replaces one source's snapshot in memory. The caller
// owns persistence and runtime rollback; other sources and standalone entries
// cannot be removed. Input must already be a fully validated provider response.
func ReconcileSubscription(pf *File, source *Subscription, drafts []Profile) ([]string, error) {
	clean := make([]Profile, 0, len(drafts))
	seen := map[string]bool{}
	for _, p := range drafts {
		if p.MetadataOnly {
			continue
		}
		if p.Connection == nil || p.Connection.URI == "" {
			return nil, fmt.Errorf("пустое подключение подписки")
		}
		p.Label = CleanLabel(p.Label)
		if p.Label == "" {
			p.Label = strings.TrimSpace(p.Connection.Host)
		}
		if p.Label == "" {
			return nil, fmt.Errorf("профиль подписки без названия")
		}
		p.ProviderLabel = p.Label
		key := Identity(p).Key + "|" + p.ProviderLabel
		if seen[key] {
			continue
		}
		seen[key] = true
		clean = append(clean, p)
	}
	if len(clean) == 0 {
		return nil, fmt.Errorf("подписка не содержит серверов; сохранён последний рабочий список")
	}
	old := []Profile{}
	kept := make([]Profile, 0, len(pf.Profiles)+len(clean))
	for _, p := range pf.Profiles {
		if p.Subscription != nil && p.Subscription.ID == source.ID {
			old = append(old, p)
		} else {
			kept = append(kept, p)
		}
	}
	identities := make([]ServerIdentity, len(clean))
	counts := [3]map[string]int{{}, {}, {}}
	oldCounts := [3]map[string]int{{}, {}, {}}
	fields := func(id ServerIdentity) [3]string { return [3]string{id.Key, id.Endpoint, id.Label} }
	for i, p := range clean {
		identities[i] = Identity(p)
		for j, key := range fields(identities[i]) {
			counts[j][key]++
		}
	}
	for _, p := range old {
		for j, key := range fields(Identity(p)) {
			oldCounts[j][key]++
		}
	}
	// Exact URI matches take precedence globally over endpoint/label fallbacks.
	matched := make([]int, len(clean))
	for i := range matched {
		matched[i] = -1
	}
	used := map[int]bool{}
	for pass := 0; pass < 3; pass++ {
		for i := range clean {
			if matched[i] >= 0 {
				continue
			}
			key := fields(identities[i])[pass]
			if counts[pass][key] != 1 || oldCounts[pass][key] != 1 {
				continue
			}
			for j, p := range old {
				if !used[j] && fields(Identity(p))[pass] == key {
					matched[i] = j
					used[j] = true
					break
				}
			}
		}
	}
	// Reserve previous IDs while allocating new ones to avoid reusing the ID of
	// a removed active server for an unrelated new server with the same label.
	reserved := &File{Profiles: append([]Profile(nil), pf.Profiles...)}
	names := []string{}
	for i, p := range clean {
		hidden := false
		for _, h := range source.Hidden {
			id := identities[i]
			if h.Key == id.Key || (h.Label != "" && h.Label == id.Label && counts[2][id.Label] == 1) || (h.Endpoint != "" && h.Endpoint == id.Endpoint && counts[1][id.Endpoint] == 1) {
				hidden = true
				break
			}
		}
		if hidden {
			continue
		}
		if matched[i] >= 0 {
			previous := old[matched[i]]
			p.Name = previous.Name
			// Keep only an actual local override; ordinary provider renames propagate.
			if previous.ProviderLabel == "" || previous.Label != previous.ProviderLabel {
				p.Label = previous.Label
			}
		} else {
			p.Name = uniqueName(reserved, p.Label)
			reserved.Profiles = append(reserved.Profiles, p)
		}
		info := source.Info
		info.ID = source.ID
		p.Subscription = &info
		p.Config = nil
		p.Kind = ""
		kept = append(kept, p)
		names = append(names, p.Name)
	}
	pf.Profiles = kept
	activePresent := false
	for _, p := range kept {
		if p.Name == pf.Active {
			activePresent = true
			break
		}
	}
	if !activePresent {
		pf.Active = ""
		// Prefer a replacement belonging to the same provider.
		if len(names) > 0 {
			pf.Active = names[0]
		} else if len(kept) > 0 {
			pf.Active = kept[0].Name
		}
	}
	if len(kept) == 0 {
		pf.Auto = false
	}
	return names, nil
}
