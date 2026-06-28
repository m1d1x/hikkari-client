import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../core/models.dart';
import '../state/app_state.dart';
import '../state/uptime_stats.dart';
import '../theme/tokens.dart';
import 'profiles_sheet.dart';
import 'sparkline.dart';

/// Compact card representing the currently active profile. Tap anywhere on
/// the card (or on the chevron) to open the full [ProfilesSheet] with all
/// profiles. Shows: label, endpoint, avg RTT, sparkline. If there is no
/// active profile yet (fresh install / all dead), still acts as the entry
/// point and labels itself "выбрать профиль".
class CurrentProfileCard extends StatelessWidget {
  const CurrentProfileCard({super.key});

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
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
    final rtt = UptimeStats.avgRtt(hist);
    final lineColor = active != null ? t.ok : t.textMuted;

    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 10, 16, 6),
      child: Material(
        color: t.surface,
        borderRadius: BorderRadius.circular(t.radiusLg),
        clipBehavior: Clip.antiAlias,
        child: InkWell(
          onTap: s.busy ? null : () => ProfilesSheet.show(context),
          mouseCursor: s.busy
              ? SystemMouseCursors.basic
              : SystemMouseCursors.click,
          child: Container(
            decoration: BoxDecoration(
              border: Border.all(color: t.border),
              borderRadius: BorderRadius.circular(t.radiusLg),
            ),
            padding: const EdgeInsets.fromLTRB(14, 12, 10, 12),
            child: Row(
              children: [
                _activeDot(t, active != null),
                const SizedBox(width: 10),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Text(
                        active?.label ?? 'выбрать профиль',
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: TextStyle(
                          color: t.text,
                          fontSize: 14,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                      const SizedBox(height: 2),
                      Text(
                        active == null || active.endpoint.isEmpty
                            ? '${profiles.length} доступно'
                            : active.endpoint,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: TextStyle(
                          color: t.textMuted,
                          fontSize: 11,
                          fontFamily: t.monoFamily,
                        ),
                      ),
                    ],
                  ),
                ),
                const SizedBox(width: 12),
                SizedBox(
                  width: 70,
                  height: 26,
                  child: Sparkline(samples: hist, color: lineColor),
                ),
                const SizedBox(width: 10),
                SizedBox(
                  width: 52,
                  child: Text.rich(
                    TextSpan(
                      text: rtt < 0 ? '—' : '$rtt',
                      children: rtt < 0
                          ? null
                          : [
                              TextSpan(
                                text: ' ms',
                                style: TextStyle(
                                  color: t.textMuted.withValues(alpha: 0.55),
                                ),
                              ),
                            ],
                    ),
                    textAlign: TextAlign.right,
                    style: TextStyle(
                      color: t.textMuted,
                      fontFamily: t.monoFamily,
                      fontSize: 11,
                      fontWeight: FontWeight.w500,
                      height: 1,
                    ),
                  ),
                ),
                const SizedBox(width: 6),
                Icon(Icons.unfold_more, size: 18, color: t.textMuted),
              ],
            ),
          ),
        ),
      ),
    );
  }

  Widget _activeDot(NekkoTokens t, bool active) => AnimatedContainer(
        duration: const Duration(milliseconds: 200),
        width: 10,
        height: 10,
        decoration: BoxDecoration(
          color: active ? t.ok : Colors.transparent,
          shape: BoxShape.circle,
          border: Border.all(
            color: active ? Colors.transparent : t.border,
            width: 1.2,
          ),
        ),
      );
}
