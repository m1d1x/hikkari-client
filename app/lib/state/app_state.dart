import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/foundation.dart';

import '../core/envelope.dart';
import '../core/models.dart';
import '../core/nekkobox_core.dart';

/// Dev-only kill switch for the routing pipeline. When true, every routing
/// mutation (master toggle, mode flip, list edit) updates Dart state only
/// and logs to the debug console — the engine is NOT touched, so an external
/// sing-box session keeps running. Flip back to false to wire to backend.
const bool kRoutingDryRun = kDebugMode;

/// Dev-only kill switch for everything else that would poke the real engine:
/// VPN on/off, profile switch/rename/delete, auto-mode, and profile import.
/// While true these mutate Dart state only (with simulated latency so the UI
/// still feels real) and never call into [NekkoboxCore] — no real VPN
/// connect/disconnect flapping while iterating on the frontend. Flip back to
/// false to wire to backend.
const bool kEngineDryRun = kDebugMode;

void _rlog(String msg) {
  debugPrint('[routing${kRoutingDryRun ? '/dry' : ''}] $msg');
}

void _elog(String msg) {
  debugPrint('[engine${kEngineDryRun ? '/dry' : ''}] $msg');
}

/// Single source of truth for the UI. Owns the [NekkoboxCore] handle, polls
/// Health() once a second, and exposes everything the screens need through
/// plain getters. Widgets watch the notifier via [provider] and rebuild on
/// notify; methods that mutate (start/stop/switch/etc.) call notifyListeners
/// at the right moments so the UI stays in sync.
///
/// Polling cadence:
///   - health: every 1s (matches the Svelte refresh loop)
///   - profiles / whitelist: on demand (loaded once at boot + after writes)
///   - uptime history: every 2s when its widget is on screen (later — wired
///     by the widget itself via [refreshUptime]).
class AppState extends ChangeNotifier {
  final NekkoboxCore _core;
  NekkoboxCore get core => _core;
  Timer? _healthTimer;

  HealthInfo? _health;
  // Polled every [_updatePollTicks] health ticks — it changes on the scale of
  // minutes, and each read is a round trip to the service.
  UpdateStatus _update = UpdateStatus.unavailable;
  int _tickCount = 0;
  static const _updatePollTicks = 5;
  List<ProfileInfo> _profiles = const [];
  // Lowercase ISO country code per profile name, for the flag badge next to
  // each profile's title. Populated in the background by
  // [_refreshProfileCountries] — never awaited on a profile-list reload, since
  // a cold cache can take a few seconds (DNS + online fallback per host).
  Map<String, String> _profileCountries = const {};
  // True while a background [_refreshProfileCountries] pass is in flight. Used
  // together with [_profileCountries] so the flag badge can tell "still
  // resolving" (show a spinner) from "resolved, no country" (show nothing):
  // a profile that isn't yet a key in the map while this is true is loading.
  bool _resolvingCountries = false;
  RoutingState _routing = const RoutingState.initial();
  Map<String, List<PingSample>> _uptime = const {};
  Map<String, List<PingSample>> _serviceUptime = const {};
  ServerCleanliness _cleanliness = ServerCleanliness.off;
  bool _busy = false;
  // True between a routing list edit and the engine restart that picks it up.
  // The UI uses this for the "применяется… / применено" badge near the mode row.
  RoutingApplyStatus _routingApply = RoutingApplyStatus.idle;
  Timer? _routingDebounce;
  Timer? _routingAppliedFade;
  bool _autoMode = false;
  String? _lastError;
  // Timestamp of the last VPN start / profile switch. The tunnel needs a
  // moment to actually pass traffic (handshake, routing table settle), so
  // the very first probe(s) right after this genuinely fail — that's not
  // the same as the profile being dead. Widgets use [isWarmingUp] to show
  // "checking" instead of "down" during this window.
  DateTime? _connectedSince;

  AppState(this._core);

