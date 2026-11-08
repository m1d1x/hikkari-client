// Links that open the app: `hikkari://import?url=<link>`, which the landing's
// «открыть в hikkari» and any other page use to hand a subscription over.
//
// The platform runner (macOS AppDelegate, Windows FlutterWindow) owns the
// `hikkari/links` channel. A link that arrives before Dart is listening —
// the one the app was started with — waits there until [DeepLinks.listen]
// asks for it with `ready`; later ones are pushed with `open`.

import 'dart:async';

import 'package:flutter/services.dart';

/// What a link asks the app to do.
sealed class DeepLink {
  const DeepLink();

  /// Reads a `hikkari://` URI; null for anything the app does not handle.
  static DeepLink? parse(String raw) {
    final uri = Uri.tryParse(raw.trim());
    if (uri == null || uri.scheme != 'hikkari') return null;
    switch (uri.host) {
      case 'import':
        final link = uri.queryParameters['url']?.trim() ?? '';
        return ImportLink.supports(link) ? ImportLink(link) : null;
    }
    return null;
  }
}

/// Import a subscription or a server key. The import dialog shows progress
/// immediately; importing never starts a VPN connection.
final class ImportLink extends DeepLink {
  final String link;

  const ImportLink(this.link);

  static const schemes = [
    'https://',
    'http://',
    'vless://',
    'trojan://',
    'ss://',
    'hysteria2://',
    'vmess://',
    'tuic://',
    'naive+https://',
    'naive+quic://',
    'hikkari+v1://',
    'vpn://',
  ];

  static bool supports(String link) {
    final lower = link.trim().toLowerCase();
    return schemes.any(lower.startsWith);
  }
}

class DeepLinks {
  static const _channel = MethodChannel('hikkari/links');

  /// Calls [onLink] for the link the app started with, if any, and for every
  /// link that opens it from now on.
  static Future<void> listen(void Function(DeepLink link) onLink) async {
    _channel.setMethodCallHandler((call) async {
      if (call.method == 'open' && call.arguments is String) {
        final link = DeepLink.parse(call.arguments as String);
        if (link != null) onLink(link);
      }
    });
    final List<Object?> pending;
    try {
      pending = await _channel.invokeListMethod<Object?>('ready') ?? const [];
    } on MissingPluginException {
      // A platform without a runner side (tests, Linux) opens no links.
      return;
    }
    for (final raw in pending) {
      if (raw is! String) continue;
      final link = DeepLink.parse(raw);
      if (link != null) onLink(link);
    }
  }
}
