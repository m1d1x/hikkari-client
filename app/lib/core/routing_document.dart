import 'models.dart';

/// Portable intent DTO. Validation, merging and execution belong to Go.
class RoutingDocument {
  static const schemaVersion = 1;
  final bool enabled;
  final RoutingMode mode;
  final List<RoutingSource> sources;

  const RoutingDocument({
    required this.enabled,
    required this.mode,
    required this.sources,
  });

  factory RoutingDocument.fromJson(Map<String, dynamic> json) {
    if (json['schemaVersion'] != schemaVersion) {
      throw const FormatException('Неподдерживаемая версия маршрутизации');
    }
    if (json['mode'] != 'bypass' && json['mode'] != 'proxy') {
      throw const FormatException('Неизвестный режим маршрутизации');
    }
    return RoutingDocument(
      enabled: json['enabled'] as bool,
      mode: RoutingMode.fromWire(json['mode'] as String),
      sources: List.unmodifiable(
        (json['sources'] as List).map(
          (source) => RoutingSource.fromJson(source as Map<String, dynamic>),
        ),
      ),
    );
  }

  Map<String, Object?> toJson() => {
    'schemaVersion': schemaVersion,
    'enabled': enabled,
    'mode': mode.wire,
    'sources': sources.map((source) => source.toJson()).toList(),
  };
}

enum RoutingSourceKind { manual, catalog, snapshot }

class RoutingSource {
  final String id;
  final RoutingSourceKind kind;
  final String name;
  final RoutingRules bypass;
  final RoutingRules proxy;

  const RoutingSource({
    required this.id,
    required this.kind,
    required this.name,
    required this.bypass,
    required this.proxy,
  });

  factory RoutingSource.fromJson(Map<String, dynamic> json) => RoutingSource(
    id: json['id'] as String,
    kind: RoutingSourceKind.values.firstWhere(
      (kind) => kind.name == json['kind'],
      orElse: () => throw const FormatException(
        'Неизвестный вид источника маршрутизации',
      ),
    ),
    name: json['name'] as String,
    bypass: RoutingRules.fromJson(json['bypass'] as Map<String, dynamic>),
    proxy: RoutingRules.fromJson(json['proxy'] as Map<String, dynamic>),
  );

  Map<String, Object?> toJson() => {
    'id': id,
    'kind': kind.name,
    'name': name,
    'bypass': bypass.toJson(),
    'proxy': proxy.toJson(),
  };
}

class RoutingRules {
  final List<String> domains;
  final List<String> ips;
  final Map<String, List<String>> processes;

  const RoutingRules({
    required this.domains,
    required this.ips,
    required this.processes,
  });

  factory RoutingRules.fromJson(Map<String, dynamic> json) => RoutingRules(
    domains: List.unmodifiable(
      (json['domains'] as List? ?? const []).cast<String>(),
    ),
    ips: List.unmodifiable((json['ips'] as List? ?? const []).cast<String>()),
    processes: Map.unmodifiable(
      (json['processes'] as Map<String, dynamic>? ?? const {}).map(
        (platform, values) => MapEntry(
          platform,
          List<String>.unmodifiable(
            (values as List? ?? const []).cast<String>(),
          ),
        ),
      ),
    ),
  );

  Map<String, Object?> toJson() => {
    'domains': domains,
    'ips': ips,
    'processes': processes,
  };
}

class RoutingDocumentPreview {
  final String platform;
  final RoutingState effective;
  final RouteList rules;
  final String target;
  final String defaultTarget;

  const RoutingDocumentPreview({
    required this.platform,
    required this.effective,
    required this.rules,
    required this.target,
    required this.defaultTarget,
  });

  factory RoutingDocumentPreview.fromJson(Map<String, dynamic> json) {
    final policy = json['policy'] as Map<String, dynamic>;
    final target = policy['target'] as String;
    final defaultTarget = policy['defaultTarget'] as String;
    if (!const ['direct', 'vpn'].contains(target) ||
        !const ['direct', 'vpn'].contains(defaultTarget)) {
      throw const FormatException('Неизвестное направление маршрутизации');
    }
    return RoutingDocumentPreview(
      platform: json['platform'] as String,
      effective: RoutingState.fromJson(
        json['effective'] as Map<String, dynamic>,
      ),
      rules: RouteList.fromJson(policy['rules'] as Map<String, dynamic>),
      target: target,
      defaultTarget: defaultTarget,
    );
  }
}

/// Runtime state is separate from the portable document. Go owns the sequence,
/// revision and apply outcome; preview never claims a real backend apply.
enum RoutingApplicationPhase {
  idle,
  pending,
  applying,
  applied,
  failed,
  preview,
}

class RoutingApplicationSnapshot {
  final int sequence;
  final int revision;
  final int appliedRevision;
  final RoutingApplicationPhase status;
  final String error;
  final int appliedAt;
  final bool preview;
  final RoutingState manual;
  final RoutingState effective;

  const RoutingApplicationSnapshot({
    required this.sequence,
    required this.revision,
    required this.appliedRevision,
    required this.status,
    required this.error,
    required this.appliedAt,
    required this.preview,
    required this.manual,
    required this.effective,
  });

  factory RoutingApplicationSnapshot.fromJson(Map<String, dynamic> json) =>
      RoutingApplicationSnapshot(
        sequence: json['sequence'] as int,
        revision: json['revision'] as int,
        appliedRevision: json['appliedRevision'] as int,
        status: RoutingApplicationPhase.values.firstWhere(
          (value) => value.name == json['status'],
          orElse: () => throw const FormatException(
            'Неизвестное состояние применения маршрутизации',
          ),
        ),
        error: json['error'] as String,
        appliedAt: json['appliedAt'] as int,
        preview: json['preview'] as bool,
        manual: RoutingState.fromJson(json['manual'] as Map<String, dynamic>),
        effective: RoutingState.fromJson(
          json['effective'] as Map<String, dynamic>,
        ),
      );
}
