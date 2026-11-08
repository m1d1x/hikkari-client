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

/// Xray is not offered on Windows yet: the installer does not ship it and
/// its TUN there has no answer for DNS and traffic leaking past the tunnel
/// (plans/2026-10-31-xray-windows.md).
final bool xrayAvailable = !Platform.isWindows;

/// Debug routing uses the real Go domain/queue with an in-memory store and
/// preview executor. It never configures or restarts a native engine.
const bool kRoutingDryRun = kDebugMode && !kLiveEngine;

/// Dev-only kill switch for everything else that would poke the real engine:
/// VPN on/off, profile switch/rename/delete, auto-mode, and profile import.
/// While true these mutate Dart state only (with simulated latency so the UI
/// still feels real) and never call into [NekkoboxCore] — no real VPN
/// connect/disconnect flapping while iterating on the frontend. Flip back to
/// false to wire to backend.
// Live debug runs retain hot reload while using real profiles and the engine.
// flutter run -d macos --dart-define=HIKKARI_LIVE=true
const bool kLiveEngine = bool.fromEnvironment('HIKKARI_LIVE');
const bool kEngineDryRun = kDebugMode && !kLiveEngine;

/// What the update banner shows in dry-run, to work on its design without a
/// staged release: `flutter run --dart-define=DRY_UPDATE=mandatory` (staged;
/// `mandatory-downloading` mid-download; `none`). Default: an ordinary update
/// is ready.
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
  // Which way the power button is taking the tunnel right now, so the screen
  // can say "подключение…" / "отключение…" instead of a silent wait.
  VpnTransition _vpnTransition = VpnTransition.none;
  // True between a routing list edit and the engine restart that picks it up.
  // The UI uses this for the "применяется… / применено" badge near the mode row.
  RoutingApplyStatus _routingApply = RoutingApplyStatus.idle;
  int _routingSequence = -1;
  int _routingEpoch = 0;
  int _routingAppliedAt = 0;
  String? _routingApplyError;
  String? get routingApplyError => _routingApplyError;
  bool _autoMode = false;
  String _carryingProfile = '';
  String? _lastErrorValue;

  String? get _lastError => _lastErrorValue;

  /// Every new error the user is shown also goes into the engine log, so a
  /// support report or the tester stream carries the words the user saw.
  set _lastError(String? value) {
    if (value != null && value != _lastErrorValue) {
      unawaited(_core.logUi('error shown: $value').catchError((_) {}));
    }
    _lastErrorValue = value;
  }

  // Timestamp of the last VPN start / profile switch. The tunnel needs a
  // moment to actually pass traffic (handshake, routing table settle), so
  // the very first probe(s) right after this genuinely fail — that's not
  // the same as the profile being dead. Widgets use [isWarmingUp] to show
  // "checking" instead of "down" during this window.
  DateTime? _connectedSince;

  final String? _baseDir;
  final bool _routingPreview;
  final bool _dryRun;
  String _engineId;
  bool _switchingEngine = false;
  String get engineId => _engineId;
  bool get switchingEngine => _switchingEngine;

  AppState(
    this._core, {
    this._baseDir,
    this._routingPreview = false,
    this._engineId = 'sing-box',
    bool? dryRun,
  }) : _dryRun = dryRun ?? kEngineDryRun;

  Future<void> switchEngine(String id) async {
    if (_switchingEngine || id == _engineId || _baseDir == null) return;
    if (id == 'xray' && !xrayAvailable) return;
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

  /// Sets the update state directly — widget tests of the update card.
  @visibleForTesting
  void debugSetUpdate(UpdateStatus next) {
    _update = next;
    notifyListeners();
  }

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

  /// The power button's press in flight: [VpnTransition.connecting] from the
  /// press until the core is up or the start failed, [VpnTransition.disconnecting]
  /// until it is down. Other busy work (profile switch, routing) leaves it
  /// [VpnTransition.none].
  VpnTransition get vpnTransition => _vpnTransition;
  RoutingApplyStatus get routingApply {
    if (_routingApply == RoutingApplyStatus.applied &&
        DateTime.now().millisecondsSinceEpoch - _routingAppliedAt > 6000) {
      return RoutingApplyStatus.idle;
    }
    return _routingApply;
  }

  bool get autoMode => _autoMode;

  /// The profile carrying traffic in auto mode, or null when the engine has
  /// not picked one. Differs from [activeProfileName], which is the user's own
  /// selection and is not what auto mode routes through.
  String? get autoProfileName =>
      _autoMode && _carryingProfile.isNotEmpty ? _carryingProfile : null;

  /// Reports the server list going on or off screen, so the engine measures
  /// the other servers only while someone can see the numbers.
  void setServerListOpen(bool open) {
    if (_dryRun) return;
    unawaited(_core.setServerListOpen(open));
  }

  String? get lastError => _lastError;
  Future<TrafficCounters?> trafficCounters() async =>
      _dryRun ? null : _core.trafficCounters();

  bool get isRunning => _health?.running ?? false;

  /// Explicitly clears the last operation error.
  void clearError() {
    if (_lastError == null) return;
    _lastError = null;
    notifyListeners();
  }

  /// Simulates a fatal engine crash on VPN start for diagnostic UI testing.
  void simulateEngineFailure([String? message]) {
    _lastError =
        message ??
        'FATAL[0000] start engine: configure tun interface: operation not permitted\n'
            '2026-11-01T02:50:12Z [singbox/inbound] tun: open utun4: permission denied\n'
            '[engine.stderr] sing-box core died unexpectedly with exit code 1';
    _health = _dryHealthOff;
    notifyListeners();
  }

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
      if (_dryRun) {
        _elog('start: dry-run, seeding fake profiles/health');
        _profiles = _dryProfiles;
        _health = _dryHealth;
        _cleanliness = _dryCleanliness;
        _update = _dryUpdate;
        _profileCountries = const {
          'dry-demo': 'nl',
          'dry-frankfurt': 'de',
          'dry-paris': 'fr',
        };
        _uptime = {
          for (final p in _dryProfiles)
            p.name:
                p.capacity?.history
                    .map(
                      (point) => PingSample(
                        ts: point.ts,
                        ok: point.rttMs > 0,
                        rttMs: point.rttMs,
                      ),
                    )
                    .toList() ??
                const [],
        };
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
    if (!_dryRun) _checkForUpdate();
  }

  /// Launch and every return to the window are reasons to look at the
  /// channel; the service ignores a request within 5 minutes of its last
  /// check, so this costs nothing when repeated.
  void _checkForUpdate() {
    unawaited(
      _core.checkUpdate().catchError((Object e) {
        _elog('checkUpdate failed: $e');
      }),
    );
    unawaited(_refreshUpdate());
  }

  Future<void> _refreshUpdate() async {
    try {
      final next = await _core.updateStatus();
      if (next.replaced) await _relaunch(next);
      if (next.state != _update.state ||
          next.version != _update.version ||
          next.mandatory != _update.mandatory ||
          next.bytesDone != _update.bytesDone) {
        _update = next;
        notifyListeners();
      }
    } catch (e) {
      _elog('updateStatus failed: $e');
    }
  }

  bool _relaunchArranged = false;

  /// The installation now holds another release than this process: its
  /// core may not read the config this release writes (tester-004, 06.11:
  /// a rollback left 1.0.51 running beside 1.0.40's core, and every start
  /// failed). The service refuses such starts; the app reopens itself as
  /// the installed release, which connects again if the core is wanted.
  bool get mustExit => _relaunchArranged;

  Future<void> _relaunch(UpdateStatus st) async {
    if (_relaunchArranged || _dryRun) return;
    _elog('app replaced by installed ${st.installed}: relaunching');
    try {
      await _core.relaunchApp();
      _relaunchArranged = true;
      notifyListeners();
    } catch (e) {
      _elog('relaunchApp failed: $e');
    }
  }

  /// «Перезапустить»: the service swaps the new version in and the app
  /// exits (to be reopened as the new version). Returns false if the app
  /// should stay: the hand-over could not start ([lastError] is set), or —
  /// in dry-run — the restart was only played out: the banner goes away and
  /// comes back a few seconds later, so it can be looked at again.
  Future<bool> restartToUpdate() async {
    if (_dryRun) {
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
    if (_disposed || _dryRun) return;
    _checkForUpdate();
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
  Future<void> setPresetTester(bool on) =>
      _changePreset(() => _core.setPresetTester(on));
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
    if (_dryRun || _disposed) return;
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
    'mandatory-downloading' => const UpdateStatus(
      state: 'downloading',
      installed: '1.0.3',
      version: '1.1.0',
      mandatory: true,
      bytesDone: 5767168,
      bytesTotal: 14680064,
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
            'Хостинг/датацентр IP: Cloudflare Turnstile почти всегда челленджит такие',
        fix:
            'Перейти на residential или mobile выход: это устраняет главную причину фрода.',
      ),
      CleanlinessReason(
        severity: 'high',
        title: 'IP/подсеть в abuse-фидах (репутация: High)',
        fix: 'Сменить IP/подсеть: с этого сервера уже спамили/абузили.',
      ),
      CleanlinessReason(
        severity: 'med',
        title:
            'ASN зарегистрирован в RU (гео IP: DE): CF повышает подозрительность',
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

  // Preview fixtures use the current production widgets and their exact metric
  // model. Every profile is visibly labelled demo; no network measurements run.
  static RelayCapacity _demoMetrics(int rtt, double mbps, double phase) {
    final now = DateTime.now().millisecondsSinceEpoch ~/ 1000;
    final bucket = now ~/ 1800 * 1800;
    return RelayCapacity(
      mbps: mbps,
      limited: false,
      rttMs: rtt,
      points: 3,
      measuredAt: now,
      expiresAt: now + 5400,
      history: List.generate(
        48,
        (i) => RelayMetricPoint(
          ts: bucket - (47 - i) * 1800,
          rttMs: i == 47
              ? rtt
              : (rtt + 12 * math.sin(i / 4 + phase) + 4 * math.sin(i * 1.7))
                    .round(),
          mbps: i == 47
              ? mbps
              : mbps *
                    (0.85 +
                        0.1 * math.sin(i / 5 + phase) +
                        0.04 * math.sin(i * 1.1)),
        ),
      ),
    );
  }

  List<ProfileInfo> _dryProfiles = [
    ProfileInfo(
      name: 'dry-demo',
      label: 'амстердам · демо',
      active: true,
      endpoint: 'demo.example.com:443',
      protocol: 'vless',
      autoEligible: true,
      capacity: _demoMetrics(87, 128, 0),
    ),
    ProfileInfo(
      name: 'dry-frankfurt',
      label: 'франкфурт · демо',
      active: false,
      endpoint: 'demo.example.com:443',
      protocol: 'vless',
      autoEligible: true,
      capacity: _demoMetrics(112, 74, 1.8),
    ),
    const ProfileInfo(
      name: 'dry-paris',
      label: 'париж · демо',
      active: false,
      endpoint: 'demo.example.com:443',
      protocol: 'vless',
      autoEligible: true,
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

  /// Starts or stops the tunnel. Returns the other VPN clients a start
  /// turned off first, for the UI to say so; empty otherwise.
  Future<List<String>> toggleVpn() async {
    if (_busy) return const <String>[];
    _vpnTransition = isRunning
        ? VpnTransition.disconnecting
        : VpnTransition.connecting;
    try {
      return await _toggleVpn();
    } finally {
      _vpnTransition = VpnTransition.none;
      if (!_disposed) notifyListeners();
    }
  }

  Future<List<String>> _toggleVpn() async {
    var foreignStopped = const <String>[];
    if (_dryRun) {
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
      return foreignStopped;
    }
    await _withBusy(() async {
      if (isRunning) {
        await _core.stopVpnAsync();
        _health = await _core.health();
        return;
      }
      final startedAt = DateTime.now();
      foreignStopped = await _core.startVpnAsync();
      _connectedSince = DateTime.now();
      // The engine can exit asynchronously after Start (e.g. TUN access
      // denied). It can also go down for a moment and come back on its own —
      // the service restarting into updated binaries resumes it within
      // seconds — so one early sample is not a verdict: watch for a few
      // seconds and report only a core that stays down, with the error it
      // logged after this start.
      for (
        var waited = Duration.zero;
        waited < _startSettle;
        waited += _startPoll
      ) {
        await Future<void>.delayed(_startPoll);
        _health = await _core.health();
        if (isRunning) break;
      }
      if (!isRunning) {
        throw NekkoboxError(
          _extractLogError(await _core.logTail(), since: startedAt) ??
              'ядро остановилось сразу после запуска',
        );
      }
    });
    return foreignStopped;
  }

  static const _startPoll = Duration(milliseconds: 500);
  static const _startSettle = Duration(seconds: 5);

  /// Extracts the most recent fatal-ish line from the engine log. We look
  /// for a fatal/error severity marker first, then any [engine.stderr] pipe
  /// line; falls back to the last "failed" line. Returns null if nothing
  /// useful is in the tail (so the caller doesn't overwrite a null error
  /// with garbage).
  String? _extractLogError(String tail, {DateTime? since}) {
    final lines = tail.split('\n');
    String? lastErr;
    for (final raw in lines) {
      // Lines from before this start describe an earlier session.
      if (since != null) {
        final at = _logLineTime(raw);
        if (at != null && at.isBefore(since)) continue;
      }
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

  /// The engine log's own stamp, "2006/01/02 15:04:05.000000", in local
  /// time; null for a line without one.
  static DateTime? _logLineTime(String line) {
    final m = RegExp(
      r'^(\d{4})/(\d{2})/(\d{2}) (\d{2}):(\d{2}):(\d{2})',
    ).firstMatch(line);
    if (m == null) return null;
    final p = [for (var i = 1; i <= 6; i++) int.parse(m.group(i)!)];
    return DateTime(p[0], p[1], p[2], p[3], p[4], p[5]);
  }

  Future<void> switchProfile(String name) async {
    if (_dryRun) {
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
                    autoEligible: p.autoEligible,
                    capacity: p.capacity,
                    subscription: p.subscription,
                  )
                : ProfileInfo(
                    name: p.name,
                    label: p.label,
                    active: false,
                    endpoint: p.endpoint,
                    protocol: p.protocol,
                    autoEligible: p.autoEligible,
                    capacity: p.capacity,
                    subscription: p.subscription,
                  ),
        ];
        _profiles = _dryProfiles;
        _autoMode = false;
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
        _autoMode = await _core.autoMode();
        _health = await _core.health();
      }
    });
    _refreshProfileCountries();
  }

  Future<void> renameProfile(String name, String label) async {
    if (_dryRun) {
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
                    autoEligible: p.autoEligible,
                    capacity: p.capacity,
                    subscription: p.subscription,
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
    if (_dryRun) {
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
    if (_dryRun) {
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
      _profiles = await _core.profiles();
      _health = await _core.health();
    });
  }

  /// Imports a profile from a server URI. Returns the generated profile
  /// name on success, or null if the import failed (error is stored in
  /// [lastError]).
  Future<String?> addProfileFromUri(String uri) async {
    if (_dryRun) {
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
    if (_dryRun) {
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

  Future<String> exportRoutingSnapshot() => _core.exportRoutingSnapshot();

  Future<void> importRoutingSnapshot(String data) async {
    await _core.importRoutingSnapshot(data);
    await refreshRoutingState();
    await refreshPresetState();
  }

  Future<void> resetToDefaults() async {
    await _core.resetToDefaults();
    await refreshRoutingState();
    await refreshPresetState();
  }

  /// Refreshes uptime history. Cheap-ish (one FFI call + dart-side copies);
  /// call it from the widget that displays uptime, on its own timer.
  Future<void> refreshUptime() async {
    if (_dryRun) return;
    await _safeReload(() async => _uptime = await _core.uptimeHistory());
  }

  String get supportUrl {
    const fromEnv = String.fromEnvironment('HIKKARI_SUPPORT_URL');
    if (fromEnv.isNotEmpty) return fromEnv;
    final osEnv = Platform.environment['HIKKARI_SUPPORT_URL'];
    if (osEnv != null && osEnv.isNotEmpty) return osEnv;
    if (kDebugMode) {
      return 'http://127.0.0.1:9445/api/v1/support/report';
    }
    return 'https://api.hikkari.net/api/v1/support/report';
  }

  String _simulatedLogs() {
    final now = DateTime.now().toUtc().toIso8601String();
    final buf = StringBuffer();
    buf.writeln(
      '2026-11-01T02:49:00Z [singbox/app] starting service (sing-box v1.11.0, nekkobox/1.0.0)',
    );
    buf.writeln(
      '2026-11-01T02:49:01Z [singbox/dns] initialized system DNS resolver on 127.0.0.1:53',
    );
    buf.writeln(
      '2026-11-01T02:49:01Z [singbox/router] rule-set loaded 4 domains, 12 geoip prefixes',
    );
    buf.writeln(
      '2026-11-01T02:49:02Z [singbox/outbound] vless-direct: handshake ok (rtt: 42ms, alpn: h2)',
    );
    buf.writeln(
      '2026-11-01T02:49:05Z [singbox/inbound] mixed: 127.0.0.1:52341 -> 1.1.1.1:443 (tcp)',
    );
    buf.writeln(
      '2026-11-01T02:49:10Z [singbox/inbound] mixed: 127.0.0.1:52342 -> 8.8.8.8:443 (tcp)',
    );
    buf.writeln(
      '2026-11-01T02:49:15Z [singbox/router] default match: route -> out-direct',
    );
    if (_lastError != null) {
      buf.writeln(
        '2026-11-01T02:50:11Z [singbox/core] WARNING: network interface state change detected',
      );
      buf.writeln(
        '2026-11-01T02:50:12Z [singbox/inbound] tun: open utun4: permission denied',
      );
      buf.writeln(
        '2026-11-01T02:50:12Z FATAL[0000] start engine: configure tun interface: operation not permitted',
      );
      buf.writeln(
        '2026-11-01T02:50:12Z [engine.stderr] sing-box core died unexpectedly with exit code 1',
      );
    } else {
      buf.writeln('$now [singbox/core] tunnel active, uptime normal');
    }
    return buf.toString();
  }

  Future<String> _buildDiagnosticsReport([int maxLines = 500]) async {
    final now = DateTime.now().toUtc().toIso8601String();
    final osName = Platform.operatingSystem;
    final osVersion = Platform.operatingSystemVersion;
    final arch = Platform.version.split(' ').first;
    final locale = Platform.localeName;
    final cpus = Platform.numberOfProcessors;
    final hostname = Platform.localHostname;

    final engine = engineId;
    final running = _health?.running ?? false;
    final status = _health?.status ?? 'off';
    final autoMode = _autoMode ? 'ON' : 'OFF';

    ProfileInfo? activeProf;
    for (final p in _profiles) {
      if (p.active) {
        activeProf = p;
        break;
      }
    }
    final profName = activeProf?.name ?? 'none';
    final profProtocol = activeProf?.protocol ?? 'vless';
    final profCountry = activeProf != null
        ? countryCodeFor(activeProf.name)
        : 'unknown';

    final routingEnabled = _routing?.enabled ?? false;
    final routingModeName = _routing?.mode.name ?? 'bypass';
    final activeList = _routing?.active;
    final procCount = activeList?.processes.length ?? 0;
    final domainsCount = activeList?.domains.length ?? 0;
    final ipsCount = activeList?.ips.length ?? 0;
    final routingSummary = routingEnabled
        ? '$routingModeName (processes: $procCount, domains: $domainsCount, ips: $ipsCount)'
        : 'disabled (direct)';

    final ifaceBuffer = StringBuffer();
    try {
      final ifaces = await NetworkInterface.list();
      for (final iface in ifaces) {
        final addrs = iface.addresses
            .map((a) => '${a.address} (${a.type.name})')
            .join(', ');
        ifaceBuffer.writeln('  * ${iface.name}: $addrs');
      }
    } catch (e) {
      ifaceBuffer.writeln('  * <error listing interfaces: $e>');
    }
    if (ifaceBuffer.isEmpty) {
      ifaceBuffer.writeln('  * <no active network interfaces>');
    }

    String rawLogs = '';
    if (_dryRun) {
      rawLogs = _simulatedLogs();
    } else {
      try {
        rawLogs = await _core.logTail();
      } catch (e) {
        rawLogs = '[error fetching logs: $e]';
      }
    }

    final buf = StringBuffer();
    buf.writeln('=== Hikkari Diagnostics Report ===');
    buf.writeln('Timestamp: $now');
    buf.writeln('Install ID: dev-${Platform.operatingSystem}-client');
    buf.writeln('');
    buf.writeln('--- SYSTEM & DEVICE ---');
    buf.writeln('OS: $osName ($osVersion)');
    buf.writeln('Arch / Dart: $arch ($cpus cores)');
    buf.writeln('Locale: $locale');
    buf.writeln('Host: $hostname');
    buf.writeln('App Version: 1.0.0+1');
    buf.writeln('');
    buf.writeln('--- ENGINE & TUNNEL STATUS ---');
    buf.writeln('Active Engine: $engine');
    buf.writeln('Engine Status: $status (running: $running)');
    buf.writeln('Auto Mode: $autoMode');
    buf.writeln('Connected Since: ${_connectedSince ?? "none"}');
    if (_lastError != null) {
      buf.writeln('Recent Fatal / Error:');
      for (final line in _lastError!.split('\n')) {
        buf.writeln('  ! $line');
      }
    } else {
      buf.writeln('Recent Error: none');
    }
    buf.writeln('');
    buf.writeln('--- ACTIVE PROFILE & ROUTING ---');
    buf.writeln(
      'Active Profile: $profName (protocol: $profProtocol, country: $profCountry)',
    );
    buf.writeln('Routing: $routingSummary');
    buf.writeln('');
    buf.writeln('--- NETWORK INTERFACES ---');
    buf.write(ifaceBuffer.toString());
    buf.writeln('');
    buf.writeln('--- RECENT ENGINE LOGS (SANITIZED) ---');
    buf.writeln(rawLogs.trim());
    buf.writeln('==================================');

    return buf.toString();
  }

  Future<String> logTail() async {
    if (_dryRun) return _simulatedLogs();
    try {
      final logs = await _core.logTail();
      return logs.isEmpty ? '[лог пуст: ядро ожидает подключения]' : logs;
    } on NekkoboxError catch (e) {
      return 'log error: ${e.message}';
    }
  }

  Future<String> collectDiagnostics([int maxLines = 500]) async {
    try {
      return await _buildDiagnosticsReport(maxLines);
    } catch (e) {
      debugPrint('[diagnostics] build report error: $e');
      return '=== Hikkari Diagnostics Report ===\nOS: ${Platform.operatingSystem}\nError: $e\n';
    }
  }

  /// Network doctor calls. Dry run has no engine: it answers with a healthy
  /// machine, marked `simulated` so the window says so.
  Future<Map<String, dynamic>> networkDiagnose() async {
    if (_dryRun) {
      return {
        'simulated': true,
        'supported': true,
        'os': Platform.isMacOS ? 'darwin' : Platform.operatingSystem,
        'proxy': {'verdict': 'none'},
        'host': {'iface': 'auto', 'dns': 'system', 'probed': true},
        'direct': {
          'checked': true,
          'internet': true,
          'secureDnsBlocked': <String>[],
        },
        'hostsOverrides': 0,
        'dpiTools': <String>[],
        'foreignWfp': <String>[],
      };
    }
    return _core.networkDiagnose();
  }

  Future<Map<String, dynamic>> repairNetwork() async {
    if (_dryRun) {
      return {
        'simulated': true,
        'actions': [
          {'name': 'proxy_disable', 'done': false},
          {'name': 'dns_flush', 'done': true},
        ],
      };
    }
    return _core.repairNetwork();
  }

  Future<Map<String, dynamic>> scanNetworkStack() async {
    if (_dryRun) {
      return {
        'simulated': true,
        'hostsOverrides': 0,
        'dpiTools': <String>[],
        'foreignWfp': <String>[],
      };
    }
    return _core.scanNetworkStack();
  }

  Future<Map<String, dynamic>> resetNetworkStack({bool force = false}) async {
    if (_dryRun) {
      return {'simulated': true, 'rebootRequired': true};
    }
    return _core.resetNetworkStack(force: force);
  }

  /// Sends a support report through the engine, which attaches the
  /// sanitized log and the install id. Throws when it was not delivered, so
  /// the caller can say so; the failure also goes into the engine log.
  Future<void> submitSupportReport(String message) async {
    if (_dryRun) {
      await _submitDryRunReport(message);
      return;
    }
    try {
      await _core.submitSupportReport(message);
    } catch (e) {
      unawaited(_core.logUi('support report not sent: $e').catchError((_) {}));
      rethrow;
    }
  }

  /// Dry run has no engine: the report goes straight to [supportUrl] with
  /// the UI-built diagnostics, marked as a dev install.
  Future<void> _submitDryRunReport(String message) async {
    final diag = await collectDiagnostics(500);
    final client = HttpClient();
    client.connectionTimeout = const Duration(seconds: 5);
    try {
      final req = await client.postUrl(Uri.parse(supportUrl));
      req.headers.contentType = ContentType.json;
      req.write(
        jsonEncode({
          'install_id': 'dev-${Platform.operatingSystem}-client',
          'platform': Platform.operatingSystem,
          'version': 'dry-run',
          'message': message,
          'logs': diag,
        }),
      );
      final resp = await req.close();
      final body = await resp.transform(utf8.decoder).join();
      if (resp.statusCode ~/ 100 != 2) {
        throw HttpException('hub answered ${resp.statusCode}: $body');
      }
    } finally {
      client.close();
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
      if (_dryRun) {
        _growDryUptime();
        return;
      }
      await _reloadSyncedProfiles();
      if (_disposed) return;
      final wasRunning = isRunning;
      await _safeReload(() async {
        _health = await _core.health();
        if (!wasRunning && isRunning) {
          _lastError = null;
        }
        _uptime = await _core.uptimeHistory();
        _serviceUptime = await _core.serviceUptimeHistory();
        _cleanliness = await _core.serverCleanliness();
        _carryingProfile = await _core.carryingProfile();
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
/// Direction of the power button's press while it is in flight.
enum VpnTransition { none, connecting, disconnecting }

enum RoutingApplyStatus { idle, pending, applying, applied, failed, preview }
