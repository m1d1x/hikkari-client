import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../core/models.dart';
import '../state/app_state.dart';
import 'profile_summary_card.dart';
import 'profiles_sheet.dart';

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

    return ProfileSummaryCard(
      title: active?.label ?? 'выбрать профиль',
      samples: hist,
      selected: active != null,
      onTap: s.busy ? null : () => ProfilesSheet.show(context),
      margin: const EdgeInsets.fromLTRB(16, 10, 16, 6),
      padding: const EdgeInsets.fromLTRB(16, 14, 12, 14),
      titleFontSize: 15,
      titleWeight: FontWeight.w700,
      chartHeight: 34,
      trailing: const Icon(Icons.unfold_more, size: 18),
    );
  }
}