  // ----- getters -----
  HealthInfo? get health => _health;
  UpdateStatus get update => _update;
  List<ProfileInfo> get profiles => _profiles;
  Map<String, String> get profileCountries => _profileCountries;
  /// Resolved lowercase ISO country code for [name], or null if not resolved
  /// (either still loading, or the host couldn't be geolocated).
  String? countryCodeFor(String name) {
    final code = _profileCountries[name];
    return (code == null || code.isEmpty) ? null : code;
  }
  /// True when [name]'s flag is still being resolved in the background — the
  /// badge shows a spinner in this window, then the flag bounces in once the
  /// code lands. A profile already present in [_profileCountries] (even with
  /// an empty value) is done resolving, so it's not loading.
  bool isCountryLoading(String name) =>
      _resolvingCountries && !_profileCountries.containsKey(name);
  RoutingState get routing => _routing;
  Map<String, List<PingSample>> get uptime => _uptime;
  Map<String, List<PingSample>> get serviceUptime => _serviceUptime;

  /// "Whiteness" of the active egress. Backed by the engine's cached report;
  /// refreshed each tick alongside health (the engine read is instant).
  ServerCleanliness get cleanliness => _cleanliness;
  bool get busy => _busy;
  RoutingApplyStatus get routingApply => _routingApply;
  bool get autoMode => _autoMode;
  String? get lastError => _lastError;
  bool get isRunning => _health?.running ?? false;
  // True for ~10s after a VPN start / profile switch. During this window
  // uptime cards should show "checking" rather than "down" for probes that
  // fail simply because the tunnel hasn't finished coming up yet.
  bool get isWarmingUp {
    final since = _connectedSince;
    if (since == null) return false;
    return DateTime.now().difference(since) < const Duration(seconds: 10);
  }
  String? get activeProfileName {
    for (final p in _profiles) {
      if (p.active) return p.name;
    }
    return null;
  }

  /// Sing-box proxy tag of the active profile, or null when there is no
  /// active profile. Matches the per-profile tag the engine's uptime tracker
  /// uses (`profile.ProfileTag` = `"p-<name>"`), so service pings and their
  /// history land under the same routed member the VPN is currently using.
  String? get activeTag {
    final name = activeProfileName;
    return name == null ? null : 'p-$name';
  }

  // ----- service ping (on-demand, sheet-driven) -----

  /// Probes each service through the active profile's Clash-API delay
  /// endpoint and records the outcome in the engine's service uptime history.
  /// Thin pass-through to [NekkoboxCore.pingServices] — widgets don't hold a
  /// core handle. Returns an empty list (never throws) when the engine can't
  /// be reached, so the sheet degrades gracefully.
  Future<List<ServicePingResultDto>> pingServices(
    String activeTag,
    List<ServicePingTargetDto> services,
  ) async {
    try {
      return await _core.pingServices(activeTag, services);
    } on NekkoboxError catch (e) {
      _elog('pingServices failed: ${e.message}');
      return const [];
    } catch (e) {
      _elog('pingServices failed: $e');
      return const [];
    }
  }

  /// Snapshot of the whole service-ping history, keyed by
  /// `"<activeTag>|<service name>"`. Pass-through to the core; returns an
  /// empty map on failure.
  Future<Map<String, List<PingSample>>> serviceUptimeHistory() async {
    try {
      return await _core.serviceUptimeHistory();
    } on NekkoboxError catch (e) {
      _elog('serviceUptimeHistory failed: ${e.message}');
      return const {};
    } catch (e) {
      _elog('serviceUptimeHistory failed: $e');
      return const {};
    }
  }

