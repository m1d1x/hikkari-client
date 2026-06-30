import 'dart:convert';

import 'envelope.dart';
import 'models.dart';

/// Platform-agnostic surface to the nekkobox engine. The desktop binding
/// (nekkobox_ffi.dart) implements [rawCall] over `dart:ffi`; mobile will
/// add a MethodChannel implementation later. Both forward to the same
/// Go `core/api.Core.Call` and the same JSON contract.
abstract class NekkoboxCore {
  /// One-shot raw call. Implementations return the raw envelope JSON
  /// string emitted by Go; the typed helpers below parse it.
  String rawCall(String method, [String argsJson = '']);

  // ----- typed helpers -----

  Object? _call(String method, [Map<String, Object?>? args]) {
    final raw = rawCall(method, args == null ? '' : jsonEncode(args));
    final env = Envelope.fromJson(jsonDecode(raw) as Map<String, dynamic>);
    return env.unwrap();
  }

  // ----- lifecycle -----

  void init(String baseDir) {
    _call('Init', {'baseDir': baseDir});
  }

  void shutdown() {
    _call('Shutdown');
  }

  // ----- VPN -----

  void startVpn() => _call('StartVPN');
  void stopVpn() => _call('StopVPN');
  void restartVpn() => _call('RestartVPN');
  bool isRunning() => _call('IsRunning') as bool;

  // ----- health -----

  HealthInfo health() =>
      HealthInfo.fromJson(_call('Health') as Map<String, dynamic>);

  String externalIp() => _call('ExternalIP') as String;

  // ----- logs -----

  String logTail() => _call('LogTail') as String;

  // ----- routing -----

  RoutingState routing() =>
      RoutingState.fromJson(_call('Routing') as Map<String, dynamic>);

  void setRoutingEnabled(bool on) =>
      _call('SetRoutingEnabled', {'on': on});

  void setRoutingMode(RoutingMode mode) =>
      _call('SetRoutingMode', {'mode': mode.wire});

  void addRule(RoutingMode mode, String category, String value) => _call(
        'AddRule',
        {'mode': mode.wire, 'category': category, 'value': value},
      );

  void removeRule(RoutingMode mode, String category, String value) => _call(
        'RemoveRule',
        {'mode': mode.wire, 'category': category, 'value': value},
      );

  // ----- profiles -----

  List<ProfileInfo> profiles() => (_call('Profiles') as List)
      .cast<Map<String, dynamic>>()
      .map(ProfileInfo.fromJson)
      .toList();

  String activeProfile() => _call('ActiveProfile') as String;

  void renameProfile(String name, String label) =>
      _call('RenameProfile', {'name': name, 'label': label});

  void deleteProfile(String name) =>
      _call('DeleteProfile', {'name': name});

  void switchProfile(String name) =>
      _call('SwitchProfile', {'name': name});

  /// Imports a profile from a server URI (e.g. `hysteria2://...`). Returns
  /// the generated profile name on success; throws [NekkoboxError] if the
  /// URI scheme is unsupported or malformed.
  String addProfileFromUri(String uri) =>
      _call('AddProfileFromURI', {'uri': uri}) as String;

  /// Whether auto-fastest mode (urltest group) is on.
  bool autoMode() => _call('AutoMode') as bool;

  /// Toggles auto-fastest mode. Reloads the engine if it was running.
  void setAutoMode(bool on) => _call('SetAutoMode', {'on': on});

  // ----- uptime -----

  Map<String, List<PingSample>> uptimeHistory() {
    final raw = _call('UptimeHistory') as Map<String, dynamic>;
    return raw.map((k, v) => MapEntry(
          k,
          (v as List)
              .cast<Map<String, dynamic>>()
              .map(PingSample.fromJson)
              .toList(),
        ));
  }
}
