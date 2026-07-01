import 'package:flutter/material.dart';

import '../theme/tokens.dart';
import '../widgets/brand_header.dart';
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
            child: Center(child: PowerToggle()),
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