  /// Initial load + start the periodic poll. Call once after the FFI handle
  /// is initialised. Health and uptime tick together at 1Hz so a single
  /// notifyListeners() updates everything that depends on either.
  Future<void> start() async {
    await _safeReload(() async {
      // In dry-run we deliberately skip reading routing from the engine —
      // any leftover live config shouldn't taint the UI we're tweaking.
      if (!kRoutingDryRun) {
        _routing = await _core.routing();
      } else {
        _rlog('start: dry-run, routing kept at ${_routing.mode.wire} '
            'enabled=${_routing.enabled}');
      }
      if (kEngineDryRun) {
        _elog('start: dry-run, seeding fake profiles/health');
        _profiles = _dryProfiles;
        _health = _dryHealth;
        _cleanliness = _dryCleanliness;
        _uptime = {'dry-demo': _seedDryUptime()};
        _autoMode = false;
      } else {
        _profiles = await _core.profiles();
        _health = await _core.health();
        _uptime = await _core.uptimeHistory();
        _autoMode = await _core.autoMode();
      }
    });
    _healthTimer ??=
        Timer.periodic(const Duration(seconds: 1), (_) => _tick());
    _refreshProfileCountries();
    if (!kEngineDryRun) {
      // Every launch is a reason to look: the service throttles repeats.
      unawaited(_core.checkUpdate().catchError((Object e) {
        _elog('checkUpdate failed: $e');
      }));
      unawaited(_refreshUpdate());
    }
  }

  Future<void> _refreshUpdate() async {
    try {
      final next = await _core.updateStatus();
      if (next.state != _update.state ||
          next.version != _update.version ||
          next.bytesDone != _update.bytesDone) {
        _update = next;
        notifyListeners();
      }
    } catch (e) {
      _elog('updateStatus failed: $e');
    }
  }

  /// «Перезапустить»: the launcher takes over and the app exits. Returns
  /// false (with [lastError] set) if the hand-over could not start.
  Future<bool> restartToUpdate() async {
    try {
      await _core.restartToUpdate();
      return true;
    } on NekkoboxError catch (e) {
      _lastError = e.message;
    } catch (e) {
      _lastError = '$e';
    }
    notifyListeners();
    return false;
  }

  /// Resolves each profile's country (for the flag badge) in the background.
  /// Fire-and-forget on purpose: [NekkoboxCore.profileCountries] can take a
  /// few seconds on a cold cache (DNS + online fallback), but it runs on the
  /// engine worker isolate, so awaiting it here never blocks the UI. While
  /// it's in flight [_resolvingCountries] is true, so freshly added profiles
  /// show a spinner where the flag will land. Skipped entirely in dry-run.
  /// Degrades to leaving the previous map in place on failure.
  Future<void> _refreshProfileCountries() async {
    if (kEngineDryRun) return;
    _resolvingCountries = true;
    notifyListeners();
    try {
      _profileCountries = await _core.profileCountries();
    } on NekkoboxError catch (e) {
      _elog('profileCountries failed: ${e.message}');
    } catch (e) {
      _elog('profileCountries failed: $e');
    } finally {
      _resolvingCountries = false;
      notifyListeners();
    }
  }

  // ----- engine dry-run fakes -----

  static const _dryHealthOff = HealthInfo(
    running: false,
    tunnelOK: false,
    ip: '—',
    status: 'off',
    message: '',
  );
  // Фейк «чистоты» для dry-run (debug): плашка рендерится с правдоподобными
  // моками, чтобы крутить UI без движка и включённого VPN.
  static const _dryCleanliness = ServerCleanliness(
    running: true,
    checked: true,
    ip: '92.246.139.199',
    asn: 'AS210644',
    asname: 'AEZA-AS',
    asnType: 'hosting',
    asnCountry: 'RU',
    isp: 'Aeza International LTD',
    org: 'Aeza',
    country: 'DE',
    city: 'Frankfurt',
    isHosting: true,
    isMobile: false,
    isProxy: false,
    isVpn: false,
    isTor: false,
    isAbuser: true,
    isCrawler: false,
    abuserLabel: 'High',
    fraudScore: -1,
    abuseEmail: 'abuse@aeza.net',
    cfColo: 'FRA',
    cfWarp: 'off',
    score: 27,
    verdict: 'datacenter',
    message: 'датацентр · DE · AEZA-AS',
    reasons: [
      CleanlinessReason(
        severity: 'high',
        title:
            'Хостинг/датацентр IP — Cloudflare Turnstile почти всегда челленджит такие',
        fix: 'Перейти на residential или mobile выход — это устраняет главную причину фрода.',
      ),
      CleanlinessReason(
        severity: 'high',
        title: 'IP/подсеть в abuse-фидах (репутация: High)',
        fix: 'Сменить IP/подсеть — с этого сервера уже спамили/абузили.',
      ),
      CleanlinessReason(
        severity: 'med',
        title:
            'ASN зарегистрирован в RU (гео IP — DE): CF повышает подозрительность',
        fix: 'Выбрать провайдера с ASN в той же юрисдикции, что и гео.',
      ),
    ],
    checkedAt: '',
  );

