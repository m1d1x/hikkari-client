# nekkobox engine — API contract

This is the single contract every UI talks to: Wails (current Svelte
frontend), Flutter on desktop (via `dart:ffi` + cgo DLL), Flutter on
Android/iOS (via `gomobile bind`).

Two equivalent surfaces sit on top of one `api.Core`:

1. **Typed Go methods** (`methods.go`) — used directly by Wails bindings
   and by `gomobile bind` on mobile. Native Java/Kotlin/Swift methods are
   generated 1:1 from these.
2. **JSON dispatch** (`dispatch.go`) — `Core.Call(method, argsJSON)` returns
   a JSON envelope. This is what `cgo` exports as a single C function for
   `dart:ffi` on the desktop side.

The two surfaces are kept in sync by construction: dispatch routes every
method to the same typed method, so behaviour is identical regardless of
which surface a UI uses.

---

## Envelope

Every `Call()` response is a JSON object with this shape:

```json
{ "ok": true,  "data": <any> }
{ "ok": false, "error": "human-readable message" }
```

- `Call` itself never throws/returns a Go error — failures arrive as
  `{"ok": false, ...}`. This keeps the FFI boundary code path single.
- `data` is omitted when the method's "successful" return is `nil` (e.g.
  void methods like `StartVPN`).

## Arguments

Arguments are passed as a single JSON object whose field names match the
method's typed parameters. No-arg methods accept `""`, `"null"`, or `"{}"`.

```jsonc
// AddRule typed signature:  AddRule(mode, category, value string) error
// argsJSON:
{ "mode": "bypass", "category": "domains", "value": "example.com" }
```

Unknown fields in `argsJSON` are silently ignored (forward-compatibility).

---

## Methods

### Lifecycle

| method     | args                  | data    |
|------------|-----------------------|---------|
| `Init`     | `{"baseDir":"..."}`   | `null`  |
| `Shutdown` | —                     | `null`  |

`Init` is required exactly once before any other call. `Shutdown` is
idempotent. Calling `Init` twice returns an error.

### VPN control

| method       | args | data   |
|--------------|------|--------|
| `StartVPN`   | —    | `null` |
| `StopVPN`    | —    | `null` |
| `RestartVPN` | —    | `null` |
| `IsRunning`  | —    | `bool` |

`IsRunning` reports whether the sing-box Clash API answers on
`127.0.0.1:9090`. It does **not** mean the tunnel is actually carrying
traffic — use `Health` for that.

### Health & egress

| method       | args | data         |
|--------------|------|--------------|
| `Health`     | —    | `HealthInfo` |
| `ExternalIP` | —    | `string`     |

`HealthInfo`:

```jsonc
{
  "running":  true,            // sing-box answers Clash API
  "tunnelOK": true,            // egress IP differs from cached ISP IP
  "ip":       "203.0.113.1",   // current egress IP ("" on failure)
  "status":   "ok",            // "off" | "connecting" | "ok" | "broken"
  "message":  ""               // human reason, esp. for "broken"
}
```

Status machine:

- `off` — core down. Refreshes the cached ISP IP in the background.
- `connecting` — core up, no egress IP yet.
- `ok` — core up, egress IP differs from ISP baseline.
- `broken` — core up, egress IP == ISP IP (tunnel leaked).

### Logging

| method    | args | data     |
|-----------|------|----------|
| `LogTail` | —    | `string` |

Returns the last ~16 KB of the log file.

### Routing (split-tunnel)

Master toggle + mode + two independent lists. Routing state lives in
`routing.json` next to `config.json`; mutations recompile `route.rules`
and `route.final` in `config.json` so a restart picks them up.

Semantics:

- `enabled = false` — no split routing, everything via VPN.
- `mode = "bypass"` — items in `bypass` list go DIRECT, rest via VPN.
- `mode = "proxy"`  — items in `proxy` list go via VPN, rest DIRECT.

| method              | args                                                      | data           |
|---------------------|-----------------------------------------------------------|----------------|
| `Routing`           | —                                                         | `RoutingState` |
| `SetRoutingEnabled` | `{"on":true}`                                             | `null`         |
| `SetRoutingMode`    | `{"mode":"bypass"\|"proxy"}`                              | `null`         |
| `AddRule`           | `{"mode":"...","category":"...","value":"..."}`           | `null`         |
| `RemoveRule`        | `{"mode":"...","category":"...","value":"..."}`           | `null`         |

Valid `category` values: `"domains"`, `"ips"`, `"processes"`.

`RoutingState`:

```jsonc
{
  "enabled": true,
  "mode":    "bypass",
  "bypass":  { "domains": [...], "ips": [...], "processes": [...] },
  "proxy":   { "domains": [...], "ips": [...], "processes": [...] }
}
```

### Profiles

