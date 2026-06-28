// Minimal smoke-test UI for the FFI bridge. One button toggles the VPN
// core; a status row polls Health() once a second so we can see end-to-end
// behaviour without any state-management plumbing yet. This is throwaway —
// gets replaced with the real UI once the bridge is proven solid.

import 'dart:async';
import 'dart:io' show File, Platform;

import 'package:flutter/material.dart';
import 'package:path/path.dart' as p;

import 'core/models.dart';
import 'core/nekkobox_core.dart';
import 'core/nekkobox_ffi.dart';

void main() {
  runApp(const NekkoboxApp());
}

class NekkoboxApp extends StatelessWidget {
  const NekkoboxApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'Nekkobox',
      theme: ThemeData(
        colorScheme: ColorScheme.fromSeed(
          seedColor: const Color(0xFF6B7CFF),
          brightness: Brightness.dark,
        ),
        useMaterial3: true,
      ),
      home: const SmokeTestScreen(),
    );
  }
}

class SmokeTestScreen extends StatefulWidget {
  const SmokeTestScreen({super.key});
  @override
  State<SmokeTestScreen> createState() => _SmokeTestScreenState();
}

class _SmokeTestScreenState extends State<SmokeTestScreen> {
  NekkoboxCore? _core;
  Timer? _poll;
  HealthInfo? _health;
  String? _initError;

  @override
  void initState() {
    super.initState();
    _bootstrap();
  }

  void _bootstrap() {
    try {
      final core = NekkoboxCoreFfi.open();
      core.init(_baseDir());
      _core = core;
      _poll = Timer.periodic(const Duration(seconds: 1), (_) => _refresh());
      _refresh();
    } catch (e) {
      setState(() => _initError = '$e');
    }
  }

  /// Base directory for the engine — folder containing the running
  /// executable. sing-box.exe, wintun.dll, config.json, profiles.json,
  /// nekkobox.dll are all expected to live here side-by-side.
  String _baseDir() => File(Platform.resolvedExecutable).parent.path;

  void _refresh() {
    final core = _core;
    if (core == null) return;
    try {
      final h = core.health();
      if (mounted) setState(() => _health = h);
    } catch (e) {
      if (mounted) setState(() => _initError = '$e');
    }
  }

  Future<void> _toggle() async {
    final core = _core;
    if (core == null) return;
    try {
      if (core.isRunning()) {
        core.stopVpn();
      } else {
        core.startVpn();
      }
      _refresh();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('$e')));
      }
    }
  }

  @override
  void dispose() {
    _poll?.cancel();
    _core?.shutdown();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    if (_initError != null) {
      return Scaffold(
        appBar: AppBar(title: const Text('Nekkobox — FFI error')),
        body: Padding(
          padding: const EdgeInsets.all(24),
          child: SelectableText(
            _initError!,
            style: const TextStyle(fontFamily: 'monospace'),
          ),
        ),
      );
    }

    final h = _health;
    final running = h?.running ?? false;

    return Scaffold(
      appBar: AppBar(title: const Text('Nekkobox — smoke test')),
      body: Center(
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            _StatusBadge(status: h?.status ?? '...'),
            const SizedBox(height: 16),
            Text(
              h == null ? 'connecting...' : (h.ip.isEmpty ? '—' : h.ip),
              style: Theme.of(context).textTheme.headlineSmall,
            ),
            const SizedBox(height: 8),
            if (h != null && h.message.isNotEmpty)
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 24),
                child: Text(
                  h.message,
                  textAlign: TextAlign.center,
                  style: TextStyle(color: Colors.grey[400]),
                ),
              ),
            const SizedBox(height: 32),
            FilledButton.icon(
              onPressed: _core == null ? null : _toggle,
              icon: Icon(running ? Icons.stop : Icons.play_arrow),
              label: Text(running ? 'Stop VPN' : 'Start VPN'),
              style: FilledButton.styleFrom(
                padding:
                    const EdgeInsets.symmetric(horizontal: 32, vertical: 16),
                textStyle: const TextStyle(fontSize: 18),
              ),
            ),
            const SizedBox(height: 32),
            Text(
              'base: ${p.basename(_baseDir())}',
              style: TextStyle(color: Colors.grey[600], fontSize: 12),
            ),
          ],
        ),
      ),
    );
  }
}

class _StatusBadge extends StatelessWidget {
  final String status;
  const _StatusBadge({required this.status});

  Color get _color {
    switch (status) {
      case 'ok':
        return Colors.green;
      case 'connecting':
        return Colors.amber;
      case 'broken':
        return Colors.red;
      default:
        return Colors.grey;
    }
  }

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 6),
      decoration: BoxDecoration(
        color: _color.withValues(alpha: 0.18),
        border: Border.all(color: _color, width: 1.5),
        borderRadius: BorderRadius.circular(20),
      ),
      child: Text(
        status.toUpperCase(),
        style: TextStyle(color: _color, fontWeight: FontWeight.bold),
      ),
    );
  }
}
