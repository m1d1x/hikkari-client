// Package engine defines the application contract for interchangeable VPN
// engines. It contains no native config, process, control API or OS details.
package engine

import (
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
	Routing       routing.RoutingState
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
