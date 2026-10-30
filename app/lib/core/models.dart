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

/// Одна конкретная причина, почему выход выглядит грязным (или заметка, почему
/// чистым). Mirrors Go's `cleanliness.Reason`.
class CleanlinessReason {
  final String severity; // "high" | "med" | "low" | "good"
  final String title;
  final String fix; // как починить ("" если нет)

  const CleanlinessReason({
    required this.severity,
    required this.title,
    required this.fix,
  });

  factory CleanlinessReason.fromJson(Map<String, dynamic> j) =>
      CleanlinessReason(
        severity: j['severity'] as String? ?? 'low',
        title: j['title'] as String? ?? '',
        fix: j['fix'] as String? ?? '',
      );
}

/// Dart projection of Go's `cleanliness.Report` — the "whiteness" of the
/// active egress as the internet sees it. Field names mirror the Go JSON
/// tags exactly.
class ServerCleanliness {
  final bool running; // tunnel up
  final bool checked; // we have a successful probe
  final String ip;
  final String asn; // "AS210644"
  final String asname; // "AEZA-AS"
  final String asnType; // "hosting" | "isp" | ...
  final String asnCountry; // ISO code where the ASN is registered
  final String isp;
  final String org;
  final String country; // ISO code
  final String city;
  final bool isHosting; // datacenter range
  final bool isMobile;
  final bool isProxy;
  final bool isVpn;
  final bool isTor;
  final bool isAbuser; // seen in abuse/spam feeds
  final bool isCrawler;
  final String abuserLabel; // "High"/"Elevated"/... or ""
  final int fraudScore; // 0-100, or -1 when unknown (no IPQS key)
  final String abuseEmail; // network abuse contact
  final String cfColo; // Cloudflare edge, e.g. "FRA"
  final String cfWarp;
  final int score; // 0-100 whiteness index
  final String verdict; // off|checking|clean|suspicious|datacenter|flagged
  final String message;
  final List<CleanlinessReason> reasons; // почему фродит
  final String checkedAt;

  const ServerCleanliness({
    required this.running,
    required this.checked,
    required this.ip,
    required this.asn,
    required this.asname,
    required this.asnType,
    required this.asnCountry,
    required this.isp,
    required this.org,
    required this.country,
    required this.city,
    required this.isHosting,
    required this.isMobile,
    required this.isProxy,
    required this.isVpn,
    required this.isTor,
    required this.isAbuser,
    required this.isCrawler,
    required this.abuserLabel,
    required this.fraudScore,
    required this.abuseEmail,
    required this.cfColo,
    required this.cfWarp,
    required this.score,
    required this.verdict,
    required this.message,
    required this.reasons,
    required this.checkedAt,
  });

  factory ServerCleanliness.fromJson(Map<String, dynamic> j) =>
      ServerCleanliness(
        running: j['running'] as bool? ?? false,
        checked: j['checked'] as bool? ?? false,
        ip: j['ip'] as String? ?? '',
        asn: j['asn'] as String? ?? '',
        asname: j['asname'] as String? ?? '',
        asnType: j['asnType'] as String? ?? '',
        asnCountry: j['asnCountry'] as String? ?? '',
        isp: j['isp'] as String? ?? '',
        org: j['org'] as String? ?? '',
        country: j['country'] as String? ?? '',
        city: j['city'] as String? ?? '',
        isHosting: j['isHosting'] as bool? ?? false,
        isMobile: j['isMobile'] as bool? ?? false,
        isProxy: j['isProxy'] as bool? ?? false,
        isVpn: j['isVpn'] as bool? ?? false,
        isTor: j['isTor'] as bool? ?? false,
        isAbuser: j['isAbuser'] as bool? ?? false,
        isCrawler: j['isCrawler'] as bool? ?? false,
        abuserLabel: j['abuserLabel'] as String? ?? '',
        fraudScore: (j['fraudScore'] as num?)?.toInt() ?? -1,
        abuseEmail: j['abuseEmail'] as String? ?? '',
        cfColo: j['cfColo'] as String? ?? '',
        cfWarp: j['cfWarp'] as String? ?? '',
        score: (j['score'] as num?)?.toInt() ?? 0,
        verdict: j['verdict'] as String? ?? 'off',
        message: j['message'] as String? ?? '',
        reasons: (j['reasons'] as List<dynamic>? ?? const [])
            .map((e) => CleanlinessReason.fromJson(e as Map<String, dynamic>))
            .toList(),
        checkedAt: j['checkedAt'] as String? ?? '',
      );