  static const _dryHealthOn = HealthInfo(
    running: true,
    tunnelOK: true,
    ip: '10.13.37.1',
    status: 'ok',
    message: '',
  );
  HealthInfo _dryHealth = _dryHealthOff;

  // Fake but plausible RTT wave so the sparkline has something to draw in
  // dry-run: a slow sine drift (network "breathing") plus small per-sample
  // jitter, with an occasional dropped probe so the "outage" rendering path
  // gets exercised too. Seeded deterministically (no Random) so hot reload
  // doesn't jump the chart around.
  List<PingSample> _seedDryUptime({int count = 90, int startTs = 0}) {
    final out = <PingSample>[];
    for (var i = 0; i < count; i++) {
      out.add(_dryPingSample(startTs + i));
    }
    return out;
  }

  PingSample _dryPingSample(int i) {
    final wave = 90 + 40 * math.sin(i / 11) + 15 * math.sin(i / 3.3 + 1);
    final jitter = (i * 37) % 23 - 11; // cheap deterministic pseudo-noise
    final dropped = i % 17 == 0; // occasional missed probe
    return PingSample(
      ts: i,
      ok: !dropped,
      rttMs: dropped ? 0 : (wave + jitter).round().clamp(20, 400),
    );
  }

  // Appends one fresh sample per tick to the active profile's history so the
  // sparkline keeps animating in dry-run, same as it would against the real
  // engine's 1Hz probe.
  void _growDryUptime() {
    final name = activeProfileName;
    if (name == null) return;
    final hist = List<PingSample>.of(_uptime[name] ?? const []);
    hist.add(_dryPingSample(hist.length));
    if (hist.length > 200) hist.removeAt(0);
    _uptime = {..._uptime, name: hist};
    notifyListeners();
  }

  List<ProfileInfo> _dryProfiles = const [
    ProfileInfo(
      name: 'dry-demo',
      label: 'Демо-профиль (dry-run)',
      active: true,
      endpoint: 'demo.example.com:443',
      protocol: 'vless',
    ),
  ];

  @override
  void dispose() {
    _routingDebounce?.cancel();
    _routingDebounce = null;
    _routingAppliedFade?.cancel();
    _routingAppliedFade = null;
    _healthTimer?.cancel();
    _healthTimer = null;
    super.dispose();
  }

  // ----- mutations -----

  Future<void> toggleVpn() async {
    if (kEngineDryRun) {
      await _withBusy(() async {
        if (isRunning) {
          _elog('toggleVpn: stop (simulated)');
          await Future<void>.delayed(const Duration(milliseconds: 250));
          _dryHealth = _dryHealthOff;
          _health = _dryHealth;
          return;
        }
        _elog('toggleVpn: start (simulated)');
        _connectedSince = DateTime.now();
        _dryHealth = const HealthInfo(
          running: false,
          tunnelOK: false,
          ip: '—',
          status: 'connecting',
          message: '',
        );
        _health = _dryHealth;
        notifyListeners();
        await Future<void>.delayed(const Duration(milliseconds: 700));
        _dryHealth = _dryHealthOn;
        _health = _dryHealth;
      });
      return;
    }
    await _withBusy(() async {
      if (isRunning) {
        await _core.stopVpnAsync();
        _health = await _core.health();
        return;
      }
      await _core.startVpnAsync();
      _connectedSince = DateTime.now();
      // sing-box can exit asynchronously a few hundred ms after Start (e.g.
      // TUN access denied). Give it a moment, then sample health — if the
      // core died, pull the last FATAL/[singbox.err] line from the log so
      // the UI shows something more actionable than just "выкл".
      await Future<void>.delayed(const Duration(milliseconds: 700));
      _health = await _core.health();
      if (!isRunning) {
        final msg = _extractLogError(await _core.logTail());
        if (msg != null) {
          throw NekkoboxError(msg);
        }
      }
    });
  }

