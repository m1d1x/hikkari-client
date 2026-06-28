import 'package:flutter/material.dart';

import '../theme/tokens.dart';
import '../widgets/brand_header.dart';
import '../widgets/power_toggle.dart';

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

  void _toggleLog() => setState(() => _logOpen = !_logOpen);

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
              BrandHeader(onTapIp: _toggleLog),
              if (_logOpen) _LogPlaceholder(onClose: _toggleLog),
              const PowerToggle(),
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

/// Stand-in until the real log overlay widget lands. Keeps the toggle
/// behaviour visible so we don't lose the "tap IP → open log" UX from the
/// Svelte version while we're mid-port.
class _LogPlaceholder extends StatelessWidget {
  final VoidCallback onClose;
  const _LogPlaceholder({required this.onClose});

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return Container(
      margin: const EdgeInsets.symmetric(horizontal: 16),
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: t.surface,
        border: Border.all(color: t.border),
        borderRadius: BorderRadius.circular(t.radius),
      ),
      child: Row(
        children: [
          Text('журнал (placeholder)', style: TextStyle(color: t.textMuted)),
          const Spacer(),
          IconButton(
            icon: const Icon(Icons.close, size: 18),
            onPressed: onClose,
            color: t.textMuted,
          ),
        ],
      ),
    );
  }
}
