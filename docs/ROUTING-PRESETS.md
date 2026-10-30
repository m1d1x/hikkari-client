# Service routing presets

## Scope

The client infrastructure is implemented in `engine/core/presets` and exposed
through the existing FFI API. Production hosting and the administrator UI are
separate work. With no configured endpoint the client uses the embedded catalog
and explicitly says that the server is not connected.

The shipped catalog is `engine/core/presets/defaults.json`. It contains Steam,
Discord and Telegram groups. These are maintained rule sets, not a guarantee of
exhaustive discovery of every domain, executable, game or third-party CDN used
by those services. The details UI displays the exact rules.

Steam web domains were checked against
[Steam Support](https://help.steampowered.com/en/faqs/view/2EA8-4D75-DA21-31EB).
The shared `akamaihd.net` suffix is deliberately not included: routing that
entire CDN would affect unrelated services. Discord's legacy and current web
domains are documented by
[Discord Support](https://support.discord.com/hc/en-us/articles/360042987951-Discordapp-com-is-now-Discord-com).
Process matching uses exact executable names. Applications launched by Steam
as separate game processes are not automatically included in the Steam group.

## User behavior

- “автообновление списков” defaults to on. Turning it off freezes the downloaded
  catalog, cancels its event stream and prevents further catalog requests;
  existing selections remain applied and can still be changed locally.
- Each service is a group of domains, IP prefixes and per-platform process
  names. Clicking a card toggles it for the current routing mode. Enabled cards
  are bright; disabled cards are muted and monochrome. Right click
  or long press expands the rules for the current category and platform in place.
- `bypass` selections go directly; `proxy` selections go through VPN. Each mode
  has independent selections. The routing master switch still controls both.
- Manual rules remain in `routing.json`. Preset rules are computed as a
  separate overlay and never copied into manual storage. Overlaps are deduped;
  removing a group cannot remove a manually added rule.
- The built-in catalog enables all groups in both routing modes. Server `defaultModes` can
  prescribe defaults; an explicit local on/off choice wins across updates.
  A removed service contributes no rules, while its local override is retained
  in case that service ID returns. IDs must not be reused for unrelated services.

## Release configuration

The endpoint is a product setting, not a user-editable URL. Configure at Go
build time (example URLs must be replaced with the deployed service):

```sh
go build -buildmode=c-shared \
  -ldflags '-X NekkoVPN/core/presets.CatalogURL=https://config.example.com/v1/routing-presets.json -X NekkoVPN/core/presets.EventsURL=https://config.example.com/v1/routing-presets/events' \
  -o libnekkobox.dylib ./cmd/nekkobox-ffi
```

Deployment/development overrides (set before launching the host process): `HIKKARI_PRESET_CATALOG_URL` and
`HIKKARI_PRESET_EVENTS_URL`. Empty values disable those endpoints. The URLs
must use HTTPS; literal loopback IPs may use HTTP for local integration tests.
The catalog must remain fetchable before connecting VPN. Events are optional;
without events the periodic HTTP reconciliation still works.

Transport authentication currently relies on HTTPS certificate validation.
There is no document-signing protocol or production signing key in this change.
The public endpoint must not contain VPN passwords, user credentials or keys.

## HTTP catalog contract

`GET <CatalogURL>` returns HTTP 200 with UTF-8 JSON. Optional ETag is returned
unchanged in `If-None-Match`; HTTP 304 keeps the existing catalog. Version is a
positive, monotonically increasing integer. A given version is immutable;
different contents require an increment. Start a production catalog at version
3 or later to distinguish it from the embedded version 2.

```json
{
  "schemaVersion": 1,
  "version": 3,
  "services": [
    {
      "id": "steam",
      "name": "Steam",
      "icon": "steam",
      "domains": ["steampowered.com", "steamcommunity.com"],
      "ips": [],
      "processes": {
        "windows": ["steam.exe", "steamwebhelper.exe"],
        "darwin": ["steam_osx", "steamwebhelper"],
        "linux": ["steam", "steamwebhelper"]
      },
      "defaultModes": []
    }
  ]
}
```

`defaultModes` accepts `bypass` and/or `proxy`. The icon is a local identifier;
currently `steam`, `discord`, `telegram` have branded glyphs, others use the
application fallback icon. No external images, executable code, regex or native
engine config can be injected by this schema. Unknown fields are rejected.
Only the current platform's process list is compiled into the backend policy.

Limits: 1 MiB, 200 services, 2,000 rules per category/platform per service,
512 bytes per rule, 100 bytes per name. IDs use lowercase ASCII kebab-case.
Domain names must be canonical lowercase ASCII hosts (punycode when needed),
not URLs or wildcards. IPs must be canonical masked CIDR prefixes. Processes
must be bare executable names, not paths or globs.

## Reactive event contract

`GET <EventsURL>` returns HTTP 200, `Content-Type: text/event-stream`.
Whenever the catalog changes, publish a complete SSE event, for example:

```text
event: catalog
data: {"version":4}

```

Any event containing `data:` invalidates the catalog. The client refetches it
through the validated HTTP path; event data itself is never used as rules.
Send `: heartbeat` comments periodically. Flush each event and disable proxy
buffering. Reconnection uses bounded backoff; the connection is renewed at least
every five minutes. Reconnect triggers reconciliation, so missed events do not
lose updates. HTTP reconciliation also runs every 60 seconds while enabled.
The Flutter UI observes the local snapshot once a second. There is no promise
of instantaneous delivery while offline, asleep or blocked.

## Persistence, application and failure semantics

`routing-presets.json` in the application data directory stores the last accepted
catalog, sync preference and per-mode overrides. It is written using a flushed
temporary file and rename. Readers see a new snapshot only after persistence
and backend application succeed. Backend mutations are serialized with user
commands. Failed application attempts restore the previous disk state and
backend policy; rollback errors are reported rather than hidden.

Active routing changes reconnect a running backend because the current backend
contract has no live route-reload operation. Updating metadata, changing an
unselected group or editing only an inactive mode does not reconnect it.
Network/HTTP/validation failures retain the last accepted catalog; with no valid
cache, the embedded catalog is used. Old versions and conflicting contents of
the same version are rejected. Invalid updates do not advance the ETag.

Debug Flutter startup passes `routingPreview: true` to Init. Preset selections
then live only in memory and cannot reconfigure or restart the real backend.
This preserves the existing debug routing simulation. Release startup uses
normal persistence and application.

## Verification

`go test -race ./core/presets ./core/api` covers catalog validation, overlay
ownership, HTTP updates, ETag, persistence, opt-out including in-flight replies,
failed application rollback, SSE invalidation, running-backend reconfiguration
and debug isolation. `flutter test` covers selection, sync opt-out, hover and
right-click details.

Production endpoint reachability from Russian networks, actual VPN traffic and
cross-platform process discovery remain deployment/acceptance checks.

For native FFI verification after a release macOS build, run
`python3 bench/mac_presets_smoke.py`. It uses a temporary state directory and
loopback HTTP/SSE provider, checks the generated configuration with the bundled
sing-box, and never starts a VPN tunnel. It refuses to run while the shared VPN
controller is active.