  /// Extracts the most recent fatal-ish line from the engine log. We look
  /// for sing-box's own FATAL marker first, then any [singbox.err] pipe
  /// line; falls back to the last "failed" line. Returns null if nothing
  /// useful is in the tail (so the caller doesn't overwrite a null error
  /// with garbage).
  String? _extractLogError(String tail) {
    final lines = tail.split('\n');
    String? lastErr;
    for (final raw in lines) {
      // Strip ANSI color codes that sing-box embeds (e.g. \x1b[31mFATAL\x1b[0m).
      final line = raw.replaceAll(RegExp(r'\x1B\[[0-9;]*[A-Za-z]'), '');
      if (line.contains('FATAL') ||
          (line.contains('[singbox.err]') && !line.contains(' INFO ') && !line.contains(' DEBUG ')) ||
          line.contains('failed to start') ||
          line.contains('Access is denied')) {
        lastErr = line.trim();
      }
    }
    return lastErr;
  }

  Future<void> switchProfile(String name) async {
    if (kEngineDryRun) {
      _elog('switchProfile: $name (simulated)');
      _connectedSince = DateTime.now();
      await _withBusy(() {
        _dryProfiles = [
          for (final p in _dryProfiles)
            p.name == name
                ? ProfileInfo(
                    name: p.name,
                    label: p.label,
                    active: true,
                    endpoint: p.endpoint,
                    protocol: p.protocol,
                  )
                : ProfileInfo(
                    name: p.name,
                    label: p.label,
                    active: false,
                    endpoint: p.endpoint,
                    protocol: p.protocol,
                  ),
        ];
        _profiles = _dryProfiles;
        _uptime.putIfAbsent(name, () => _seedDryUptime());
      });
      return;
    }
    await _withBusy(() async {
      try {
        await _core.switchProfile(name);
        _connectedSince = DateTime.now();
      } finally {
        _profiles = await _core.profiles();
        _health = await _core.health();
      }
    });
    _refreshProfileCountries();
  }

  Future<void> renameProfile(String name, String label) async {
    if (kEngineDryRun) {
      _elog('renameProfile: $name -> $label (simulated)');
      await _withBusy(() {
        _dryProfiles = [
          for (final p in _dryProfiles)
            p.name == name
                ? ProfileInfo(
                    name: p.name,
                    label: label,
                    active: p.active,
                    endpoint: p.endpoint,
                    protocol: p.protocol,
                  )
                : p,
        ];
        _profiles = _dryProfiles;
      });
      return;
    }
    await _withBusy(() async {
      await _core.renameProfile(name, label);
      _profiles = await _core.profiles();
    });
  }

  Future<void> deleteProfile(String name) async {
    if (kEngineDryRun) {
      _elog('deleteProfile: $name (simulated)');
      await _withBusy(() {
        _dryProfiles = _dryProfiles.where((p) => p.name != name).toList();
        _profiles = _dryProfiles;
      });
      return;
    }
    await _withBusy(() async {
      await _core.deleteProfile(name);
      _profiles = await _core.profiles();
    });
  }

  /// Toggles auto-fastest mode. The engine reload (when running) means
  /// there's a 1-2s pause; AppState's _withBusy lock hides the spinner.
  Future<void> setAutoMode(bool on) async {
    if (kEngineDryRun) {
      _elog('setAutoMode: $on (simulated)');
      await _withBusy(() async {
        await Future<void>.delayed(const Duration(milliseconds: 300));
        _autoMode = on;
      });
      return;
    }
    await _withBusy(() async {
      await _core.setAutoMode(on);
      _autoMode = await _core.autoMode();
      _health = await _core.health();
    });
  }

