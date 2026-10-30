# Routing intent contract v1

`core/routing.Document` is the portable description of user routing intent.
`RoutingState` is its platform-specific execution projection and the legacy UI
view. Neither representation contains engine-native outbound IDs or credentials.
Go owns validation, source composition and routing semantics. Dart DTOs only
transport the document. The shared cross-language example is
`testdata/document-v1.json`.

## Document

The required envelope is `schemaVersion: 1`, `enabled: bool`,
`mode: "bypass" | "proxy"`, and `sources: Source[]`.
An empty `sources` array is valid. Missing/null envelope fields, unknown fields,
duplicate JSON keys, unsupported versions and trailing JSON are errors.
Documents are limited to 4 MiB and 201 sources (one manual source plus up to
200 selected service groups). Source order is preserved for presentation and
stable compilation; it does not introduce rule priority.

Each source contains `id`, `kind`, `name`, `bypass`, and `proxy`:

- `id` is unique, 1–96 ASCII lowercase letters/digits/hyphens, starting with a
  letter or digit. Catalog source IDs use the `catalog-` prefix.
- `kind` is `manual`, `catalog`, or `snapshot`. This records provenance; it is
  not an executable instruction and does not cause network access.
- `name` is nonblank and at most 100 UTF-8 bytes.
- Each per-mode rule set has `domains: string[]`, `ips: string[]` and
  `processes: { darwin?: string[], windows?: string[], linux?: string[] }`.
  Missing/null category collections represent empty lists; they never mean
  inheritance. Both modes survive serialization even when one is inactive.

Domain rules are lowercase canonical hostnames matching the host and its
subdomains. IP rules use masked CIDR notation. Process rules are exact binary
names, never full paths or wildcards; spelling is preserved. Each category and
OS process list is limited to 2000 rules, each at most 512 UTF-8 bytes. Domains
are at most 253 bytes with DNS labels of at most 63 characters. Imported and
catalog data must already be canonical. Interactive input may accept a pasted
URL/path/address through `NormalizeValue`, then stores its canonical rule.
There is no silent normalization or partial acceptance of external documents.

## Resolution and adapter boundary

`Document.Resolve(platform)` validates the whole document, including inactive
modes and foreign-platform rules, and returns a new `RoutingState`. Only the
requested OS process rules contribute to that projection. The document retains
all OS rules. Resolution does not inspect the host OS, touch files or mutate
source values. Source lists are united independently per category and per mode;
exact duplicates contribute once. Removing a source cannot delete an overlapping
rule belonging to another source.

The catalog constructs sources only for groups selected in at least one mode.
A source contributes to a mode only when selected there. Source provenance and
all OS rules remain available in the document before resolution. Resolving does
not copy catalog rules into manual persistence.

`RoutingState.Policy()` produces the common adapter plan:
`rules`, `target: "direct" | "vpn"`, `defaultTarget: "direct" | "vpn"`.

- Disabled: empty match rules, default VPN.
- Bypass: the bypass rules go direct, default VPN.
- Proxy: the proxy rules go through VPN, default direct.

Categories are alternatives: domain OR IP OR process, not an AND expression.
The engine capabilities validator, sing-box compiler and Xray compiler consume
this shared plan. An adapter maps `vpn` to its selected server or automatic
selector and `direct` to its native direct outbound. Native tags, TUN creation,
control APIs and DNS syntax stay in the adapter. Unsupported active capabilities
fail explicitly, without fallback to another engine.

`ActiveEqual` compares the active behavior, ignoring source metadata, rule
ordering/duplicates and inactive-mode edits. It is used by catalog application
to decide whether the running backend needs restarting.

Existing native system rules (e.g. sing-box DNS prelude, LAN/ICMP routing and
QUIC handling) are preserved. This contract unifies **user routing intent**;
it does not claim those existing infrastructure policies are identical between
engines or prove identical live traffic behavior on every OS.

## Public API

`RoutingDocument()` returns the current v1 document, combining manual rules
with the selected service contents. Legacy manual process rules are attributed
to the installation OS. Returned values are detached from live catalog state.

