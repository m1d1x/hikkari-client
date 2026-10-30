import 'dart:convert';
import 'dart:typed_data';

class DiscoveredApp {
  final String name;
  final String exe;
  final String path;
  final bool running;
  final Uint8List? iconBytes;

  const DiscoveredApp({
    required this.name,
    required this.exe,
    required this.path,
    this.running = false,
    this.iconBytes,
  });

  factory DiscoveredApp.fromJson(Map<String, dynamic> j) {
    Uint8List? icon;
    final iconStr = j['icon'] as String?;
    if (iconStr != null && iconStr.isNotEmpty) {
      try {
        icon = base64Decode(iconStr);
      } catch (_) {}
    }
    return DiscoveredApp(
      name: j['name'] as String? ?? '',
      exe: j['exe'] as String? ?? '',
      path: j['path'] as String? ?? '',
      running: j['running'] as bool? ?? false,
      iconBytes: icon,
    );
  }
}