| method          | args                                   | data            |
|-----------------|----------------------------------------|-----------------|
| `Profiles`      | —                                      | `ProfileInfo[]` |
| `ActiveProfile` | —                                      | `string`        |
| `RenameProfile` | `{"name":"...","label":"..."}`         | `null`          |
| `DeleteProfile` | `{"name":"..."}`                       | `null`          |
| `SwitchProfile` | `{"name":"..."}`                       | `null`          |

`ProfileInfo`:

```jsonc
{
  "name":     "main",          // stable id
  "label":    "Основной",      // human-readable
  "active":   true,            // currently selected
  "endpoint": "1.2.3.4:51820", // "host:port" or ""
  "protocol": "AmneziaWG"      // pretty name ("AmneziaWG", "WireGuard", "—")
}
```

`SwitchProfile` swaps only the endpoint block in `config.json`; route, DNS,
and routing rules are shared across all profiles. If the core is running
it restarts it to apply the change.

`DeleteProfile` refuses to delete the last profile or the currently active
one — the caller must switch first.

### Profile geo (country flags)

| method             | args | data                  |
|---------------------|------|-----------------------|
| `ProfileCountries`  | —    | `map[string]string`   |

Resolves each profile's server host to a lowercase ISO 3166-1 alpha-2
country code, keyed by profile name:

```jsonc
{ "main": "us", "backup": "de", "no-endpoint": "" }
```

Resolution order: local embedded IPv4 range database, then (only if the
host is a domain) DNS, then a short-timeout online fallback for ranges the
local database doesn't know. Every result — including an unresolved `""` —
is cached in-process for 24h, so calling this repeatedly (e.g. after every
profile list refresh) is cheap after the first pass. Never blocks longer
than ~3.5s per uncached host.

### Uptime history

| method          | args | data                       |
|-----------------|------|----------------------------|
| `UptimeHistory` | —    | `map[string]PingSample[]`  |

Keys are profile names. Each profile gets a rolling window of ~300 samples
(≈5 minutes at 1 Hz).

`PingSample`:

```jsonc
{
  "ts":     1727550000,   // unix seconds
  "ok":     true,
  "rtt_ms": 42            // 0 if !ok
}
```

### Service uptime (on-demand pings of arbitrary user services)

On-demand, one-shot probing of user-defined services through the active
profile's Clash-API delay endpoint. Every result is also recorded in a
rolling history keyed by `"<activeTag>|<service name>"`.

| method                 | args                                                  | data                       |
|------------------------|-------------------------------------------------------|----------------------------|
| `PingServices`         | `{"activeTag":"...","services":[{"name","url"}]}`     | `ServicePingResult[]`      |
| `ServiceUptimeHistory` | —                                                     | `map[string]PingSample[]`  |

`PingServices` probes each service in parallel and returns one result per
input service (order preserved). It is a single-shot call — no background
loop.

`ServicePingTarget` (input element):

```jsonc
{
  "name": "youtube",              // service label / history key suffix
  "url":  "https://youtube.com"   // URL to probe via Clash-API delay
}
```

`ServicePingResult` (output element):

```jsonc
{
  "name":   "youtube",
  "ok":     true,
  "rtt_ms": 42            // 0 if !ok
}
```

`ServiceUptimeHistory` keys are `"<activeTag>|<service name>"`; each value is
a rolling window of `PingSample` (same shape as under *Uptime history*).

---

## Platform bindings

### Desktop (cgo + `dart:ffi`)

One C-exported function is enough:

```c
// future: cmd/nekkobox-ffi/main.go uses //export
char* nekkobox_call(const char* method, const char* args_json);
void  nekkobox_free(char* result);
```

The result is malloc'd JSON of the envelope shape; the caller must call
`nekkobox_free` to release it.

### Android / iOS (`gomobile bind`)

`gomobile bind` exposes typed methods directly:

```kotlin
val core = Api.newCore()
core.init(baseDir)
val health = core.health() // typed HealthInfo
```

Mobile builds may also use `core.call("Health", "")` if it's more ergonomic
on the Flutter side (single MethodChannel handler) — both work.

### Wails (current)

`app.go` is a thin facade that delegates to `api.Core`. The JSON shapes
in `methods.go` are identical to what Wails was already generating, so the
existing Svelte frontend continues to work without changes.

---

## Concurrency

- `Core.Call`, `Core.Init`, `Core.Shutdown` are safe to call from any
  goroutine. Init/Shutdown serialise on `Core.mu`; typed methods delegate
  to managers that have their own locking.
- The uptime ping loop is the only goroutine spawned by Init. It exits
  when `Shutdown` cancels the context.

## Stability

The method names, argument field names, and envelope shape are part of the
binary compatibility contract — renaming any of them breaks every UI at
once. Adding new methods or new optional fields to existing shapes is
backwards-compatible.
