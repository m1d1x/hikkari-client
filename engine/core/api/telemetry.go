package api

import (
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"NekkoVPN/core/downdetector"
	"NekkoVPN/core/profile"
	"NekkoVPN/core/telemetry"
)

// newTelemetry wires the agent to the engine. The downdetector follows the
// same switch (docs/TELEMETRY.md §1), so OnChange carries it along.
func (c *Core) newTelemetry() *telemetry.Agent {
	return telemetry.New(telemetry.Options{
		StatePath: filepath.Join(c.baseDir, "telemetry.json"),
		QueuePath: filepath.Join(c.baseDir, "telemetry-queue.json"),
		URL:       presetEndpoint("HIKKARI_TELEMETRY_URL", telemetry.EventsURL),
		App:       c.telemetryApp,
		OnChange:  c.followTelemetry,
		Logf:      c.logger.Logf,
	})
}

// followTelemetry makes the downdetector's enabled state the switch's.
// Its own setting is kept only for share_private.
func (c *Core) followTelemetry(on bool) {
	s := c.downdetector.Settings()
	if s.Enabled == on {
		return
	}
	s.Enabled = on
	if err := c.downdetector.SetSettings(s); err != nil {
		c.logger.Logf("[telemetry] downdetector follow %v: %v", on, err)
	}
}

// telemetryApp describes this build. The installed version comes from the
// service, which owns updates; a build without it (debug, Linux) is "dev".
func (c *Core) telemetryApp() telemetry.App {
	version, channel := "dev", "dev"
	if st := c.UpdateStatus(); st.Installed != "" {
		version, channel = st.Installed, "stable"
	}
	osName := runtime.GOOS
	if osName == "darwin" {
		osName = "macos"
	}
	return telemetry.App{
		Version: version,
		Channel: channel,
		OS:      osName,
		OSMajor: telemetry.OSMajor(),
		Arch:    runtime.GOARCH,
	}
}

// TelemetrySettings is the switch as the settings screen shows it.
func (c *Core) TelemetrySettings() telemetry.Settings { return c.telemetry.Settings() }

// SetTelemetryEnabled records the user's explicit choice; off also stops
// the downdetector and forgets everything queued.
func (c *Core) SetTelemetryEnabled(on bool) error { return c.telemetry.SetEnabled(on) }

// TelemetryLastBatch is the last batch as sent, for "what is sent".
func (c *Core) TelemetryLastBatch() *telemetry.Batch { return c.telemetry.LastBatch() }

// TelemetryEvent queues an event the UI observes (screens, CTA). It is
// checked against the same contract as the engine's own events.
func (c *Core) TelemetryEvent(name string, fields map[string]any) error {
	return c.telemetry.Event(name, fields)
}

// TelemetryError queues a UI failure; the message is scrubbed here.
func (c *Core) TelemetryError(code, component, message string) error {
	return c.telemetry.Error(code, component, message)
}

// reportConnect records the outcome of a StartVPN.
func (c *Core) reportConnect(started time.Time, err error) {
	engineID := contractEngine(c.backend.Info().ID)
	proto := c.activeProto()
	fields := map[string]any{
		"engine": engineID,
		"proto":  proto,
		"ok":     err == nil,
		"secs":   int(time.Since(started).Seconds()),
	}
	if err != nil {
		fields["error"] = "engine.start"
		_ = c.telemetry.Error("engine.start", engineID, err.Error())
	}
	if e := c.telemetry.Event("connect", fields); e != nil {
		c.logger.Logf("[telemetry] connect: %v", e)
	}
	if err == nil {
		_ = c.telemetry.FirstConnect(engineID, proto)
	}
}

// contractEngine names an adapter as the contract does.
func contractEngine(id string) string {
	if id == "sing-box" {
		return "singbox"
	}
	return id
}

// activeProto folds the active profile's stack into the contract's list.
func (c *Core) activeProto() string {
	pf, err := c.profiles.Read()
	if err != nil {
		return "other"
	}
	for _, p := range pf.Profiles {
		if p.Name == pf.Active {
			return contractProto(profile.ProtoSlug(p))
		}
	}
	return "other"
}

func contractProto(slug string) string {
	parts := strings.Split(slug, "-")
	has := func(s string) bool {
		for _, p := range parts[1:] {
			if p == s {
				return true
			}
		}
		return false
	}
	switch parts[0] {
	case "vless":
		if has("xhttp") {
			return "vless-xhttp"
		}
		if has("reality") {
			return "vless-reality"
		}
	case "vmess", "trojan", "hysteria2":
		return parts[0]
	case "shadowsocks", "ss":
		return "shadowsocks"
	case "hy2":
		return "hysteria2"
	}
	return "other"
}

// syncDowndetectorSwitch aligns the downdetector with the telemetry switch
// at startup: a downdetector file from before the switch existed must not
// keep reporting when telemetry is off, nor stay off when it is on.
func (c *Core) syncDowndetectorSwitch() { c.followTelemetry(c.telemetry.Enabled()) }

// downdetectorSettingsFollowingSwitch keeps enabled under the telemetry
// switch: the UI may change share_private only.
func (c *Core) downdetectorSettingsFollowingSwitch(s downdetector.Settings) downdetector.Settings {
	s.Enabled = c.telemetry.Enabled()
	return s
}
