import 'dart:io' show Platform;

import 'package:flutter/material.dart';
import 'package:window_manager/window_manager.dart';

import '../core/deep_links.dart';

import '../theme/tokens.dart';
import '../widgets/brand_header.dart';
import '../widgets/import_profile_dialog.dart';
import '../widgets/log_overlay.dart';
import '../widgets/power_toggle.dart';
import '../widgets/route_section.dart';
import '../widgets/window_titlebar.dart';

class HomeScreen extends StatefulWidget {
  const HomeScreen({super.key});

  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

class _HomeScreenState extends State<HomeScreen> {
  bool _logOpen = false;

  @override
  void initState() {
    super.initState();
    DeepLinks.listen(_onLink);
  }

  /// A `hikkari://` link opened the app: bring the window up and ask the user
  /// to confirm what the link hands over.
  Future<void> _onLink(DeepLink link) async {
    if (!mounted) return;
    if (Platform.isMacOS || Platform.isWindows || Platform.isLinux) {
      await windowManager.show();
      await windowManager.focus();
    }
    if (!mounted) return;
    switch (link) {
      case ImportLink(:final link):
        await ImportProfileDialog.show(context, initialLink: link);
    }
  }

  void _openLog() => setState(() => _logOpen = true);
  void _closeLog() => setState(() => _logOpen = false);

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return Scaffold(
      backgroundColor: t.bg,
      body: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          const WindowTitlebar(),
          BrandHeader(onOpenLogs: _openLog),
          if (_logOpen) LogOverlay(onClose: _closeLog),
          const Expanded(
            child: CustomScrollView(
              // Keep the bottom edge anchored as routing grows past the
              // viewport. Its header must keep moving when flex space runs out.
              reverse: true,
              slivers: [
                SliverFillRemaining(
                  hasScrollBody: false,
                  child: Column(
                    children: [
                      Expanded(
                        child: Align(
                          alignment: Alignment(0, -0.22),
                          child: PowerToggle(),
                        ),
                      ),
                      SafeArea(
                        top: false,
                        child: Padding(
                          padding: EdgeInsets.only(bottom: 16),
                          child: RouteSection(),
                        ),
                      ),
                    ],
                  ),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}
