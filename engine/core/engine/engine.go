// Package engine defines the application contract for interchangeable VPN
// engines. It contains no native config, process, control API or OS details.
package engine

import (
	"NekkoVPN/core/autotunnel"
	"NekkoVPN/core/profile"
	"NekkoVPN/core/routing"
	"context"
	"fmt"
	"io"
)

type Logger interface {
	Logf(string, ...interface{})
	PipeLines(string, io.ReadCloser)
}

// Capabilities describe the selected standalone adapter on the current OS.
type Capabilities struct {
	TUN               bool `json:"tun"`
	DomainRouting     bool `json:"domainRouting"`
	IPRouting         bool `json:"ipRouting"`
	AutoSelect        bool `json:"autoSelect"`
	LiveProfileSwitch bool `json:"liveProfileSwitch"`
	ProfileProbe      bool `json:"profileProbe"`
	ProcessRouting    bool `json:"processRouting"`
	FolderRouting     bool `json:"folderRouting"`
}

type Info struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Capabilities Capabilities `json:"capabilities"`
}

// Configuration is the user's intent. Profile IDs stay opaque here; mapping
// them to outbound tags and native routing/DNS config belongs to an adapter.
type Configuration struct {
	Profiles      []profile.Profile
	ActiveProfile string
	AutoSelect    bool
	// AutoProfiles limits automatic selection to these opaque profile IDs.
	// Nil preserves the adapter-level default; application configurations set it explicitly.
	AutoProfiles   []string
	AutoPreference []string
	Routing        routing.RoutingState
	Host           HostNetwork
}

// HostNetwork adapts the core's direct (bypass) path to the machine. The
// zero value keeps the core's defaults: the OS's own default interface and
// the system resolver. See core/netdoctor for how it is chosen.
type HostNetwork struct {
	// DirectInterface is the OS interface name to bind direct traffic to
	// instead of auto-detecting it.
	DirectInterface string
	// DirectDNS is a plain DNS server (IP) for direct lookups instead of
	// the system resolver.
	DirectDNS string
}

type Backend interface {
	Info() Info
	Configure(Configuration) error
	Start(context.Context) error
	Stop() error
	IsRunning() bool
	SelectProfile(profileID string) error
	Delay(profileID, url string, timeoutMs int) (int, error)
	// Decode/Export operate on individual protocol links, never subscriptions.
	// Provider HTTP and installation identity belong to application services.
	// No engine-native objects cross
	// the boundary. Normalize upgrades old stored native profiles losslessly.
	Decode(input string) ([]profile.Profile, error)
	Export(profile.Profile) (string, error)
	Normalize(profile.Profile) (profile.Profile, error)
	Baseline()
	Diagnose(reason string)
}

type Factory func(baseDir string, log Logger) (Backend, error)

// DirectProber is optional: requests over the direct path of a running tunnel.
// A strict route sends everything from this process into the TUN, so only the
// engine can still reach the network the way a bypassed app does.
type DirectProber interface {
	// CanProbeDirect is false when the engine is not answering, so that a dead
	// engine does not read as a dead network.
	CanProbeDirect() bool
	// ProbeDirect returns nil when url answered at all.
	ProbeDirect(url string, timeoutMs int) error
}

// CoreState is what the owner of a core separate from the GUI knows about it.
type CoreState struct {
	// Supervised: the owner reports the rest. An older one, kept for a moment
	// by an update, does not.
	Supervised bool
	// Wanted: the core should be up. Wanted and not Running is for the GUI to
	// put right.
	Wanted  bool
	Running bool
	// ConfigCurrent: the core runs what this GUI would give it now.
	ConfigCurrent bool
}

// CoreSupervision is optional: the core lives apart from the GUI and outlasts it,
// so the GUI can find it down or on another release's config.
type CoreSupervision interface {
	CoreState() (CoreState, error)
	// StopForRestart is Stop with a start to follow: the core stays wanted, so
	// a start that fails is tried again.
	StopForRestart() error
}

