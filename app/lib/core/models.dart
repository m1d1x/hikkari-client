// Dart projections of the JSON shapes documented in
// `NekkoVPN/core/api/contract.md`. Field names match the Go JSON tags
// exactly — never rename either side independently.

class HealthInfo {
  final bool running;
  final bool tunnelOK;
  final String ip;
  final String status; // "off" | "connecting" | "ok" | "broken"
  final String message;

  const HealthInfo({
    required this.running,
    required this.tunnelOK,
    required this.ip,
    required this.status,
    required this.message,
  });

  factory HealthInfo.fromJson(Map<String, dynamic> j) => HealthInfo(
        running: j['running'] as bool? ?? false,
        tunnelOK: j['tunnelOK'] as bool? ?? false,
        ip: j['ip'] as String? ?? '',
        status: j['status'] as String? ?? 'off',
        message: j['message'] as String? ?? '',
      );

  @override
  String toString() =>
      'HealthInfo(status=$status, ip=$ip, running=$running, msg=$message)';
}

class ProfileInfo {
  final String name;
  final String label;
  final bool active;
  final String endpoint;
  final String protocol;

  const ProfileInfo({
    required this.name,
    required this.label,
    required this.active,
    required this.endpoint,
    required this.protocol,
  });

  factory ProfileInfo.fromJson(Map<String, dynamic> j) => ProfileInfo(
        name: j['name'] as String? ?? '',
        label: j['label'] as String? ?? '',
        active: j['active'] as bool? ?? false,
        endpoint: j['endpoint'] as String? ?? '',
        protocol: j['protocol'] as String? ?? '',
      );
}

/// Split-routing mode. Mirrors Go's `configfile.RoutingMode`.
///
/// - [bypass] — items in [RoutingState.bypass] go DIRECT, rest via VPN.
/// - [proxy]  — items in [RoutingState.proxy] go via VPN, rest DIRECT.
enum RoutingMode {
  bypass,
  proxy;

  String get wire => name; // "bypass" / "proxy"
  static RoutingMode fromWire(String? s) =>
      s == 'proxy' ? RoutingMode.proxy : RoutingMode.bypass;
}

class RouteList {
  final List<String> domains;
  final List<String> ips;
  final List<String> processes;

  const RouteList({
    required this.domains,
    required this.ips,
    required this.processes,
  });

  const RouteList.empty()
      : domains = const [],
        ips = const [],
        processes = const [];

  factory RouteList.fromJson(Map<String, dynamic>? j) {
    if (j == null) return const RouteList.empty();
    return RouteList(
      domains: _strList(j['domains']),
      ips: _strList(j['ips']),
      processes: _strList(j['processes']),
    );
  }

  RouteList copyWith({
    List<String>? domains,
    List<String>? ips,
    List<String>? processes,
  }) =>
      RouteList(
        domains: domains ?? this.domains,
        ips: ips ?? this.ips,
        processes: processes ?? this.processes,
      );

  /// Returns a new list with [value] appended to the given [category],
  /// deduped case-insensitively. Same semantics as the Go side.
  RouteList withAdded(String category, String value) {
    bool seen(List<String> xs) => xs.any((v) => v.toLowerCase() == value.toLowerCase());
    switch (category) {
      case 'domains':
        return seen(domains) ? this : copyWith(domains: [...domains, value]);
      case 'ips':
        return seen(ips) ? this : copyWith(ips: [...ips, value]);
      case 'processes':
        return seen(processes)
            ? this
            : copyWith(processes: [...processes, value]);
    }
    return this;
  }

  RouteList withRemoved(String category, String value) {
    List<String> drop(List<String> xs) =>
        xs.where((v) => v.toLowerCase() != value.toLowerCase()).toList();
    switch (category) {
      case 'domains':
        return copyWith(domains: drop(domains));
      case 'ips':
        return copyWith(ips: drop(ips));
      case 'processes':
        return copyWith(processes: drop(processes));
    }
    return this;
  }

  static List<String> _strList(Object? v) =>
      (v as List?)?.cast<String>() ?? const [];
}

class RoutingState {
  final bool enabled;
  final RoutingMode mode;
  final RouteList bypass;
  final RouteList proxy;

  const RoutingState({
    required this.enabled,
    required this.mode,
    required this.bypass,
    required this.proxy,
  });

  const RoutingState.initial()
      : enabled = true,
        mode = RoutingMode.bypass,
        bypass = const RouteList.empty(),
        proxy = const RouteList.empty();

  factory RoutingState.fromJson(Map<String, dynamic> j) => RoutingState(
        enabled: j['enabled'] as bool? ?? true,
        mode: RoutingMode.fromWire(j['mode'] as String?),
        bypass:
            RouteList.fromJson(j['bypass'] as Map<String, dynamic>?),
        proxy: RouteList.fromJson(j['proxy'] as Map<String, dynamic>?),
      );

  /// Returns the active list for the current mode.
  RouteList get active => mode == RoutingMode.bypass ? bypass : proxy;

  RoutingState copyWith({
    bool? enabled,
    RoutingMode? mode,
    RouteList? bypass,
    RouteList? proxy,
  }) =>
      RoutingState(
        enabled: enabled ?? this.enabled,
        mode: mode ?? this.mode,
        bypass: bypass ?? this.bypass,
        proxy: proxy ?? this.proxy,
      );
}

class PingSample {
  final int ts;
  final bool ok;
  final int rttMs;

  const PingSample({required this.ts, required this.ok, required this.rttMs});

  factory PingSample.fromJson(Map<String, dynamic> j) => PingSample(
        ts: j['ts'] as int? ?? 0,
        ok: j['ok'] as bool? ?? false,
        rttMs: j['rtt_ms'] as int? ?? 0,
      );
}
