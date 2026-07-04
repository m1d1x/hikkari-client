import 'dart:convert';

import 'app_discovery.dart';
import 'envelope.dart';
import 'models.dart';

/// Platform-agnostic surface to the nekkobox engine. The desktop binding
/// (nekkobox_ffi.dart) implements [rawCall] over `dart:ffi`; mobile will
/// add a MethodChannel implementation later. Both forward to the same
/// Go `core/api.Core.Call` and the same JSON contract.
///
/// Every engine method except [init]/[shutdown] is asynchronous and runs on
/// the FFI binding's worker isolate — the UI isolate never blocks on a Go
/// call, no matter how slow it is (RestartVPN, a cold country lookup, etc.).
/// [init]/[shutdown] stay synchronous: they're one-shot bootstrap that must
/// run before/after the UI exists, and blocking the splash for them is fine.
abstract class NekkoboxCore {
  /// One-shot raw call on the *current* isolate. Implementations return the
  /// raw envelope JSON string emitted by Go; the typed helpers below parse
  /// it. Reserved for bootstrap — prefer [rawCallAsync] everywhere else.
  String rawCall(String method, [String argsJson = '']);

  /// Async variant — runs the FFI call on a background isolate so the UI
  /// thread stays responsive. Subclasses override this; the default falls
  /// back to the sync version on the current isolate.
  Future<String> rawCallAsync(String method, [String argsJson = '']) async {
    return rawCall(method, argsJson);
  }

  // ----- typed helpers -----

  /// Synchronous unwrap — bootstrap only ([init]/[shutdown]).
  Object? _call(String method, [Map<String, Object?>? args]) {
    final raw = rawCall(method, args == null ? '' : jsonEncode(args));
    final env = Envelope.fromJson(jsonDecode(raw) as Map<String, dynamic>);
    return env.unwrap();
  }

  Future<Object?> _callAsync(String method, [Map<String, Object?>? args]) async {
    final raw = await rawCallAsync(
        method, args == null ? '' : jsonEncode(args));
    final env = Envelope.fromJson(jsonDecode(raw) as Map<String, dynamic>);
    return env.unwrap();
  }

  // ----- lifecycle (sync bootstrap) -----

  void init(String baseDir) {
    _call('Init', {'baseDir': baseDir});
  }

  void shutdown() {
    _call('Shutdown');
  }

  // ----- VPN -----

  Future<void> restartVpnAsync() => _callAsync('RestartVPN');
  Future<void> startVpnAsync() => _callAsync('StartVPN');
  Future<void> stopVpnAsync() => _callAsync('StopVPN');
  Future<bool> isRunning() async => await _callAsync('IsRunning') as bool;

  // ----- health -----

  Future<HealthInfo> health() async =>
      HealthInfo.fromJson(await _callAsync('Health') as Map<String, dynamic>);

  Future<String> externalIp() async => await _callAsync('ExternalIP') as String;

  // ----- logs -----

  Future<String> logTail() async => await _callAsync('LogTail') as String;

  // ----- routing -----

  Future<RoutingState> routing() async =>
      RoutingState.fromJson(await _callAsync('Routing') as Map<String, dynamic>);

  Future<void> setRoutingEnabled(bool on) =>
      _callAsync('SetRoutingEnabled', {'on': on});

  Future<void> setRoutingMode(RoutingMode mode) =>
      _callAsync('SetRoutingMode', {'mode': mode.wire});

  Future<void> addRule(RoutingMode mode, String category, String value) =>
      _callAsync(
        'AddRule',
        {'mode': mode.wire, 'category': category, 'value': value},
      );

  Future<void> removeRule(RoutingMode mode, String category, String value) =>
      _callAsync(
        'RemoveRule',
        {'mode': mode.wire, 'category': category, 'value': value},
      );

  // ----- profiles -----

  Future<List<ProfileInfo>> profiles() async => (await _callAsync('Profiles')
          as List)
      .cast<Map<String, dynamic>>()
      .map(ProfileInfo.fromJson)
      .toList();

