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
  bool _autoMode = false;
  String? _lastError;

  AppState(this._core);

  // ----- getters -----
  HealthInfo? get health => _health;
  List<ProfileInfo> get profiles => _profiles;
  Whitelist get whitelist => _whitelist;
  Map<String, List<PingSample>> get uptime => _uptime;
  bool get busy => _busy;
  bool get whitelistDirty => _whitelistDirty;
  bool get autoMode => _autoMode;
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
      _autoMode = _core.autoMode();
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
    await _withBusy(() async {
      if (isRunning) {
        _core.stopVpn();
        _health = _core.health();
        return;
      }
      _core.startVpn();
      // sing-box can exit asynchronously a few hundred ms after Start (e.g.
      // TUN access denied). Give it a moment, then sample health — if the
      // core died, pull the last FATAL/[singbox.err] line from the log so
      // the UI shows something more actionable than just "выкл".
      await Future<void>.delayed(const Duration(milliseconds: 700));
      _health = _core.health();
      if (!isRunning) {
        final msg = _extractLogError(_core.logTail());
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
          line.contains('[singbox.err]') ||
          line.contains('failed to start') ||
          line.contains('Access is denied')) {
        lastErr = line.trim();
      }
    }
    return lastErr;
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

  /// Toggles auto-fastest mode. The engine reload (when running) means
  /// there's a 1-2s pause; AppState's _withBusy lock hides the spinner.
  Future<void> setAutoMode(bool on) async {
    await _withBusy(() {
      _core.setAutoMode(on);
      _autoMode = _core.autoMode();
      _health = _core.health();
    });
  }

  /// Imports a profile from a server URI. Returns the generated profile
  /// name on success, or null if the import failed (error is stored in
  /// [lastError]).
  Future<String?> addProfileFromUri(String uri) async {
    String? name;
    await _withBusy(() {
      name = _core.addProfileFromUri(uri);
      _profiles = _core.profiles();
    });
    return name;
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
