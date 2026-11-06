package api

import (
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"NekkoVPN/core/profile"
	"NekkoVPN/core/routing"
)

var lifecycleSequence atomic.Uint64

// traceCoreTransition records intent BEFORE touching the backend, including
// asynchronous callers invisible to FFI tracing. Only code symbols and change
// categories are logged: never configuration values, links or credentials.
// Callers own operationMu; completion covers start AND rollback, not just Stop.
func (c *Core) traceCoreTransition(reason, detail string) func(error) {
	if c.logger == nil {
		return func(error) {}
	}
	id, started := lifecycleSequence.Add(1), time.Now()
	var pcs [20]uintptr
	n := runtime.Callers(2, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	var callers []string
	for {
		frame, more := frames.Next()
		if strings.HasPrefix(frame.Function, "NekkoVPN/core/") {
			callers = append(callers, strings.TrimPrefix(frame.Function, "NekkoVPN/core/"))
		}
		if !more {
			break
		}
	}
	c.logger.Logf("[lifecycle] begin id=%d reason=%s running=%v caller=%s %s", id, reason, c.backend.IsRunning(), strings.Join(callers, " <- "), detail)
	return func(err error) {
		message := ""
		if err != nil {
			message = subscriptionSecret.ReplaceAllString(err.Error(), "[redacted-link]")
		}
		c.logger.Logf("[lifecycle] end id=%d reason=%s running=%v took=%s ok=%v err=%q", id, reason, c.backend.IsRunning(), time.Since(started).Round(time.Millisecond), err == nil, message)
	}
}

func routingChangeSummary(previous, next routing.RoutingState) string {
	var changed []string
	if previous.Enabled != next.Enabled {
		changed = append(changed, "enabled")
	}
	if previous.Mode != next.Mode {
		changed = append(changed, "mode")
	}
	for _, pair := range []struct {
		name string
		a, b routing.RouteList
	}{
		{"bypass", previous.Bypass, next.Bypass}, {"proxy", previous.Proxy, next.Proxy},
	} {
		for _, field := range []struct {
			name string
			a, b []string
		}{
			{"domains", pair.a.Domains, pair.b.Domains}, {"ips", pair.a.IPs, pair.b.IPs},
			{"processes", pair.a.Processes, pair.b.Processes}, {"folders", pair.a.Folders, pair.b.Folders},
		} {
			if !reflect.DeepEqual(field.a, field.b) {
				changed = append(changed, fmt.Sprintf("%s.%s:%d->%d", pair.name, field.name, len(field.a), len(field.b)))
			}
		}
	}
	return "changed=" + strings.Join(changed, ",")
}

// Use the same runtime comparison as commitProfiles, but report only counts.
func profileChangeSummary(before, after *profile.File) string {
	old := make(map[string]profile.Profile, len(before.Profiles))
	for _, p := range before.Profiles {
		old[p.Name] = p
	}
	added, changed := 0, 0
	for _, p := range after.Profiles {
		previous, exists := old[p.Name]
		if !exists {
			added++
		} else {
			if profile.Identity(previous).Key != profile.Identity(p).Key || previous.Kind != p.Kind || !reflect.DeepEqual(previous.Config, p.Config) {
				changed++
			}
			delete(old, p.Name)
		}
	}
	return fmt.Sprintf("profiles=%d->%d added=%d removed=%d changed=%d active_changed=%v auto_changed=%v", len(before.Profiles), len(after.Profiles), added, len(old), changed, before.Active != after.Active, before.Auto != after.Auto)
}
