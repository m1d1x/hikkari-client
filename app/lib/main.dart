// App entry: loads the FFI bridge, boots AppState, applies the theme,
// hands off to HomeScreen. Everything else is composed from screens/ and
// widgets/.

import 'dart:io' show Directory, File, Platform;
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:window_manager/window_manager.dart';

import 'core/nekkobox_core.dart';
import 'core/nekkobox_ffi.dart';
import 'screens/home_screen.dart';
import 'screens/mandatory_update_screen.dart';
import 'state/app_state.dart';
import 'theme/app_theme.dart';
import 'theme/tokens.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  if (Platform.isWindows || Platform.isLinux || Platform.isMacOS) {
    await windowManager.ensureInitialized();
    const opts = WindowOptions(
      titleBarStyle: TitleBarStyle.hidden,
      windowButtonVisibility: false,
      backgroundColor: Color(0xFF0E0E10),
    );
    await windowManager.waitUntilReadyToShow(opts, () async {
      await windowManager.show();
      await windowManager.focus();
    });
  }
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

class _BootGateState extends State<_BootGate>
    with WidgetsBindingObserver, WindowListener {
  NekkoboxCore? _core;
  AppState? _state;
  String? _bootError;
  bool _shutDown = false;

  static bool get _desktop =>
      Platform.isWindows || Platform.isLinux || Platform.isMacOS;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    if (_desktop) {
      // Closing the window must go through [onWindowClose]: on Windows the
      // core lives in HikkariService, not in this process, so nothing stops
      // the tunnel when we exit unless we do it ourselves — and State.dispose
      // is not guaranteed to run when the window is torn down.
      windowManager.addListener(this);
      windowManager.setPreventClose(true);
    }
    _boot();
  }

  @override
  Future<void> onWindowClose() async {
    _shutdown();
    await windowManager.destroy();
    exit(0);
  }

  /// Stops the engine (and with it the tunnel) exactly once.
  void _shutdown() {
    if (_shutDown) return;
    _shutDown = true;
    // AppState's own dispose cancels the health timer.
    _state?.dispose();
    _core?.shutdown();
  }

  Future<void> _boot() async {
    try {
      final core = NekkoboxCoreFfi.open();
      final baseDir = _baseDir();
      final selection = File('$baseDir/engine-selection.json');
      var engineId = const String.fromEnvironment('HIKKARI_ENGINE');
      if (await selection.exists()) {
        try {
          final value =
              jsonDecode(await selection.readAsString())
                  as Map<String, dynamic>;
          final saved = value['engineId'];
          if (saved == 'sing-box' || saved == 'xray') {
            engineId = saved as String;
          }
        } catch (_) {
          // Invalid preference is ignored; the selected default remains explicit.
        }
      }
      if (engineId != 'xray' || !xrayAvailable) engineId = 'sing-box';
      core.init(baseDir, engineId: engineId, routingPreview: kRoutingDryRun);
      final state = AppState(
        core,
        baseDir: baseDir,
        routingPreview: kRoutingDryRun,
        engineId: engineId,
      )..start();
      setState(() {
        _core = core;
        _state = state;
      });
    } catch (e) {
      setState(() => _bootError = '$e');
    }
  }

  /// Base directory for the engine — everything it reads and writes
  /// (config.json, profiles.json, logs, caches) lives here.
  ///
  /// Never the executable's folder: on Windows that is Program Files, which
  /// an unelevated GUI cannot write (the core runs in HikkariService, which
  /// gets the config over its pipe), and on macOS a signed, read-only .app
  /// bundle. Both use the per-user location instead. On Windows the
  /// installer (HikkariService.exe install) migrates a portable install's
  /// files into it.
  String _baseDir() {
    // Isolate real-engine desktop checks from the user's saved profiles.
    const override = String.fromEnvironment('NEKKOBOX_DATA_DIR');
    if (override.isNotEmpty) {
      final dir = Directory(override)..createSync(recursive: true);
      return dir.path;
    }
    final Directory dir;
    if (Platform.isMacOS) {
      final home = Platform.environment['HOME'] ?? '';
      dir = Directory('$home/Library/Application Support/nekkobox');
    } else if (Platform.isWindows) {
      final local = Platform.environment['LOCALAPPDATA'];
      if (local == null || local.isEmpty) {
        throw StateError('LOCALAPPDATA is not set');
      }
      dir = Directory('$local\\Hikkari');
    } else {
      return File(Platform.resolvedExecutable).parent.path;
    }
    dir.createSync(recursive: true);
    return dir.path;
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) _state?.onAppResumed();
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    if (_desktop) windowManager.removeListener(this);
    _shutdown();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    if (_bootError != null) return _BootError(message: _bootError!);
    final s = _state;
    if (s == null) return const _BootSplash();
    return ChangeNotifierProvider<AppState>.value(
      value: s,
      child: const _Root(),
    );
  }
}

/// The app, or — while the installed version is below the channel's
/// minimum — the mandatory-update screen in its place.
class _Root extends StatelessWidget {
  const _Root();

  @override
  Widget build(BuildContext context) {
    final blocked = context.select<AppState, bool>((s) => s.updateStatus.blocks);
    return blocked ? const MandatoryUpdateScreen() : const HomeScreen();
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
            Text(
              'FFI init failed',
              style: TextStyle(
                color: t.text,
                fontSize: 18,
                fontWeight: FontWeight.w600,
              ),
            ),
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
