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
// AddRule typed signature:  AddRule(category, value string) error
// argsJSON:
{ "category": "domains", "value": "example.com" }
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

### Whitelist (bypass-VPN route rules)

| method       | args                                    | data        |
|--------------|-----------------------------------------|-------------|
| `Whitelist`  | —                                       | `Whitelist` |
| `AddRule`    | `{"category":"...","value":"..."}`      | `null`      |
| `RemoveRule` | `{"category":"...","value":"..."}`      | `null`      |

Valid `category` values: `"domains"`, `"ips"`, `"processes"`.

`Whitelist`:

```jsonc
{
  "domains":   ["example.com", ...],
  "ips":       ["10.0.0.0/8",  ...],
  "processes": ["chrome.exe",  ...]
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
and whitelist rules are shared across all profiles. If the core is running
it restarts it to apply the change.

`DeleteProfile` refuses to delete the last profile or the currently active
one — the caller must switch first.

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
