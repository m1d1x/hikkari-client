import 'package:flutter/material.dart';

import '../theme/tokens.dart';
import '../widgets/brand_header.dart';
import '../widgets/log_overlay.dart';
import '../widgets/power_toggle.dart';
import '../widgets/route_section.dart';
import '../widgets/update_banner.dart';
import '../widgets/window_titlebar.dart';

class HomeScreen extends StatefulWidget {
  const HomeScreen({super.key});

  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

class _HomeScreenState extends State<HomeScreen> {
  bool _logOpen = false;

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
          const UpdateBanner(),
          if (_logOpen) LogOverlay(onClose: _closeLog),
          const Expanded(
            // Nudge the button + profile block up a touch from dead-centre so
            // it sits higher in the empty space above the routing bar.
            child: Align(
              alignment: Alignment(0, -0.22),
              child: PowerToggle(),
            ),
          ),
          const SafeArea(
            top: false,
            child: Padding(
              padding: EdgeInsets.only(bottom: 16),
              child: RouteSection(),
            ),
          ),
        ],
      ),
    );
  }
}