  static const off = ServerCleanliness(
    running: false,
    checked: false,
    ip: '',
    asn: '',
    asname: '',
    asnType: '',
    asnCountry: '',
    isp: '',
    org: '',
    country: '',
    city: '',
    isHosting: false,
    isMobile: false,
    isProxy: false,
    isVpn: false,
    isTor: false,
    isAbuser: false,
    isCrawler: false,
    abuserLabel: '',
    fraudScore: -1,
    abuseEmail: '',
    cfColo: '',
    cfWarp: '',
    score: 0,
    verdict: 'off',
    message: '',
    reasons: [],
    checkedAt: '',
  );
}

class ProfileInfo {
  final String name;
  final String label;
  final bool active;
  final String endpoint;
  final String protocol;
  final SubscriptionInfo? subscription;

  const ProfileInfo({
    required this.name,
    required this.label,
    required this.active,
    required this.endpoint,
    required this.protocol,
    this.subscription,
  });

  factory ProfileInfo.fromJson(Map<String, dynamic> j) => ProfileInfo(
    name: j['name'] as String? ?? '',
    label: j['label'] as String? ?? '',
    active: j['active'] as bool? ?? false,
    endpoint: j['endpoint'] as String? ?? '',
    protocol: j['protocol'] as String? ?? '',
    subscription: j['subscription'] is Map<String, dynamic>
        ? SubscriptionInfo.fromJson(j['subscription'] as Map<String, dynamic>)
        : null,
  );
}

class SubscriptionInfo {
  final String id;
  final String title;
  final int uploadBytes;
  final int downloadBytes;
  final int totalBytes;
  final int expiresAt;
  final int updateIntervalSeconds;
  final String webPageUrl;
  final String supportUrl;

  const SubscriptionInfo({
    required this.id,
    required this.title,
    required this.uploadBytes,
    required this.downloadBytes,
    required this.totalBytes,
    required this.expiresAt,
    required this.updateIntervalSeconds,
    required this.webPageUrl,
    required this.supportUrl,
  });

