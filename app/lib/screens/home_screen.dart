import 'package:flutter/material.dart';

import '../theme/tokens.dart';
import '../widgets/apply_bar.dart';
import '../widgets/brand_header.dart';
import '../widgets/current_profile_card.dart';
import '../widgets/log_overlay.dart';
import '../widgets/power_toggle.dart';
import '../widgets/route_section.dart';

/// The single-page home of the app. Stacks the same vertical sections the
/// Svelte UI uses; each section is its own widget so refactors are cheap.
/// Profiles, whitelist, and log overlay land in follow-up commits.
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
      body: SafeArea(
        child: SingleChildScrollView(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              BrandHeader(onOpenLogs: _openLog),
              if (_logOpen) LogOverlay(onClose: _closeLog),
              const PowerToggle(),
              const CurrentProfileCard(),
              const RouteSection(),
              const ApplyBar(),
              // Profiles, route/whitelist, items, apply-bar all land here
              // in follow-up tasks. Layout is intentionally bare for now —
              // it has to look right with just header + power before we
              // start sandwiching more sections in.
            ],
          ),
        ),
      ),
    );
  }
}