  /// Imports a profile from a server URI. Returns the generated profile
  /// name on success, or null if the import failed (error is stored in
  /// [lastError]).
  Future<String?> addProfileFromUri(String uri) async {
    if (kEngineDryRun) {
      _elog('addProfileFromUri: $uri (simulated)');
      String? name;
      await _withBusy(() {
        name = 'dry-${_dryProfiles.length + 1}';
        _dryProfiles = [
          ..._dryProfiles,
          ProfileInfo(
            name: name!,
            label: 'Импортированный профиль ${_dryProfiles.length + 1}',
            active: false,
            endpoint: uri,
            protocol: 'vless',
          ),
        ];
        _profiles = _dryProfiles;
      });
      return name;
    }
    String? name;
    await _withBusy(() async {
      name = await _core.addProfileFromUri(uri);
      _profiles = await _core.profiles();
    });
    _refreshProfileCountries();
    return name;
  }

  /// Shareable subscription URI for [name], or null if the engine can't
  /// export it (older backend without `ExportProfileURI`, or a profile that
  /// can't be serialised). Never throws — the share sheet degrades to a
  /// "ссылка недоступна" state on null.
  Future<String?> exportProfileUri(String name) async {
    if (kEngineDryRun) {
      final p = _profiles.firstWhere(
        (p) => p.name == name,
        orElse: () => ProfileInfo(
            name: name, label: name, active: false, endpoint: '', protocol: 'vless'),
      );
      final proto = p.protocol.isEmpty ? 'vless' : p.protocol;
      final host = p.endpoint.isEmpty ? 'demo.example.com:443' : p.endpoint;
      return '$proto://dry-run-uuid@$host'
          '?type=tcp#${Uri.encodeComponent(p.label)}';
    }
    try {
      return await _core.exportProfileUri(name);
    } on NekkoboxError catch (e) {
      _elog('exportProfileUri failed: ${e.message}');
      return null;
    } catch (e) {
      _elog('exportProfileUri failed: $e');
      return null;
    }
  }

  // Debounce window for batching list edits — paste of 50 domains from
  // Telegram triggers one engine restart, not 50.
  static const Duration _routingApplyDelay = Duration(milliseconds: 1500);

  Future<void> addRule(String category, String value) async {
    _rlog('addRule mode=${_routing.mode.wire} cat=$category value="$value"');
    final list = _routing.active.withAdded(category, value);
    _routing = _routing.mode == RoutingMode.bypass
        ? _routing.copyWith(bypass: list)
        : _routing.copyWith(proxy: list);
    notifyListeners();
    if (!kRoutingDryRun) {
      await _withBusy(() async {
        await _core.addRule(_routing.mode, category, value);
        _routing = await _core.routing();
      });
    }
    _markRoutingDirty();
  }

  Future<void> removeRule(String category, String value) async {
    _rlog('removeRule mode=${_routing.mode.wire} cat=$category value="$value"');
    final list = _routing.active.withRemoved(category, value);
    _routing = _routing.mode == RoutingMode.bypass
        ? _routing.copyWith(bypass: list)
        : _routing.copyWith(proxy: list);
    notifyListeners();
    if (!kRoutingDryRun) {
      await _withBusy(() async {
        await _core.removeRule(_routing.mode, category, value);
        _routing = await _core.routing();
      });
    }
    _markRoutingDirty();
  }

  Future<void> setRoutingEnabled(bool on) async {
    if (_routing.enabled == on) return;
    _rlog('setRoutingEnabled $on');
    _routing = _routing.copyWith(enabled: on);
    notifyListeners();
    if (!kRoutingDryRun) {
      await _withBusy(() async {
        await _core.setRoutingEnabled(on);
        _routing = await _core.routing();
      });
    }
    _markRoutingDirty();
  }

  Future<void> setRoutingMode(RoutingMode mode) async {
    if (_routing.mode == mode) return;
    _rlog('setRoutingMode ${mode.wire}');
    _routing = _routing.copyWith(mode: mode);
    notifyListeners();
    if (!kRoutingDryRun) {
      await _withBusy(() async {
        await _core.setRoutingMode(mode);
        _routing = await _core.routing();
      });
    }
    _markRoutingDirty();
  }

