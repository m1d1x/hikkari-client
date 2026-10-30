import 'dart:async';
import 'dart:convert';
import 'dart:math' as math;
import 'dart:io';

import 'package:flutter/foundation.dart';

import '../core/envelope.dart';
import '../core/models.dart';
import '../core/routing_presets.dart';
import '../core/routing_document.dart';
import '../core/nekkobox_core.dart';

/// Debug routing uses the real Go domain/queue with an in-memory store and
/// preview executor. It never configures or restarts a native engine.
const bool kRoutingDryRun = kDebugMode;

/// Dev-only kill switch for everything else that would poke the real engine:
/// VPN on/off, profile switch/rename/delete, auto-mode, and profile import.
/// While true these mutate Dart state only (with simulated latency so the UI
/// still feels real) and never call into [NekkoboxCore] — no real VPN
/// connect/disconnect flapping while iterating on the frontend. Flip back to
/// false to wire to backend.
const bool kEngineDryRun = kDebugMode;

/// What the update banner shows in dry-run, to work on its design without a
/// staged release: `flutter run --dart-define=DRY_UPDATE=mandatory` (or
/// `none`). Default: an ordinary update is ready.
const String kDryUpdate = String.fromEnvironment(
  'DRY_UPDATE',
  defaultValue: 'ready',
);

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
///   - profiles: every 1s from the local snapshot; subscription HTTP runs in Go
///   - uptime history: every 2s when its widget is on screen (later — wired
///     by the widget itself via [refreshUptime]).
class AppState extends ChangeNotifier {
  final NekkoboxCore _core;
  NekkoboxCore get core => _core;
  Timer? _healthTimer;
  Timer? _presetsTimer;
  RoutingPresets? _presets;
  RoutingPresets? get presets => _presets;
  bool _presetsLoading = false;
  bool _presetsBusy = false;
  bool get presetsBusy => _presetsBusy;
  bool _disposed = false;
  String? _presetError;
  String? get presetError => _presetError;

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
  bool _countryRefreshPending = false;
  RoutingState _routing = const RoutingState.initial();
  Map<String, List<PingSample>> _uptime = const {};
  Map<String, List<PingSample>> _serviceUptime = const {};
  ServerCleanliness _cleanliness = ServerCleanliness.off;
  bool _busy = false;
  // True between a routing list edit and the engine restart that picks it up.
  // The UI uses this for the "применяется… / применено" badge near the mode row.
  RoutingApplyStatus _routingApply = RoutingApplyStatus.idle;
  int _routingSequence = -1;
  int _routingEpoch = 0;
  int _routingAppliedAt = 0;
  String? _routingApplyError;
  String? get routingApplyError => _routingApplyError;
  bool _autoMode = false;
  String? _lastError;
  // Timestamp of the last VPN start / profile switch. The tunnel needs a
  // moment to actually pass traffic (handshake, routing table settle), so
  // the very first probe(s) right after this genuinely fail — that's not
  // the same as the profile being dead. Widgets use [isWarmingUp] to show
  // "checking" instead of "down" during this window.
  DateTime? _connectedSince;

  final String? _baseDir;
  final bool _routingPreview;
  String _engineId;
  bool _switchingEngine = false;
  String get engineId => _engineId;
  bool get switchingEngine => _switchingEngine;

  AppState(
    this._core, {
    this._baseDir,
    this._routingPreview = false,
    this._engineId = 'sing-box',
  });

  Future<void> switchEngine(String id) async {
    if (_switchingEngine || id == _engineId || _baseDir == null) return;
    final startedAt = DateTime.now();
    _switchingEngine = true;
    _routingEpoch++;
    _lastError = null;
    notifyListeners();
    try {
      await _core.stopVpnAsync();
      _healthTimer?.cancel();
      _presetsTimer?.cancel();
      _healthTimer = null;
      _presetsTimer = null;
      _core.shutdown();
      _core.init(_baseDir, engineId: id, routingPreview: _routingPreview);
      _engineId = id;
      String? validationError;
      try {
        await _core.validateConfiguration();
      } catch (e) {
        validationError = 'Движок $id не поддерживает текущую политику: $e';
      }
      await File(
        '$_baseDir/engine-selection.json',
      ).writeAsString(jsonEncode({'engineId': id}));
      await start();
      if (validationError != null) _lastError = validationError;
    } catch (e) {
      _lastError = 'Не удалось переключить движок: $e';
    } finally {
      const minimumIndicatorTime = Duration(milliseconds: 650);
      final elapsed = DateTime.now().difference(startedAt);
      if (elapsed < minimumIndicatorTime) {
        await Future<void>.delayed(minimumIndicatorTime - elapsed);
      }
      _switchingEngine = false;
      if (!_disposed) notifyListeners();
    }
  }