  factory SubscriptionInfo.fromJson(Map<String, dynamic> j) => SubscriptionInfo(
    id: j['id'] as String? ?? '',
    title: j['title'] as String? ?? '',
    uploadBytes: (j['uploadBytes'] as num?)?.toInt() ?? 0,
    downloadBytes: (j['downloadBytes'] as num?)?.toInt() ?? 0,
    totalBytes: (j['totalBytes'] as num?)?.toInt() ?? 0,
    expiresAt: (j['expiresAt'] as num?)?.toInt() ?? 0,
    updateIntervalSeconds: (j['updateIntervalSeconds'] as num?)?.toInt() ?? 0,
    webPageUrl: j['webPageUrl'] as String? ?? '',
    supportUrl: j['supportUrl'] as String? ?? '',
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
  }) => RouteList(
    domains: domains ?? this.domains,
    ips: ips ?? this.ips,
    processes: processes ?? this.processes,
  );

  /// Returns a new list with [value] appended to the given [category],
  /// deduped case-insensitively. Same semantics as the Go side.
  RouteList withAdded(String category, String value) {
    bool seen(List<String> xs) =>
        xs.any((v) => v.toLowerCase() == value.toLowerCase());
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
    bypass: RouteList.fromJson(j['bypass'] as Map<String, dynamic>?),
    proxy: RouteList.fromJson(j['proxy'] as Map<String, dynamic>?),
  );

  /// Returns the active list for the current mode.
  RouteList get active => mode == RoutingMode.bypass ? bypass : proxy;

  RoutingState copyWith({
    bool? enabled,
    RoutingMode? mode,
    RouteList? bypass,
    RouteList? proxy,
  }) => RoutingState(
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

/// A single user service to probe through the active profile. Mirrors Go's
/// `api.ServicePingTarget`.
class ServicePingTargetDto {
  final String name;
  final String url;

  const ServicePingTargetDto({required this.name, required this.url});

  Map<String, Object?> toJson() => {'name': name, 'url': url};
}

/// Outcome of probing one [ServicePingTargetDto]. Mirrors Go's
/// `api.ServicePingResult`.
class ServicePingResultDto {
  final String name;
  final bool ok;
  final int rttMs;

  const ServicePingResultDto({
    required this.name,
    required this.ok,
    required this.rttMs,
  });

  factory ServicePingResultDto.fromJson(Map<String, dynamic> j) =>
      ServicePingResultDto(
        name: j['name'] as String? ?? '',
        ok: j['ok'] as bool? ?? false,
        rttMs: j['rtt_ms'] as int? ?? 0,
      );
}

class CustomService {
  final String name;
  final String url;

  const CustomService({required this.name, required this.url});

  factory CustomService.fromJson(Map<String, dynamic> j) => CustomService(
    name: j['Name'] as String? ?? '',
    url: j['URL'] as String? ?? '',
  );
}

/// Adapter capabilities: feature support belongs to the selected engine.
class EngineInfo {
  final String id;
  final String name;
  final bool tun;
  final bool domainRouting;
  final bool ipRouting;
  final bool autoSelect;
  final bool liveProfileSwitch;
  final bool profileProbe;
  final bool processRouting;
  const EngineInfo({
    required this.id,
    required this.name,
    required this.tun,
    required this.domainRouting,
    required this.ipRouting,
    required this.autoSelect,
    required this.liveProfileSwitch,
    required this.profileProbe,
    required this.processRouting,
  });
  factory EngineInfo.fromJson(Map<String, dynamic> json) {
    final caps = json['capabilities'] as Map<String, dynamic>? ?? const {};
    return EngineInfo(
      id: json['id'] as String? ?? '',
      name: json['name'] as String? ?? '',
      tun: caps['tun'] as bool? ?? false,
      domainRouting: caps['domainRouting'] as bool? ?? false,
      ipRouting: caps['ipRouting'] as bool? ?? false,
      autoSelect: caps['autoSelect'] as bool? ?? false,
      liveProfileSwitch: caps['liveProfileSwitch'] as bool? ?? false,
      profileProbe: caps['profileProbe'] as bool? ?? false,
      processRouting: caps['processRouting'] as bool? ?? false,
    );
  }
}

/// Dart projection of Go's `svcproto.UpdateState` — what HikkariService
/// knows about updates. See contract.md, *Updates*.
class UpdateStatus {
  final String state; // idle | downloading | ready | error | unavailable
  final String installed;
  final String version; // staged / downloading release
  final bool mandatory;
  final int bytesDone;
  final int bytesTotal;
  final String error;

  const UpdateStatus({
    required this.state,
    this.installed = '',
    this.version = '',
    this.mandatory = false,
    this.bytesDone = 0,
    this.bytesTotal = 0,
    this.error = '',
  });

  static const unavailable = UpdateStatus(state: 'unavailable');

  bool get isReady => state == 'ready';
  bool get isDownloading => state == 'downloading';

  factory UpdateStatus.fromJson(Map<String, dynamic> j) => UpdateStatus(
    state: j['state'] as String? ?? 'unavailable',
    installed: j['installed'] as String? ?? '',
    version: j['version'] as String? ?? '',
    mandatory: j['mandatory'] as bool? ?? false,
    bytesDone: (j['bytesDone'] as num?)?.toInt() ?? 0,
    bytesTotal: (j['bytesTotal'] as num?)?.toInt() ?? 0,
    error: j['error'] as String? ?? '',
  );
}