  /// Forces the pending routing changes through right now (cancels the
  /// debounce window).
  Future<void> flushRouting() async {
    _routingDebounce?.cancel();
    _routingDebounce = null;
    if (_routingApply != RoutingApplyStatus.pending) return;
    _rlog('flushRouting (manual)');
    await _applyRoutingNow();
  }

  void _markRoutingDirty() {
    _routingApply = RoutingApplyStatus.pending;
    notifyListeners();
    _routingDebounce?.cancel();
    _routingDebounce = Timer(_routingApplyDelay, () {
      _routingDebounce = null;
      unawaited(_applyRoutingNow());
    });
  }

  // How long the "обновлено" badge stays visible before fading back to idle.
  static const Duration _routingAppliedFadeAfter = Duration(seconds: 6);

  Future<void> _applyRoutingNow() async {
    _routingAppliedFade?.cancel();
    _routingAppliedFade = null;

    _routingApply = RoutingApplyStatus.applying;
    notifyListeners();
    _rlog('apply: restartVpn (isRunning=$isRunning)');
    if (!kRoutingDryRun) {
      await _withBusy(() async {
        if (isRunning) await _core.restartVpnAsync();
      });
    } else {
      // Simulate the restart latency so the UI status flow feels real.
      await Future<void>.delayed(const Duration(milliseconds: 300));
    }
    _routingApply = RoutingApplyStatus.applied;
    notifyListeners();
    _rlog('apply: done');

    // Auto-fade "обновлено" back to idle so it doesn't sit there forever.
    _routingAppliedFade = Timer(_routingAppliedFadeAfter, () {
      _routingAppliedFade = null;
      if (_routingApply != RoutingApplyStatus.applied) return;
      _routingApply = RoutingApplyStatus.idle;
      notifyListeners();
    });
  }

  /// Refreshes uptime history. Cheap-ish (one FFI call + dart-side copies);
  /// call it from the widget that displays uptime, on its own timer.
  Future<void> refreshUptime() async {
    if (kEngineDryRun) return;
    await _safeReload(() async => _uptime = await _core.uptimeHistory());
  }

  Future<String> logTail() async {
    if (kEngineDryRun) return '[engine/dry] no log — engine calls are disabled';
    try {
      return await _core.logTail();
    } on NekkoboxError catch (e) {
      return 'log error: ${e.message}';
    }
  }

  // ----- internals -----

  // Guards against overlapping ticks: if the engine worker is still busy with
  // the previous second's poll (e.g. queued behind a RestartVPN), we skip
  // this tick rather than pile up a backlog of health calls.
  bool _ticking = false;

  Future<void> _tick() async {
    if (kEngineDryRun) {
      _growDryUptime();
      return;
    }
    if (_ticking) return;
    _ticking = true;
    if (++_tickCount % _updatePollTicks == 0) unawaited(_refreshUpdate());
    try {
      await _safeReload(() async {
        _health = await _core.health();
        _uptime = await _core.uptimeHistory();
        _serviceUptime = await _core.serviceUptimeHistory();
        _cleanliness = await _core.serverCleanliness();
      });
    } finally {
      _ticking = false;
    }
  }

  Future<void> _withBusy(FutureOr<void> Function() body) async {
    if (_busy) return;
    _busy = true;
    _lastError = null;
    notifyListeners();
    try {
      await Future.sync(body);
    } on NekkoboxError catch (e) {
      _lastError = e.message;
    } catch (e) {
      _lastError = e.toString();
    } finally {
      _busy = false;
      notifyListeners();
    }
  }

  Future<void> _safeReload(FutureOr<void> Function() body) async {
    try {
      await Future.sync(body);
      _lastError = null;
    } on NekkoboxError catch (e) {
      _lastError = e.message;
    } catch (e) {
      _lastError = e.toString();
    }
    notifyListeners();
  }
}

/// What the routing-apply badge in the UI should show.
///
/// - [idle] — nothing pending.
/// - [pending] — list was just edited; debounce timer is running.
/// - [applying] — engine restart in flight.
/// - [applied] — last change has been picked up by the engine.
enum RoutingApplyStatus { idle, pending, applying, applied }
