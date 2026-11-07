# Relay capacity measured on observation points

Capacity tests run on owned Russian observation points. Subscriber clients do
not download synthetic payloads, switch through other profiles for a test, or
submit speed measurements. They receive observations in the subscription update.

## Measurement and ownership

`hikkari-control/internal/stand` queues `capacity-v1` independently of the
diagnostic knob matrix. Only active points marked `in_russia` receive it; older
probers without the `Hikkari-Stand-Features: capacity-v1` capability do not claim
these tasks. A point runs profiles sequentially using the verified client kit.
The isolated core uses the profile's actual transport configuration, a private
temporary config and a loopback proxy. It does not reconfigure a subscriber VPN.

Each profile has a 32 MiB download budget and bounded request/process deadlines.
The download grows from a small warm-up to larger transfers. A sample shorter
than 750 ms is marked as a lower bound. A direct control measurement flags a
possible observation-point/test-endpoint bottleneck; it cannot prove that the
server's own maximum was reached. The reported number is measured download
throughput on the tested path, not a guaranteed subscriber speed or upload rate.

The existing fleet renderer supplies supported outbound profiles. AmneziaWG
endpoints are not rendered by `FleetLinks` today and therefore have no capacity
measurement; missing data must remain missing, never become zero or fabricated
speed. Supporting these requires a managed benchmark peer, not a subscriber's key.

## Delivery and identity

The configuration key is SHA-256 of the canonical transport URL, excluding its
userinfo, display fragment and `hikkari-node` query parameter. Query ordering and
host/scheme case are canonicalized. A changed host, port, fingerprint or transport
parameter invalidates the old observation. Source URLs and subscriber credentials
are not sent in metrics. Only verified Hikkari subscription sources supply advice.

The subscription supplies `Hikkari-Relay-Capacity` for compatible header clients;
newer negotiated subscription envelopes can carry the same metrics in their
body. No dedicated measurement connection or upload is introduced on the client.
Delivery follows the existing subscription transport policy, including its
existing recovery path; this feature does not establish a separate tunnel.

The hub filters by the requested configuration keys and active RU points.
Observations expire after 90 minutes. An unavailable measurement store must not
break subscription delivery; its lookup is limited to 250 ms. `relay_capacity_settings`
controls scheduling (`enabled`, `interval_minutes`, default 30). Disabling it
also prevents claiming a previously queued capacity task.

## Automatic selection

Advice is restricted to the pinned Hikkari subscription. Fresh positive rates
order candidate profile IDs; missing and expired rates do not invent a ranking.
The client sends that ordering and deadline to the local sing-box API:

`PUT /proxies/auto/hikkari-preference`

```json
{"outbounds":["p-relay-a","p-relay-b"],"expiresAt":1794058000}
```

The core checks membership and local health. Initial advice can change which
node receives new connections; subsequent advice keeps a healthy held node.
Existing connections are not interrupted. Failed dials still use local failover;
hub unavailability never blocks recovery. The core itself enforces expiry even
when the GUI disappears. Advice is refreshed locally at least every 30 seconds
to recover from an independent core restart. Older cores return 404/405 and keep
their ordinary failover; optional advice support is checked again after 10 minutes.

This live advice API is separate from VPN configuration. Updating measured
numbers must not reconfigure or restart the tunnel. The optional updater is
currently implemented by the sing-box adapter; other adapters retain their
existing automatic-selection behavior.

## Core source and reproducible builds

`engine/core-patches/capacity-v1.patch` applies to the pinned Hikkari fork tag.
`scripts/apply-core-capacity-patch.sh` is used by Windows, macOS and the stand kit.
The resulting version has the `-capacity.1` suffix. The patch includes selection,
local API and expiry tests. It must be removed from these build surfaces together
when a future pinned fork tag incorporates the feature.

## Profile latency and history (relay-metrics-v2)

The measurement core performs three HTTPS `generate_204` requests through the
profile before download load, using fresh connections and 2-second deadlines.
At least two successful responses are required; their median is the RTT. This is
observation-point-to-endpoint HTTP latency, not the subscriber's own latency.
A failed download control does not suppress latency observations.

`relay_capacity_history` retains 24 hours of real observations. The hub reduces
each half-hour bucket to one latest sample per active RU point, then takes the
median of successful latency and throughput observations independently. Latest
failures replace prior successes; missing points remain gaps. At most 48 buckets
per configuration are returned. There is no invented initial chart.

Clients advertise `Hikkari-Features: relay-metrics-v2`. The subscription response
is `application/vnd.hikkari.subscription+json`, with `{version:2,nonce,ciphertext}`.
Nonce/ciphertext use JSON's base64 encoding of byte arrays. The payload contains
`{version:2,links,metrics}`. AES-256-GCM uses a random 12-byte nonce,
AAD `hikkari-subscription-v2`, and SHA-256 of that string, one NUL byte and the
192-bit random subscription token as the key. The bearer is not a user password.
The sealed response omits the plaintext measurement header. Legacy clients keep
base64 links and a compact history-free header. Authentication failures reject
the response instead of silently accepting different configuration.

The envelope travels over verified HTTPS through the existing subscription
transport, including when the VPN is off. It protects content, not the hub's IP,
SNI or traffic timing; it makes no claim to defeat traffic analysis or blocking.
No additional client report or connection to each relay is introduced.

For Hikkari profiles, `UptimeHistory` projects probe history for the UI. The local
tracker stops probing those profiles on subscriber installations. Tester mode
continues local measurements for diagnostics; the public graph never reads
those local measurements. Other providers keep their existing local ping.

The profile card switches chart and number together using `AppDurations.hover`
and `AppCurves.hover`. There is one metric row, no measurement tooltip, and
unchanged card height on hover. Expired/missing data show a dash.

The prober persists a completed stand result to a private outbox before sending
it. Failed delivery is retried before claiming another task, including after a
worker restart. A repeated acknowledgement or expired lease (409) clears the
outbox with a log entry. Production needs migration 037 plus hub and prober
updates; a Flutter-only reload cannot activate the backend.

## Client pacing

Background Hikkari subscription/metric refresh runs every 5 minutes with a
confirmed healthy tunnel and every 30 minutes without one. Offline retries and
startup/focus wakes obey the 30-minute lower bound. Restoring a healthy tunnel
returns to the 5-minute schedule. Server retry deadlines remain authoritative.
Initial import is immediate; third-party providers retain their prior schedule.
This policy affects subscription/metric delivery; telemetry, downdetector and
tester-log senders have their own schedules and are not changed by it.