// HotReloader is optional: the backend can apply what it was last configured
// with to the running core in place, without stopping it, so the tunnel and the
// connections it carries survive. Anything the core cannot change in place is
// declined (ReloadOutcome.Declined) and the caller restarts the core instead.
// An error means the same: nothing was applied.
type HotReloader interface {
	Reload(ctx context.Context) (ReloadOutcome, error)
}

// ReloadOutcome is what an in-place reload did, as tag counts.
type ReloadOutcome struct {
	Applied  bool
	Declined string
	Added    int
	Removed  int
	Replaced int
}

// TesterGated is optional: the engine has things it does only on a tester's
// install, while they earn their place: counting the TCP flows that reach each
// profile, and starting another attempt when a flow stays silent.
type TesterGated interface {
	SetTesterGate(tester func() bool)
}

// DirectTraffic is optional: the engine can show what goes over the direct path
// and send named hosts from it through the tunnel without restarting.
type DirectTraffic interface {
	// DirectConnections lists the open connections. Answered from the core's
	// memory, it sends nothing over the network.
	DirectConnections() ([]autotunnel.Conn, error)
	// AutoTunnelList is the file of hosts the running core follows.
	AutoTunnelList() string
}

// RouteObserver is optional: what the running engine can report about itself
// without sending anything over the network. Adapters without such a view leave
// the background checks to fall back on Delay.
type RouteObserver interface {
	// RoutedProfile is the profile auto mode currently sends new connections
	// through. "" when auto mode is off or the engine is not answering.
	RoutedProfile() string
	// ReceivedBytes is the bytes received per open connection through a profile,
	// keyed by connection ID.
	ReceivedBytes(profileID string) (map[string]uint64, error)
	// LastDelay is the engine's own last probe of a profile. known is false when
	// the engine is not probing it itself.
	LastDelay(profileID string) (ok bool, rttMs int, known bool)
}

// UDPRouteObserver is optional: the profile auto mode sends new UDP flows
// through, when the engine keeps a group of its own for UDP. "" when there is
// none or the engine is not answering.
type UDPRouteObserver interface {
	RoutedUDPProfile() string
}

// LegacyProfileSource is optional and used once when upgrading an install
// whose profiles existed only inside an engine-native configuration file.
type LegacyProfileSource interface {
	LegacyProfiles() ([]profile.Profile, error)
}

// ValidateConfiguration guards persisted policy as well as new UI commands.
// An adapter must advertise only features supported by its own implementation
// on the current OS. Unsupported policy never silently changes engines.
func ValidateConfiguration(c Configuration, caps Capabilities) error {
	if c.AutoSelect && !caps.AutoSelect {
		return fmt.Errorf("движок не поддерживает автоматический выбор сервера")
	}
	if !c.Routing.Enabled {
		return nil
	}
	list := c.Routing.Policy().Rules
	if len(list.Processes) > 0 && !caps.ProcessRouting {
		return fmt.Errorf("выбранный адаптер не поддерживает исключения приложений на этой ОС")
	}
	if len(list.Folders) > 0 && !caps.FolderRouting {
		return fmt.Errorf("выбранный адаптер не поддерживает правила по папкам на этой ОС")
	}
	if len(list.Domains) > 0 && !caps.DomainRouting {
		return fmt.Errorf("выбранный адаптер не поддерживает правила по доменам")
	}
	if len(list.IPs) > 0 && !caps.IPRouting {
		return fmt.Errorf("выбранный адаптер не поддерживает правила по IP")
	}
	return nil
}

// LegacyServiceScopes is optional migration metadata. Native history keys are
// known only to the adapter that originally wrote them.
type LegacyServiceScopes interface {
	ServiceScopes([]profile.Profile) map[string]string
}

// TrafficCounters are cumulative payload bytes processed by the engine,
// including traffic routed direct by user rules. They are not link capacity.
type TrafficCounters struct {
	UploadBytes   uint64 `json:"uploadBytes"`
	DownloadBytes uint64 `json:"downloadBytes"`
}
type TrafficObserver interface {
	TrafficCounters() (TrafficCounters, error)
}

// AutoPreferenceUpdater receives advisory ranking separately from configuration,
// so new hub measurements cannot restart a healthy tunnel.
type AutoPreferenceUpdater interface {
	SetAutoPreference(profileIDs []string, expiresAt int64) error
}
