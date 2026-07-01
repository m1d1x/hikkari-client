import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../core/models.dart';
import '../state/app_state.dart';
import '../theme/tokens.dart';
import 'profile_summary_card.dart';
import 'profiles_sheet.dart';
import 'service_ping_sheet.dart';

/// Compact card representing the currently active profile. Tap anywhere on
/// the card (or on the chevron) to open the full [ProfilesSheet] with all
/// profiles. Shows: label, avg RTT, sparkline. If there is no
/// active profile yet (fresh install / all dead), still acts as the entry
/// point and labels itself "выбрать профиль".
class CurrentProfileCard extends StatelessWidget {
  const CurrentProfileCard({super.key});

  @override
  Widget build(BuildContext context) {
    final s = context.watch<AppState>();
    final t = context.tokens;
    final profiles = s.profiles;
    if (profiles.isEmpty) return const SizedBox.shrink();

    ProfileInfo? active;
    for (final p in profiles) {
      if (p.active) {
        active = p;
        break;
      }
    }

    final hist = active == null
        ? const <PingSample>[]
        : (s.uptime[active.name] ?? const <PingSample>[]);

    final label = active?.label ?? 'выбрать профиль';
    return GestureDetector(
      onSecondaryTap: active == null
          ? null
          : () => ServicePingSheet.show(context, label),
      child: ProfileSummaryCard(
        title: label.toLowerCase(),
        samples: hist,
        selected: active != null,
        forceChecking: s.isWarmingUp,
        onTap: s.busy ? null : () => ProfilesSheet.show(context),
        margin: const EdgeInsets.fromLTRB(16, 10, 16, 6),
        // +3px vs. the routing card's 16px header padding — matches the
        // optical nudge on "маршрутизация" ([RouteSection._head]) so the
        // two stacked cards' text starts flush on the same left edge.
        padding: const EdgeInsets.fromLTRB(19, 17, 12, 17),
        titleFontSize: 14,
        titleWeight: FontWeight.w700,
        // 28 (not e.g. 34) so the row's tallest child matches the routing
        // card's iOS switch (28px) — keeps the two stacked home-screen
        // cards the same overall height.
        chartHeight: 28,
        trailing: Icon(Icons.keyboard_arrow_down, size: 18, color: t.textMuted),
      ),
    );
  }
}
