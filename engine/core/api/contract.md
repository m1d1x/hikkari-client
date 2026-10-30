# nekkobox engine — API contract

This is the single contract every UI talks to: Flutter on desktop (via
`dart:ffi` + cgo DLL) and Flutter on Android/iOS (via `gomobile bind`).

Two equivalent surfaces sit on top of one `api.Core`:

1. **Typed Go methods** (`methods.go`) — used by `gomobile bind` on mobile.
   Native Java/Kotlin/Swift methods are generated 1:1 from these.
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
idempotent. Repeating `Init` with the same data directory is idempotent; a different directory requires `Shutdown`.

### VPN control

| method       | args | data   |
|--------------|------|--------|
| `StartVPN`   | —    | `null` |
| `StopVPN`    | —    | `null` |
| `RestartVPN` | —    | `null` |
| `IsRunning`  | —    | `bool` |
| `ValidateConfiguration` | — | validates stored profiles, automatic selection and routing against selected engine capabilities |

`IsRunning` reports liveness through the selected engine adapter. It does **not** mean the tunnel is actually carrying
traffic — use `Health` for that.

### Health & egress

| method       | args | data         |
|--------------|------|--------------|
| `Health`     | —    | `HealthInfo` |
| `ExternalIP` | —    | `string`     |

`HealthInfo`:

```jsonc
{
  "running":  true,            // selected engine reports running
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

The versioned user-intent contract is specified in
[`core/routing/contract.md`](../routing/contract.md). It is independent of native
engine configuration. `RoutingDocument` returns that document;
`PreviewRoutingDocument` accepts `{"document": <v1 document>}` and returns
`platform`, `effective` and the common `policy` (`rules`, `target`,
`defaultTarget`). Both operations are read-only. Existing methods below remain
compatibility commands for manual routing. `RoutingStatus` exposes the coherent
Go-owned revision/application snapshot; `FlushRouting` immediately consumes a
pending batch. Their fields and lifecycle are specified in the routing contract.
Flutter no longer schedules routing restarts or declares application success.

Master toggle + mode + two independent lists. Routing state lives in
`routing.json` in the client data directory stores the last committed policy.
`Routing()` returns desired manual intent, which can be pending in the Go batch
queue; `RoutingStatus()` distinguishes candidate and applied revisions. The adapter compiles this policy
for its selected runtime engine. Unsupported active rules fail
with an error before the engine starts.

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

Valid `category` values: `"domains"`, `"ips"`, `"processes"`, `"folders"` (path fragments such as `steamapps/common/`; requires the `folderRouting` capability).

`RoutingState`:

```jsonc
{
  "enabled": true,
  "mode":    "bypass",
  "bypass":  { "domains": [...], "ips": [...], "processes": [...], "folders": [...] },
  "proxy":   { "domains": [...], "ips": [...], "processes": [...], "folders": [...] }
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
  "protocol": "AmneziaWG",     // pretty name ("AmneziaWG", "WireGuard", "—")
  "subscription": {             // omitted for individually imported profiles
    "id": "sub-…",              // stable local grouping key for the subscription source
    "title": "Pelmeska_s",      // provider title / Content-Disposition filename
    "uploadBytes": 0,
    "downloadBytes": 655328376866,
    "totalBytes": 0,             // 0 means provider did not set a quota
    "expiresAt": 1795986000,     // Unix seconds; 0 means no expiry supplied
    "updateIntervalSeconds": 21600,
    "webPageUrl": "https://…",
    "supportUrl": "https://…"
  }
}
```

HTTP subscriptions synchronize automatically while Core is alive: normally every
5 minutes (plus scheduler/network latency). Startup, application
resume (`SubscriptionWake`, no arguments), failed connection starts and confirmed
unhealthy tunnels can accelerate a successful source to a 2-minute minimum
cooldown. A continuous unhealthy episode creates only one accelerated fetch,
deferred until cooldown permits it; recovery rearms the trigger. No refresh control or synchronization-error UI is required. Metadata
(expiry, uploaded/downloaded/total bytes and title) updates on every accepted
response; metadata-only updates never reconfigure or restart the VPN.

The original credential URL, stable source ID, scheduling/backoff, current
metadata and hidden server identities are stored atomically in the owner-only
`profiles.json`. `Profiles` returns public metadata only, never source URLs.
Repeated import of the same URL reconciles its existing source instead of
creating another group. Legacy groups regain their URL on reimport only when
exact connection overlap identifies one unowned group; URLs cannot otherwise be
recovered from old profiles.

Reconciliation preserves IDs and local label overrides using exact connection
identity first, then unambiguous protocol/host/port or provider labels. New
servers are added and missing servers are removed only after a complete valid
response. Other subscriptions and standalone imports are independent. A locally
deleted server is remembered as hidden and does not return after synchronization.
Removing the active server selects a remaining profile (preferably from the same
source); no remaining profiles clears auto mode and stops the engine.

Empty, malformed, partially unsupported and failed HTTP responses preserve the
last valid server list and metadata. Errors go to logs with links redacted.
Retries back off from 2 minutes to 15 minutes; authentication/rate-limit failures
wait at least 15 minutes and `Retry-After` can extend the wait to 24 hours. Focus
and unhealthy-tunnel hints do not bypass failure backoff. Configuration,
persistence or restart failure restores the previous snapshot/runtime where the
adapter and disk remain available; rollback errors are logged as well.

`Profile-Update-Interval` is expressed in hours by provider protocols and is
normalized to seconds in the metadata (see [provider documentation](https://docs.v2raytun.com/overview/supported-headers)).
Hikkari's product freshness target is 5 minutes independently of that hint;
server throttling/backoff still takes priority. The UI computes remaining days
from expiry and remaining traffic as max(0, total - upload - download).

`SwitchProfile` passes an opaque profile ID to the adapter. It uses live
selection when supported, otherwise restarts the engine. Routing policy
stays shared across profiles.

`DeleteProfile` allows deleting active and last profiles. Removing the active
profile selects the first remaining one. Removing the last profile stops the
VPN, clears the selection and auto mode, and leaves the profile list empty.
Starting a VPN with an empty profile list returns an actionable error.

HTTP(S) imports send a random installation ID in `X-Hwid`, persisted as
`subscription-device-id` in the data directory and reused across imports and
updates. It is independent of the VPN core. Provider device-limit errors are
returned before any profiles are written; error placeholders are not servers.

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
  "rtt_ms": 42,           // 0 if !ok
  "src":    "tunnel"      // how it was measured: "tunnel" | "tcp" | "icmp";
                          // on failure, the last method tried; absent on old samples
}
```

Only `tunnel` proves traffic passes through the proxy. `tcp` means the port
accepts a handshake (DPI may still kill the protocol), `icmp` only that the
host answers pings.

### Downdetector

Anonymous reports for the shared downdetector (docs/DOWNDETECTOR.md). Phase 0:
reports are built every 5 minutes and queued in `downdetector-outbox.json`;
nothing is sent yet.

| method                    | args                                        | data             |
|---------------------------|---------------------------------------------|------------------|
| `DowndetectorSettings`    | —                                           | `Settings`       |
| `SetDowndetectorSettings` | `{"enabled":bool,"share_private":bool}`     | —                |
| `DowndetectorLastReport`  | —                                           | `Report \| null` |

`Settings`: `enabled` (default `true`), `share_private` (default `false`).
Servers from an http(s) subscription are reported always; the user's own
(single links, pasted configs, profiles saved before `origin` existed) only
with `share_private`. Turning `enabled` off also drops the queued reports.

`Report` — the complete list of what a report carries:

```jsonc
{
  "install": "r-…",            // random, rotated weekly
  "asn":     12389,            // provider, 0 = unknown
  "country": "ru",             // "" = unknown
  "region":  "moscow",         // big city or region, "" = unknown
  "window":  [1761800100, 1761800400],
  "servers": [
    {"id": "s:…", "proto": "vless-reality", "src": "tunnel",
     "ok": 9, "fail": 1, "rtt_p50": 74}
  ]
}
```

### Service uptime (on-demand pings of arbitrary user services)

On-demand, one-shot probing of user-defined services through the active
profile through the selected adapter. Every result is also recorded in a
rolling history keyed by `"<profileId>|<service name>"`.

| method                 | args                                                  | data                       |
|------------------------|-------------------------------------------------------|----------------------------|
| `PingServices`         | `{"profileId":"...","services":[{"name","url"}]}`     | `ServicePingResult[]`      |
| `ServiceUptimeHistory` | —                                                     | `map[string]PingSample[]`  |

`PingServices` probes each service in parallel and returns one result per
input service (order preserved). It is a single-shot call — no background
loop.

`ServicePingTarget` (input element):

```jsonc
{
  "name": "youtube",              // service label / history key suffix
  "url":  "https://youtube.com"   // URL to probe through the adapter
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

`ServiceUptimeHistory` keys are `"<profileId>|<service name>"`; each value is
a rolling window of `PingSample` (same shape as under *Uptime history*).

### Updates (Windows)

Delivered by HikkariService; elsewhere `UpdateStatus` is always
`unavailable` (docs/AUTOUPDATE-ROADMAP.md).

| method            | args | data          |
|-------------------|------|---------------|
| `UpdateStatus`    | —    | `UpdateState` |
| `CheckUpdate`     | —    | `null`        |
| `RestartToUpdate` | —    | `null`        |

`CheckUpdate` asks the service to poll the channel now (ignored within a few
minutes of the last check). `RestartToUpdate` hands over to the launcher; the
UI must exit right after it returns — the launcher waits for this process,
has the service swap the staged version in and starts the new one.

`UpdateState`:

```jsonc
{
  "state":      "ready",   // idle | downloading | ready | error | unavailable
  "installed":  "1.0.0",
  "version":    "1.1.0",   // staged / downloading release
  "mandatory":  false,     // installed < the release's minimum_version
  "bytesDone":  0,         // while downloading
  "bytesTotal": 0,
  "error":      "",        // last failure; retried on schedule
  "checkedAt":  1761800000 // unix seconds of the last completed check
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

## Engine boundary

`EngineInfo` returns `id`, `name`, and `capabilities`: `tun`, `domainRouting`,
`ipRouting`, `processRouting`, `autoSelect`, `liveProfileSwitch`, `profileProbe`.
Capabilities describe the selected standalone adapter on the current OS.
An adapter must not depend on another engine to provide its advertised features.

`InitWithEngine` accepts `engine.Factory`. The concrete implementation is chosen
in `factory.go`; application services and Flutter use the common contract.
`InitWithVPN` remains a compatibility entry point for existing process injectors.

Subscriptions are expanded by `core/subscription`, with a persistent random
installation identity owned by the client. `Backend.Decode` receives individual
protocol URIs/INI, never provider URLs or HWID. Portable exports use the stored
connection and current label; legacy normalization belongs to the shared
compatibility codec. Unknown legacy native fields remain stored for lossless compilation.

Service history uses a versioned on-disk envelope after migration. The API still
returns a plain history map keyed by opaque profile ID and service name. Historical
scope mappings come from the compatibility codec; migration runs only once.

## Independent Xray adapter (macOS)

`Init` accepts optional `engineId`: `"sing-box"` (default) or `"xray"`.
An explicit engine change requires `Shutdown` before `Init`. Flutter selects
its initial adapter with `--dart-define=HIKKARI_ENGINE=xray`. All buttons and
application services keep using the same API and `engine.Backend` contract.

Xray mode uses **only Xray-core 26.10.21**. It does not import another adapter,
start another engine, generate another engine's config or call Clash API.
`xray.json` contains native Xray TUN, DNS, transport outbounds and routing
compiled from the common client policy. `xray-runtime.json` retains private
listener identities across client restarts. Both files are written atomically
with mode 0600. Profiles, rules, subscriptions, installation identity and history
remain client-owned.

Manual selection uses the common restart fallback (`liveProfileSwitch=false`).
Automatic selection uses native Xray observatory/leastPing. Per-profile HTTP(S)
probes use that profile's Xray SOCKS listener, independently of the active
selection. Unsupported protocols or non-portable legacy parameters return
errors. There is no fallback to another engine.

`core/hostprocess` provides a generic macOS process host: elevation, supervision,
readiness and saved/restored system DNS. It has no engine configuration schema,
profile IDs or routing-policy logic. The Xray adapter supplies only its binary,
arguments, readiness probe and DNS gateway. DNS snapshots live in a root-owned
per-installation directory and are retained for retry when restoration fails.
Shutdown errors propagate through the common API. Automated host tests use a
fake networksetup executable and do not touch system DNS.

Old saved native profiles and history are migrated by `core/profile/legacy`,
a pure compatibility codec with no runtime-engine dependency. Unknown native
fields remain stored so changing engines never destroys the original data.

Verification on **macOS 26.3 (25D125), arm64**, Xray **26.10.21**:

- Real native TUN carried a request through VLESS. A curl process exclusion
  compiled from the same common policy selected the direct outbound instead.
- The privileged fixture used only `198.18.0.0/15`, never changed DNS or the
  default route, removed its route, and checked the restored route/DNS state.
- Xray-only loopback tests verify bypass/proxy modes, domain/IP rules, manual
  profile changes, probes, native automatic selection with a failed server,
  TCP and UDP. An import-graph test forbids cross-adapter dependencies.

The complete GUI connection through the installed platform host and its system
DNS changes has not been exercised. Windows builds and live subscriptions remain
outside this verification.

```sh
cd engine
HIKKARI_XRAY_BINARY="$PWD/../archive/xray" go test -race ./...
```

After building, run `python3 bench/mac_import_smoke.py` and
`python3 bench/mac_xray_smoke.py` from the root. The latter initializes Xray
without a sing-box binary in its data directory and validates only native Xray
configuration. Both use temporary data and leave a running VPN untouched.

The privileged proof is `bench/native_xray_tun_smoke.py`. Its input is exported
explicitly by `TestPrepareNativeTUNFixture`; it is never run by the regular test
suite. Official version/OS sources:
[pinned release](https://github.com/XTLS/Xray-core/releases/tag/v26.10.21),
[macOS TUN](https://github.com/XTLS/Xray-core/blob/v26.10.21/proxy/tun/tun_darwin.go),
[macOS process lookup](https://github.com/XTLS/Xray-core/blob/v26.10.21/common/net/find_process_darwin.go).

## Service routing presets

`Init` accepts optional `routingPreview: bool` (default false). Flutter debug
startup uses true: preset choices are in-memory and do not reconfigure a VPN.

- `RoutingPresets()` → snapshot: `syncEnabled`, `catalog`, `choices`, `source`,
  `configured`, `connected`, `platform`, `lastChecked`, `error`.
- `SetPresetSync({on: bool})` → null; default on, off freezes the catalog.
- `SetServicePreset({mode: "bypass"|"proxy", id: string, on: bool})` → null;
  applies the group without merging it into manual storage.
- `RefreshRoutingPresets()` → null; fetches/applies now when sync is enabled.

`Routing()` continues returning manual rules. The backend receives those rules
plus the selected service overlay. The complete wire format, deployment
configuration, event contract and fallback semantics are documented in
[ROUTING-PRESETS.md](../../../docs/ROUTING-PRESETS.md).
