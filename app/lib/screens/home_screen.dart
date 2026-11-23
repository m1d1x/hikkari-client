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
  final _powerArea = GlobalKey();
  final _routing = RouteSectionController();

  // The scroll body grows to its intrinsic height once that exceeds the
  // viewport, so the flexible area above routing can shrink only down to the
  // power control's intrinsic height. The difference is exact free space.
  double _routingSpare() {
    final area = _powerArea.currentContext?.findRenderObject() as RenderBox?;
    if (area == null || !area.hasSize) return 0;
    final spare =
        area.size.height - area.getMaxIntrinsicHeight(area.size.width);
    return spare > 0 ? spare : 0;
  }

  @override
  void initState() {
    super.initState();
    DeepLinks.listen(_onLink);
  }

  /// A `hikkari://` link opened the app: bring the window up and show import
  /// progress without a second confirmation.
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
      // Empty space anywhere on the page closes an open routing card: once a
      // long catalog is scrolled deep, its header is off-screen and this is
      // the only way out. Rows, fields and buttons keep their own taps — an
      // inner gesture wins the arena.
      body: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: _routing.close,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            const WindowTitlebar(),
            BrandHeader(onOpenLogs: _openLog),
            AnimatedSize(
              duration: AppDurations.expand,
              curve: AppCurves.fastInSlowOut,
              alignment: Alignment.topCenter,
              clipBehavior: Clip.hardEdge,
              child: _logOpen
                  ? LogOverlay(onClose: _closeLog)
                  : const SizedBox(width: double.infinity, height: 0),
            ),
            Expanded(
              child: CustomScrollView(
                slivers: [
                  SliverFillRemaining(
                    hasScrollBody: false,
                    child: Column(
                      children: [
                        Expanded(
                          child: Align(
                            key: _powerArea,
                            alignment: const Alignment(0, -0.22),
                            child: const PowerToggle(),
                          ),
                        ),
                        SafeArea(
                          top: false,
                          child: RouteSection(
                            spareHeight: _routingSpare,
                            controller: _routing,
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
      ),
    );
  }
}
