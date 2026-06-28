// App entry: loads the FFI bridge, boots AppState, applies the theme,
// hands off to HomeScreen. Everything else is composed from screens/ and
// widgets/.

import 'dart:io' show File, Platform;

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import 'core/nekkobox_core.dart';
import 'core/nekkobox_ffi.dart';
import 'screens/home_screen.dart';
import 'state/app_state.dart';
import 'theme/app_theme.dart';
import 'theme/tokens.dart';

void main() {
  runApp(const NekkoboxApp());
}

class NekkoboxApp extends StatelessWidget {
  const NekkoboxApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'Nekko VPN',
      debugShowCheckedModeBanner: false,
      theme: buildAppTheme(),
      home: const _BootGate(),
    );
  }
}

/// Loads the DLL, initialises the engine, then mounts the real UI under a
/// Provider. If FFI init fails we render an error screen instead of the app
/// — clearer than a black window plus stderr-only stack trace.
class _BootGate extends StatefulWidget {
  const _BootGate();

  @override
  State<_BootGate> createState() => _BootGateState();
}

class _BootGateState extends State<_BootGate> {
  NekkoboxCore? _core;
  AppState? _state;
  String? _bootError;

  @override
  void initState() {
    super.initState();
    _boot();
  }

  void _boot() {
    try {
      final core = NekkoboxCoreFfi.open();
      core.init(_baseDir());
      final state = AppState(core)..start();
      setState(() {
        _core = core;
        _state = state;
      });
    } catch (e) {
      setState(() => _bootError = '$e');
    }
  }

  /// Base directory for the engine — folder containing the running
  /// executable. sing-box.exe, wintun.dll, config.json, profiles.json,
  /// nekkobox.dll all live here.
  String _baseDir() => File(Platform.resolvedExecutable).parent.path;

  @override
  void dispose() {
    // AppState's own dispose cancels the health timer.
    _state?.dispose();
    _core?.shutdown();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    if (_bootError != null) return _BootError(message: _bootError!);
    final s = _state;
    if (s == null) return const _BootSplash();
    return ChangeNotifierProvider<AppState>.value(
      value: s,
      child: const HomeScreen(),
    );
  }
}

class _BootSplash extends StatelessWidget {
  const _BootSplash();
  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: context.tokens.bg,
      body: const Center(child: CircularProgressIndicator()),
    );
  }
}

class _BootError extends StatelessWidget {
  final String message;
  const _BootError({required this.message});

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return Scaffold(
      backgroundColor: t.bg,
      body: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            Icon(Icons.error_outline, color: t.bad, size: 48),
            const SizedBox(height: 12),
            Text('FFI init failed',
                style: TextStyle(
                    color: t.text, fontSize: 18, fontWeight: FontWeight.w600)),
            const SizedBox(height: 12),
            Container(
              padding: const EdgeInsets.all(12),
              decoration: BoxDecoration(
                color: t.surface,
                border: Border.all(color: t.border),
                borderRadius: BorderRadius.circular(t.radius),
              ),
              child: SelectableText(
                message,
                style: TextStyle(
                  fontFamily: t.monoFamily,
                  color: t.textMuted,
                  fontSize: 12,
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}
