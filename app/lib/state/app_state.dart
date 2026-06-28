import 'dart:async';

import 'package:flutter/foundation.dart';

import '../core/envelope.dart';
import '../core/models.dart';
import '../core/nekkobox_core.dart';

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
  Timer? _healthTimer;

  HealthInfo? _health;
  List<ProfileInfo> _profiles = const [];
  Whitelist _whitelist =
      const Whitelist(domains: [], ips: [], processes: []);
  Map<String, List<PingSample>> _uptime = const {};
  bool _busy = false;
  bool _whitelistDirty = false;
  String? _lastError;

  AppState(this._core);

  // ----- getters -----
  HealthInfo? get health => _health;
  List<ProfileInfo> get profiles => _profiles;
  Whitelist get whitelist => _whitelist;
  Map<String, List<PingSample>> get uptime => _uptime;
  bool get busy => _busy;
  bool get whitelistDirty => _whitelistDirty;
  String? get lastError => _lastError;
  bool get isRunning => _health?.running ?? false;
  String? get activeProfileName {
    for (final p in _profiles) {
      if (p.active) return p.name;
    }
    return null;
  }

  /// Initial load + start the periodic poll. Call once after the FFI handle
  /// is initialised. Health and uptime tick together at 1Hz so a single
  /// notifyListeners() updates everything that depends on either.
  void start() {
    _safeReload(() {
      _profiles = _core.profiles();
      _whitelist = _core.whitelist();
      _health = _core.health();
      _uptime = _core.uptimeHistory();
    });
    _healthTimer ??=
        Timer.periodic(const Duration(seconds: 1), (_) => _tick());
  }

  @override
  void dispose() {
    _healthTimer?.cancel();
    _healthTimer = null;
    super.dispose();
  }

  // ----- mutations -----

  Future<void> toggleVpn() async {
    await _withBusy(() {
      if (isRunning) {
        _core.stopVpn();
      } else {
        _core.startVpn();
      }
      _health = _core.health();
    });
  }

  Future<void> switchProfile(String name) async {
    await _withBusy(() {
      try {
        _core.switchProfile(name);
      } finally {
        _profiles = _core.profiles();
        _health = _core.health();
      }
    });
  }

  Future<void> renameProfile(String name, String label) async {
    await _withBusy(() {
      _core.renameProfile(name, label);
      _profiles = _core.profiles();
    });
  }

  Future<void> deleteProfile(String name) async {
    await _withBusy(() {
      _core.deleteProfile(name);
      _profiles = _core.profiles();
    });
  }

  Future<void> addRule(String category, String value) async {
    await _withBusy(() {
      _core.addRule(category, value);
      _whitelist = _core.whitelist();
      _whitelistDirty = true;
    });
  }

  Future<void> removeRule(String category, String value) async {
    await _withBusy(() {
      _core.removeRule(category, value);
      _whitelist = _core.whitelist();
      _whitelistDirty = true;
    });
  }

  /// Apply pending whitelist changes by restarting the core. The dirty flag
  /// clears regardless of whether the core was running — if it wasn't, the
  /// new rules take effect on the next StartVPN anyway.
  Future<void> applyRules() async {
    await _withBusy(() async {
      if (isRunning) _core.restartVpn();
      _whitelistDirty = false;
    });
  }

  /// Refreshes uptime history. Cheap-ish (one FFI call + dart-side copies);
  /// call it from the widget that displays uptime, on its own timer.
  void refreshUptime() {
    _safeReload(() => _uptime = _core.uptimeHistory());
  }

  String logTail() {
    try {
      return _core.logTail();
    } on NekkoboxError catch (e) {
      return 'log error: ${e.message}';
    }
  }

  // ----- internals -----

  void _tick() {
    _safeReload(() {
      _health = _core.health();
      _uptime = _core.uptimeHistory();
    });
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

  void _safeReload(void Function() body) {
    try {
      body();
      _lastError = null;
    } on NekkoboxError catch (e) {
      _lastError = e.message;
    } catch (e) {
      _lastError = e.toString();
    }
    notifyListeners();
  }
}
