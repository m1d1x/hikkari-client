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

class Whitelist {
  final List<String> domains;
  final List<String> ips;
  final List<String> processes;

  const Whitelist({
    required this.domains,
    required this.ips,
    required this.processes,
  });

  factory Whitelist.fromJson(Map<String, dynamic> j) => Whitelist(
        domains: _strList(j['domains']),
        ips: _strList(j['ips']),
        processes: _strList(j['processes']),
      );

  static List<String> _strList(Object? v) =>
      (v as List?)?.cast<String>() ?? const [];
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