  Future<String> activeProfile() async =>
      await _callAsync('ActiveProfile') as String;

  Future<void> renameProfile(String name, String label) =>
      _callAsync('RenameProfile', {'name': name, 'label': label});

  Future<void> deleteProfile(String name) =>
      _callAsync('DeleteProfile', {'name': name});

  Future<void> switchProfile(String name) =>
      _callAsync('SwitchProfile', {'name': name});

  /// Imports a profile from a server URI (e.g. `hysteria2://...`). Returns
  /// the generated profile name on success; throws [NekkoboxError] if the
  /// URI scheme is unsupported or malformed.
  Future<String> addProfileFromUri(String uri) async =>
      await _callAsync('AddProfileFromURI', {'uri': uri}) as String;

  /// Lowercase ISO 3166-1 alpha-2 country code per profile name, resolved
  /// from each profile's server host. Missing/unresolved entries map to "".
  /// Cached engine-side, but the first call per host can take up to ~3.5s
  /// (DNS + online fallback) — runs on the worker isolate, so awaiting it
  /// never blocks the UI.
  Future<Map<String, String>> profileCountries() async =>
      (await _callAsync('ProfileCountries') as Map<String, dynamic>)
          .cast<String, String>();

  /// Whether auto-fastest mode (urltest group) is on.
  Future<bool> autoMode() async => await _callAsync('AutoMode') as bool;

  /// Toggles auto-fastest mode. Reloads the engine if it was running.
  Future<void> setAutoMode(bool on) => _callAsync('SetAutoMode', {'on': on});

  // ----- uptime -----

  Future<Map<String, List<PingSample>>> uptimeHistory() async {
    final raw = await _callAsync('UptimeHistory') as Map<String, dynamic>;
    return raw.map((k, v) => MapEntry(
          k,
          (v as List)
              .cast<Map<String, dynamic>>()
              .map(PingSample.fromJson)
              .toList(),
        ));
  }

  // ----- service uptime -----

  /// Probes each service in parallel through the active profile and returns
  /// one result per service (order preserved). Also records each outcome in
  /// the service uptime history (see [serviceUptimeHistory]).
  Future<List<ServicePingResultDto>> pingServices(
    String activeTag,
    List<ServicePingTargetDto> services,
  ) async {
    final data = await _callAsync('PingServices', {
      'activeTag': activeTag,
      'services': services.map((s) => s.toJson()).toList(),
    });
    return (data as List)
        .cast<Map<String, dynamic>>()
        .map(ServicePingResultDto.fromJson)
        .toList();
  }

  /// Rolling history of on-demand service pings, keyed by
  /// `"<activeTag>|<service name>"`.
  Future<Map<String, List<PingSample>>> serviceUptimeHistory() async {
    final raw = await _callAsync('ServiceUptimeHistory') as Map<String, dynamic>;
    return raw.map((k, v) => MapEntry(
          k,
          (v as List)
              .cast<Map<String, dynamic>>()
              .map(PingSample.fromJson)
              .toList(),
        ));
  }

  // ----- custom services -----

  Future<List<CustomService>> listCustomServices() async {
    final data = await _callAsync('ListCustomServices');
    if (data == null) return [];
    return (data as List)
        .cast<Map<String, dynamic>>()
        .map(CustomService.fromJson)
        .toList();
  }

  Future<void> addCustomService(String name, String url) =>
      _callAsync('AddCustomService', {'name': name, 'url': url});

  Future<void> removeCustomService(String name) =>
      _callAsync('RemoveCustomService', {'name': name});

  // ----- app discovery -----

  Future<List<DiscoveredApp>> listApps({bool all = false}) async {
    final data = await _callAsync('ListApps', {'all': all});
    if (data == null) return [];
    return (data as List)
        .cast<Map<String, dynamic>>()
        .map(DiscoveredApp.fromJson)
        .toList();
  }
}