  // ----- getters -----
  HealthInfo? get health => _health;
  UpdateStatus get updateStatus => _update;
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
  RoutingApplyStatus get routingApply {
    if (_routingApply == RoutingApplyStatus.applied &&
        DateTime.now().millisecondsSinceEpoch - _routingAppliedAt > 6000) {
      return RoutingApplyStatus.idle;
    }
    return _routingApply;
  }

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

  /// Stable profile ID; native engine tags belong to the backend adapter.
  String? get activeProfileId => activeProfileName;

  // ----- service ping (on-demand, sheet-driven) -----

  /// Probes each service through the active profile's engine
  /// adapter and records the outcome in the engine's service uptime history.
  /// Thin pass-through to [NekkoboxCore.pingServices] — widgets don't hold a
  /// core handle. Returns an empty list (never throws) when the engine can't
  /// be reached, so the sheet degrades gracefully.
  Future<List<ServicePingResultDto>> pingServices(
    String profileId,
    List<ServicePingTargetDto> services,
  ) async {
    try {
      return await _core.pingServices(profileId, services);
    } on NekkoboxError catch (e) {
      _elog('pingServices failed: ${e.message}');
      return const [];
    } catch (e) {
      _elog('pingServices failed: $e');
      return const [];
    }
  }

  /// Snapshot of the whole service-ping history, keyed by
  /// `"<profileId>|<service name>"`. Pass-through to the core; returns an
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
      _routingEpoch++;
      _routingSequence = -1;
      await refreshRoutingState();
      if (kEngineDryRun) {
        _elog('start: dry-run, seeding fake profiles/health');
        _profiles = _dryProfiles;
        _health = _dryHealth;
        _cleanliness = _dryCleanliness;
        _update = _dryUpdate;
        _uptime = {'dry-demo': _seedDryUptime()};
        _autoMode = false;
      } else {
        _profiles = await _core.profiles();
        _health = await _core.health();
        _uptime = await _core.uptimeHistory();
        _autoMode = await _core.autoMode();
      }
    });
    _healthTimer ??= Timer.periodic(const Duration(seconds: 1), (_) => _tick());
    _refreshProfileCountries();
    await refreshPresetState();
    if (_disposed) return;
    _presetsTimer ??= Timer.periodic(
      const Duration(seconds: 1),
      (_) => refreshPresetState(),
    );
    if (_disposed) return;
    if (!kEngineDryRun) {
      // Every launch is a reason to look: the service throttles repeats.
      unawaited(
        _core.checkUpdate().catchError((Object e) {
          _elog('checkUpdate failed: $e');
        }),
      );
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

  /// «Перезапустить»: the service swaps the new version in and the app
  /// exits (to be reopened as the new version). Returns false if the app
  /// should stay: the hand-over could not start ([lastError] is set), or —
  /// in dry-run — the restart was only played out: the banner goes away and
  /// comes back a few seconds later, so it can be looked at again.
  Future<bool> restartToUpdate() async {
    if (kEngineDryRun) {
      _elog('restartToUpdate: dry-run, simulating the swap');
      await Future<void>.delayed(const Duration(milliseconds: 1500));
      _update = UpdateStatus(state: 'idle', installed: _dryUpdate.version);
      notifyListeners();
      Timer(const Duration(seconds: 4), () {
        _update = _dryUpdate;
        notifyListeners();
      });
      return false;
    }
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

  /// Refresh silently after focus/resume; no sync errors enter lastError.
  Future<void> onAppResumed() async {
    if (_disposed || kEngineDryRun) return;
    try {
      await _core.subscriptionWake();
    } catch (e) {
      _elog('subscription wake failed: $e');
    }
    if (!_disposed) await _tick();
  }

  Future<void> _reloadSyncedProfiles() async {
    if (_busy || _disposed || _switchingEngine) return;
    try {
      final profiles = await _core.profiles();
      final autoMode = await _core.autoMode();
      if (_disposed || _busy || _switchingEngine) return;
      final endpoints = {for (final p in _profiles) p.name: p.endpoint};
      final topologyChanged =
          profiles.length != _profiles.length ||
          profiles.any((p) => endpoints[p.name] != p.endpoint);
      _profiles = profiles;
      _autoMode = autoMode;
      if (topologyChanged) {
        _profileCountries = {
          for (final p in profiles)
            if (endpoints[p.name] == p.endpoint &&
                _profileCountries.containsKey(p.name))
              p.name: _profileCountries[p.name]!,
        };
        _refreshProfileCountries();
      }
    } catch (e) {
      _elog('background profiles reload failed: $e');
    }
  }

  Future<void> refreshPresetState() async {
    if (_presetsLoading || _disposed) return;
    _presetsLoading = true;
    try {
      final value = await _core.routingPresets();
      if (!_disposed) {
        if (_presets == null) _presetError = null;
        _presets = value;
        notifyListeners();
      }
    } catch (e) {
      if (!_disposed) {
        _presetError = e.toString();
        notifyListeners();
      }
    } finally {
      _presetsLoading = false;
    }
  }

  Future<void> _changePreset(Future<void> Function() change) async {
    if (_presetsBusy || _disposed) return;
    _presetsBusy = true;
    _presetError = null;
    notifyListeners();
    try {
      await change();
      await refreshPresetState();
    } catch (e) {
      _presetError = e.toString();
    } finally {
      await refreshRoutingState();
      _presetsBusy = false;
      if (!_disposed) notifyListeners();
    }
  }

  Future<void> setPresetSync(bool on) =>
      _changePreset(() => _core.setPresetSync(on));
  Future<void> setServicePreset(String id, bool on) {
    final mode = _routing.mode;
    return _changePreset(() => _core.setServicePreset(mode, id, on));
  }

  Future<void> refreshPresets() =>
      _changePreset(() => _core.refreshRoutingPresets());

  /// Resolves each profile's country (for the flag badge) in the background.
  /// Fire-and-forget on purpose: [NekkoboxCore.profileCountries] can take a
  /// few seconds on a cold cache (DNS + online fallback), but it runs on the
  /// engine worker isolate, so awaiting it here never blocks the UI. While
  /// it's in flight [_resolvingCountries] is true, so freshly added profiles
  /// show a spinner where the flag will land. Skipped entirely in dry-run.
  /// Degrades to leaving the previous map in place on failure.
  Future<void> _refreshProfileCountries() async {
    if (kEngineDryRun || _disposed) return;
    if (_resolvingCountries) {
      _countryRefreshPending = true;
      return;
    }
    final endpoints = {for (final p in _profiles) p.name: p.endpoint};
    _resolvingCountries = true;
    notifyListeners();
    try {
      final countries = await _core.profileCountries();
      if (_disposed) return;
      _profileCountries = {
        for (final p in _profiles)
          if (endpoints[p.name] == p.endpoint && countries.containsKey(p.name))
            p.name: countries[p.name]!,
      };
    } on NekkoboxError catch (e) {
      _elog('profileCountries failed: ${e.message}');
    } catch (e) {
      _elog('profileCountries failed: $e');
    } finally {
      _resolvingCountries = false;
      if (!_disposed) notifyListeners();
      if (_countryRefreshPending && !_disposed) {
        _countryRefreshPending = false;
        unawaited(_refreshProfileCountries());
      }
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
  static UpdateStatus get _dryUpdate => switch (kDryUpdate) {
    'none' => const UpdateStatus(state: 'idle', installed: '1.0.3'),
    'mandatory' => const UpdateStatus(
      state: 'ready',
      installed: '1.0.3',
      version: '1.1.0',
      mandatory: true,
    ),
    _ => const UpdateStatus(
      state: 'ready',
      installed: '1.0.3',
      version: '1.0.4',
    ),
  };

  static const _dryCleanliness = ServerCleanliness(
    running: true,
    checked: true,
    ip: '203.0.113.10',
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
        fix:
            'Перейти на residential или mobile выход — это устраняет главную причину фрода.',
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
    _disposed = true;
    _presetsTimer?.cancel();
    _routingEpoch++;
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
      // The engine can exit asynchronously a few hundred ms after Start (e.g.
      // TUN access denied). Give it a moment, then sample health — if the
      // core died, pull the last FATAL/[engine.stderr] line from the log so
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
  /// for a fatal/error severity marker first, then any [engine.stderr] pipe
  /// line; falls back to the last "failed" line. Returns null if nothing
  /// useful is in the tail (so the caller doesn't overwrite a null error
  /// with garbage).
  String? _extractLogError(String tail) {
    final lines = tail.split('\n');
    String? lastErr;
    for (final raw in lines) {
      // Strip ANSI color codes embedded by a native engine (e.g. \x1b[31mFATAL\x1b[0m).
      final line = raw.replaceAll(RegExp(r'\x1B\[[0-9;]*[A-Za-z]'), '');
      if (line.contains('FATAL') ||
          line.contains(' ERROR ') ||
          (line.contains('[engine.stderr]') &&
              !line.contains(' INFO ') &&
              !line.contains(' DEBUG ')) ||
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
      try {
        await _core.deleteProfile(name);
      } finally {
        _profiles = await _core.profiles();
        _autoMode = await _core.autoMode();
        _health = await _core.health();
      }
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
            label: 'импортированный профиль ${_dryProfiles.length + 1}',
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
          name: name,
          label: name,
          active: false,
          endpoint: '',
          protocol: 'vless',
        ),
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

  Future<void> refreshRoutingState() async {
    final epoch = _routingEpoch;
    try {
      final snapshot = await _core.routingStatus();
      if (_disposed ||
          epoch != _routingEpoch ||
          snapshot.sequence < _routingSequence) {
        return;
      }
      _routingSequence = snapshot.sequence;
      _routing = snapshot.manual;
      _routingApply = switch (snapshot.status) {
        RoutingApplicationPhase.idle => RoutingApplyStatus.idle,
        RoutingApplicationPhase.pending => RoutingApplyStatus.pending,
        RoutingApplicationPhase.applying => RoutingApplyStatus.applying,
        RoutingApplicationPhase.applied => RoutingApplyStatus.applied,
        RoutingApplicationPhase.failed => RoutingApplyStatus.failed,
        RoutingApplicationPhase.preview => RoutingApplyStatus.preview,
      };
      _routingAppliedAt = snapshot.appliedAt;
      _routingApplyError = snapshot.error.isEmpty ? null : snapshot.error;
      if (_routingApplyError != null) _lastError = _routingApplyError;
      notifyListeners();
    } catch (error) {
      if (!_disposed && epoch == _routingEpoch) {
        _lastError = 'Не удалось прочитать маршрутизацию: $error';
        notifyListeners();
      }
    }
  }

  // Every command is delivered, including concurrent edits. Routing commands
  // do not share _withBusy, which intentionally skips other busy UI actions.
  Future<void> _routingCommand(Future<void> Function() command) async {
    final epoch = _routingEpoch;
    if (_switchingEngine) {
      _lastError = 'Дождитесь переключения движка';
      notifyListeners();
      return;
    }
    try {
      await command();
      if (!_disposed && epoch == _routingEpoch) _lastError = null;
    } catch (error) {
      if (!_disposed && epoch == _routingEpoch) _lastError = error.toString();
    } finally {
      if (!_disposed && epoch == _routingEpoch) await refreshRoutingState();
    }
  }

  Future<void> addRule(String category, String value) {
    final mode = _routing.mode;
    return _routingCommand(() => _core.addRule(mode, category, value));
  }

  Future<void> removeRule(String category, String value) {
    final mode = _routing.mode;
    return _routingCommand(() => _core.removeRule(mode, category, value));
  }

  Future<void> setRoutingEnabled(bool on) =>
      _routingCommand(() => _core.setRoutingEnabled(on));

  Future<void> setRoutingMode(RoutingMode mode) =>
      _routingCommand(() => _core.setRoutingMode(mode));

  Future<void> flushRouting() => _routingCommand(_core.flushRouting);

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
    if (_ticking || _disposed || _switchingEngine) return;
    _ticking = true;
    if (++_tickCount % _updatePollTicks == 0) unawaited(_refreshUpdate());
    try {
      await refreshRoutingState();
      if (_disposed) return;
      if (kEngineDryRun) {
        _growDryUptime();
        return;
      }
      await _reloadSyncedProfiles();
      if (_disposed) return;
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
enum RoutingApplyStatus { idle, pending, applying, applied, failed, preview }