`PreviewRoutingDocument({"document": Document})` strictly parses and resolves
for the installation OS, then checks the selected adapter's active capabilities.
On success it returns `platform`, `effective: RoutingState` and `policy: Policy`.
Errors use the standard API envelope. Preview does not write state, configure,
stop or start the backend, or select another adapter. The typed Go method takes
JSON text; the JSON FFI method accepts the document object in `document`.

The Dart client provides `RoutingDocument`, `RoutingSource`, `RoutingRules`,
`RoutingDocumentPreview`, and typed read/preview methods. Their shared fixture
is exercised by Go resolution tests and Dart round-trip tests.

## Compatibility and implementation boundary

Existing `Routing`, `SetRoutingEnabled`, `SetRoutingMode`, `AddRule`,
`RemoveRule` and service-selection methods remain supported. Local storage is
still `routing.json` plus `routing-presets.json`; this change does not perform a
destructive storage migration or add snapshot import/export. It introduces the
common versioned intent model, uses it for service composition, centralizes
strict rule validation, and shares execution semantics across both adapters.

## Runtime application service

Runtime application status and revisions are separate from the portable
Document. The Go application service owns command serialization, the 1500 ms
batch window, native configuration, restart decisions, persistence and recovery.
Flutter does not start a debounce timer, restart VPN for routing edits or invent
apply success. Its six-second hiding of the applied label is presentation only.

`RoutingStatus()` returns a nonblocking cached snapshot:

- `sequence`: increments for every published state, including phase changes.
- `revision`: increments for accepted manual changes and catalog transactions.
- `appliedRevision`: last revision accepted by the real backend/persistence path;
  preview never advances it. Revision 0 is the initial baseline.
- `status`: `idle`, `pending`, `applying`, `applied`, `failed`, or `preview`.
- `error`: the apply error, including any failed recovery operations.
- `appliedAt`: Unix epoch milliseconds of the last successful real apply.
- `preview`: whether the in-memory/preview executor is selected.
- `manual`: current desired manual policy; restored baseline after failed apply.
- `effective`: desired manual plus selected source rules. While pending/applying,
  it is a candidate; `appliedRevision` must not be confused with `revision`.

Readers can obtain `applying` while native configuration/Start is blocked. Dart
rejects older sequences and ignores responses from a previous engine session.
`Routing()` remains a compatibility projection of desired manual intent. The
file `routing.json` contains last committed manual intent, not pending edits.
`FlushRouting()` immediately applies the pending batch through the same service.

Manual changes while running accumulate for 1500 ms. Duplicate commands are
no-ops. Inactive-policy changes are saved without restarting the active VPN.
Offline changes apply immediately without starting VPN. Selecting/updating a
catalog group incorporates pending manual changes into the same transaction;
it does not schedule a second restart. Catalog delivery still owns HTTP/SSE,
ETag and its data, but delegates the whole apply/persist boundary to the service.

A transaction configures the adapter, restarts only when active behavior changed
and the backend was running, then persists. Configure/Stop/Start/write failures
restore the previous manual policy and native configuration. When appropriate,
recovery restarts the former runtime. Recovery errors are joined with the
original failure and remain `failed`; the service never turns them into applied.
Success means the adapter accepted its configuration/lifecycle operations, not
that tunnel traffic has been independently verified. Health remains separate.

Preview uses the same commands, validation, composition and status machinery,
with an in-memory routing store and no catalog path. It does not configure,
start, stop or restart a native engine, including during shutdown. Its terminal
status is `preview`. It does not write routing or native configuration files.

Stop/shutdown consume pending changes without an unnecessary restart; shutdown
cancels the batch timer and joins background observers before Core reuse. Pending
changes are volatile until committed; a crash can lose pending edits but cannot
publish an untested candidate over the last committed file. Atomic temporary
write/sync/rename is used for manual persistence. This is not a crash-atomic
multi-file journal: catalog and manual files have runtime rollback, and failed
rollback is reported explicitly.

Portable snapshot import/export and a new local document storage format remain
separate follow-up work. Existing storage and command compatibility are retained.
