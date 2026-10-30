import 'models.dart';

class ServicePreset {
  final String id;
  final String name;
  final String icon;
  final List<String> domains;
  final List<String> ips;
  final Map<String, List<String>> processes;
  final List<String> defaultModes;

  const ServicePreset({
    required this.id,
    required this.name,
    required this.icon,
    required this.domains,
    required this.ips,
    required this.processes,
    required this.defaultModes,
  });

  factory ServicePreset.fromJson(Map<String, dynamic> json) => ServicePreset(
    id: json['id'] as String,
    name: json['name'] as String,
    icon: json['icon'] as String? ?? '',
    domains: _strings(json['domains']),
    ips: _strings(json['ips']),
    processes: (json['processes'] as Map<String, dynamic>? ?? {}).map(
      (key, value) => MapEntry(key, _strings(value)),
    ),
    defaultModes: _strings(json['defaultModes']),
  );

  String details(String platform) {
    final apps = processes[platform] ?? const <String>[];
    return [
      if (domains.isNotEmpty) 'Домены и их поддомены:\n${domains.join('\n')}',
      if (apps.isNotEmpty) 'Приложения на этом устройстве:\n${apps.join('\n')}',
      if (ips.isNotEmpty) 'IP / подсети:\n${ips.join('\n')}',
    ].join('\n\n');
  }
}

class RoutingPresets {
  final bool syncEnabled;
  final bool configured;
  final bool connected;
  final String source;
  final String platform;
  final String error;
  final String lastChecked;
  final int version;
  final List<ServicePreset> services;
  final Map<String, Map<String, bool>> choices;

  const RoutingPresets({
    required this.syncEnabled,
    required this.configured,
    required this.connected,
    required this.source,
    required this.platform,
    required this.error,
    required this.lastChecked,
    required this.version,
    required this.services,
    required this.choices,
  });

  factory RoutingPresets.fromJson(Map<String, dynamic> json) {
    final catalog = json['catalog'] as Map<String, dynamic>;
    return RoutingPresets(
      syncEnabled: json['syncEnabled'] as bool,
      configured: json['configured'] as bool,
      connected: json['connected'] as bool? ?? false,
      source: json['source'] as String,
      platform: json['platform'] as String,
      error: json['error'] as String? ?? '',
      lastChecked: json['lastChecked'] as String? ?? '',
      version: (catalog['version'] as num).toInt(),
      services: (catalog['services'] as List)
          .map((entry) => ServicePreset.fromJson(entry as Map<String, dynamic>))
          .toList(),
      choices: (json['choices'] as Map<String, dynamic>? ?? {}).map(
        (mode, entries) =>
            MapEntry(mode, Map<String, bool>.from(entries as Map)),
      ),
    );
  }

  bool selected(ServicePreset service, RoutingMode mode) =>
      choices[mode.wire]?[service.id] ??
      service.defaultModes.contains(mode.wire);

  String get status {
    if (!syncEnabled) return 'Автообновление выключено · сохранённая версия';
    if (error.isNotEmpty) {
      return 'Нет обновления · используется сохранённый список';
    }
    if (!configured) return 'Встроенный список · сервер ещё не подключён';
    if (connected) return 'Обновляется автоматически';
    if (source == 'cache') return 'Сохранённый список · проверяем обновления';
    return 'Автообновление включено';
  }
}

List<String> _strings(dynamic value) =>
    (value as List? ?? const []).cast<String>();
