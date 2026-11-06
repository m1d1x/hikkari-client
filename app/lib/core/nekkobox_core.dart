import 'dart:convert';

import 'package:flutter/foundation.dart';

import 'app_discovery.dart';
import 'envelope.dart';
import 'models.dart';
import 'routing_presets.dart';
import 'routing_document.dart';

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

  Future<Object?> _callAsync(
    String method, [
    Map<String, Object?>? args,
  ]) async {
    final raw = await rawCallAsync(
      method,
      args == null ? '' : jsonEncode(args),
    );
    final env = Envelope.fromJson(jsonDecode(raw) as Map<String, dynamic>);
    return env.unwrap();
  }

  // ----- lifecycle (sync bootstrap) -----

  void init(
    String baseDir, {
    String engineId = '',
    bool routingPreview = false,
  }) {
    _call('Init', {
      'baseDir': baseDir,
      'engineId': engineId,
      'routingPreview': routingPreview,
    });
  }

  void shutdown() {
    _call('Shutdown');
  }

  // ----- VPN -----

  Future<void> restartVpnAsync() => _callAsync('RestartVPN');
  Future<void> startVpnAsync() => _callAsync('StartVPN');
  Future<void> stopVpnAsync() => _callAsync('StopVPN');
  Future<bool> isRunning() async => await _callAsync('IsRunning') as bool;

  Future<EngineInfo> engineInfo() async => EngineInfo.fromJson(
    await _callAsync('EngineInfo') as Map<String, dynamic>,
  );

  Future<void> validateConfiguration() =>
      _callAsync('ValidateConfiguration').then((_) {});

  // ----- updates (Windows; see contract.md) -----

  Future<UpdateStatus> updateStatus() async => UpdateStatus.fromJson(
    await _callAsync('UpdateStatus') as Map<String, dynamic>,
  );
  Future<void> checkUpdate() => _callAsync('CheckUpdate');

  /// Hands over to the launcher; the app must exit right after this returns.
  Future<void> restartToUpdate() => _callAsync('RestartToUpdate');

  /// Arranges for the installed release to open once this process exits
  /// ([UpdateStatus.replaced]); the app must exit right after this returns.
  Future<void> relaunchApp() => _callAsync('RelaunchApp');

  // ----- network doctor (see engine core/netdoctor) -----

  /// Read-only check: system proxy verdict and the direct-path selection.
  Future<Map<String, dynamic>> networkDiagnose() async =>
      (await _callAsync('NetworkDiagnose') as Map<String, dynamic>?) ?? {};

  /// Soft repair: orphan proxy off, DNS cache flushed, direct path
  /// re-selected (reconnecting a running tunnel).
  Future<Map<String, dynamic>> repairNetwork() async =>
      (await _callAsync('RepairNetwork') as Map<String, dynamic>?) ?? {};

  /// Read-only: what the deep reset would deal with (hosts overrides, DPI
  /// tools) and the foreign WFP providers nothing touches.
  Future<Map<String, dynamic>> scanNetworkStack() async =>
      (await _callAsync('ScanNetworkStack') as Map<String, dynamic>?) ?? {};

  /// Deep reset through the service (Winsock + TCP/IP, needs a reboot).
  /// Without [force] it is refused while static-IP adapters would lose
  /// their configuration; the result then lists them.
  Future<Map<String, dynamic>> resetNetworkStack({bool force = false}) async =>
      (await _callAsync('ResetNetworkStack', {'force': force})
          as Map<String, dynamic>?) ??
      {};

  // ----- health -----

  Future<HealthInfo> health() async =>
      HealthInfo.fromJson(await _callAsync('Health') as Map<String, dynamic>);

  Future<String> externalIp() async => await _callAsync('ExternalIP') as String;

  /// "Whiteness" of the active egress — cached engine-side, instant read.
  Future<ServerCleanliness> serverCleanliness() async =>
      ServerCleanliness.fromJson(
        await _callAsync('ServerCleanliness') as Map<String, dynamic>,
      );

  // ----- logs -----

  Future<String> logTail() async =>
      ((await _callAsync('LogTail')) as String?) ?? '';

  Future<String> collectDiagnostics([int maxLines = 500]) async =>
      ((await _callAsync('CollectDiagnostics', {'maxLines': maxLines}))
          as String?) ??
      '';

  Future<Map<String, dynamic>> submitSupportReport(String message) async {
    final res = await _callAsync('SubmitSupportReport', {'message': message});
    return (res as Map<String, dynamic>?) ?? {};
  }

  // ----- routing -----

  Future<RoutingApplicationSnapshot> routingStatus() async =>
      RoutingApplicationSnapshot.fromJson(
        await _callAsync('RoutingStatus') as Map<String, dynamic>,
      );

  Future<void> flushRouting() => _callAsync('FlushRouting');

  Future<RoutingDocument> routingDocument() async => RoutingDocument.fromJson(
    await _callAsync('RoutingDocument') as Map<String, dynamic>,
  );

  Future<RoutingDocumentPreview> previewRoutingDocument(
    RoutingDocument document,
  ) async => RoutingDocumentPreview.fromJson(
    await _callAsync('PreviewRoutingDocument', {'document': document.toJson()})
        as Map<String, dynamic>,
  );

  Future<RoutingPresets> routingPresets() async => RoutingPresets.fromJson(
    await _callAsync('RoutingPresets') as Map<String, dynamic>,
  );

  Future<void> setPresetSync(bool on) =>
      _callAsync('SetPresetSync', {'on': on});

  /// Writes what the user was shown into the engine log as an `[ui]` line.
  Future<void> logUi(String message) =>
      _callAsync('LogUI', {'message': message});

  Future<void> setPresetTester(bool on) =>
      _callAsync('SetPresetTester', {'on': on});

  Future<void> setServicePreset(RoutingMode mode, String id, bool on) =>
      _callAsync('SetServicePreset', {'mode': mode.wire, 'id': id, 'on': on});

  Future<void> refreshRoutingPresets() => _callAsync('RefreshRoutingPresets');

  Future<String> exportRoutingSnapshot() async =>
      await _callAsync('ExportRoutingSnapshot') as String;

  Future<void> importRoutingSnapshot(String data) =>
      _callAsync('ImportRoutingSnapshot', {'data': data});

  Future<void> resetToDefaults() => _callAsync('ResetToDefaults');

  Future<RoutingState> routing() async => RoutingState.fromJson(
    await _callAsync('Routing') as Map<String, dynamic>,
  );

  Future<void> setRoutingEnabled(bool on) =>
      _callAsync('SetRoutingEnabled', {'on': on});

  Future<void> setRoutingMode(RoutingMode mode) =>
      _callAsync('SetRoutingMode', {'mode': mode.wire});

  Future<void> addRule(RoutingMode mode, String category, String value) =>
      _callAsync('AddRule', {
        'mode': mode.wire,
        'category': category,
        'value': value,
      });

  Future<void> removeRule(RoutingMode mode, String category, String value) =>
      _callAsync('RemoveRule', {
        'mode': mode.wire,
        'category': category,
        'value': value,
      });

  // ----- profiles -----

  /// Lifecycle hint; synchronization and throttling remain in the engine.
  Future<void> subscriptionWake() => _callAsync('SubscriptionWake');

  Future<List<ProfileInfo>> profiles() async =>
      (await _callAsync('Profiles') as List)
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

  /// Serialises [name] back into a shareable subscription URI (the inverse of
  /// [addProfileFromUri]), for the share sheet's QR + copy. Throws
  /// [NekkoboxError] if the engine can't export this profile.
  Future<String> exportProfileUri(String name) async =>
      await _callAsync('ExportProfileURI', {'name': name}) as String;

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

  /// The profile traffic goes through: in auto mode the core's own choice.
  /// Empty while the engine is off.
  Future<String> carryingProfile() async =>
      await _callAsync('CarryingProfile') as String? ?? '';

  // ----- uptime -----

  /// Tells the engine the server list went on or off screen. Other servers are
  /// measured only while it is on screen.
  Future<void> setServerListOpen(bool open) =>
      _callAsync('SetServerListOpen', {'open': open});

  Future<Map<String, List<PingSample>>> uptimeHistory() async {
    final raw = await _callAsync('UptimeHistory') as Map<String, dynamic>;
    return raw.map(
      (k, v) => MapEntry(
        k,
        (v as List)
            .cast<Map<String, dynamic>>()
            .map(PingSample.fromJson)
            .toList(),
      ),
    );
  }

  // ----- service uptime -----

  /// Probes each service in parallel through the active profile and returns
  /// one result per service (order preserved). Also records each outcome in
  /// the service uptime history (see [serviceUptimeHistory]).
  Future<List<ServicePingResultDto>> pingServices(
    String profileId,
    List<ServicePingTargetDto> services,
  ) async {
    final data = await _callAsync('PingServices', {
      'profileId': profileId,
      'services': services.map((s) => s.toJson()).toList(),
    });
    return (data as List)
        .cast<Map<String, dynamic>>()
        .map(ServicePingResultDto.fromJson)
        .toList();
  }

  /// Rolling history of on-demand service pings, keyed by
  /// `"<profileId>|<service name>"`.
  Future<Map<String, List<PingSample>>> serviceUptimeHistory() async {
    final raw =
        await _callAsync('ServiceUptimeHistory') as Map<String, dynamic>;
    return raw.map(
      (k, v) => MapEntry(
        k,
        (v as List)
            .cast<Map<String, dynamic>>()
            .map(PingSample.fromJson)
            .toList(),
      ),
    );
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

  final _appRequests = <bool, Future<List<DiscoveredApp>>>{};

  Future<List<DiscoveredApp>> listApps({bool all = false}) =>
      _appRequests.putIfAbsent(all, () async {
        try {
          final raw = await rawCallAsync('ListApps', jsonEncode({'all': all}));
          // JSON and base64 icons can be large even when FFI itself is async.
          return await compute(_decodeApps, raw);
        } finally {
          _appRequests.remove(all);
        }
      });
}

List<DiscoveredApp> _decodeApps(String raw) {
  final data = Envelope.fromJson(
    jsonDecode(raw) as Map<String, dynamic>,
  ).unwrap();
  if (data == null) return [];
  return (data as List)
      .cast<Map<String, dynamic>>()
      .map(DiscoveredApp.fromJson)
      .toList();
}
