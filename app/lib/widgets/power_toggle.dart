import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../core/models.dart';
import '../state/app_state.dart';
import '../state/uptime_stats.dart';
import '../theme/tokens.dart';
import 'profiles_sheet.dart';
import 'service_ping_sheet.dart';
import 'sparkline.dart';

class PowerToggle extends StatelessWidget {
  const PowerToggle({super.key});

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final s = context.watch<AppState>();
    final running = s.isRunning;
    final busy = s.busy;
    final err = s.lastError;

    ProfileInfo? active;
    for (final p in s.profiles) {
      if (p.active) {
        active = p;
        break;
      }
    }
    final hist = active == null
        ? const <PingSample>[]
        : (s.uptime[active.name] ?? const <PingSample>[]);
    final rtt = UptimeStats.avgRtt(hist);
    final checking = s.isWarmingUp || hist.length < 3;
    final recent = UptimeStats.recentAvgRtt(hist);
    final dead = !checking && (recent < 0 || recent > 1000);

    final label = active?.label.toLowerCase() ?? 'выбрать профиль';

    final rttText = dead
        ? 'down'
        : checking
            ? '…'
            : '$rtt ms';

    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        // Power button
        SizedBox(
          width: 168,
          height: 168,
          child: Material(
            color: Colors.transparent,
            shape: const CircleBorder(),
            clipBehavior: Clip.antiAlias,
            child: InkWell(
              onTap: busy ? null : () => s.toggleVpn(),
              mouseCursor: busy
                  ? SystemMouseCursors.basic
                  : SystemMouseCursors.click,
              child: AnimatedContainer(
                duration: const Duration(milliseconds: 300),
                curve: Curves.easeOut,
                decoration: BoxDecoration(
                  shape: BoxShape.circle,
                  color: Colors.transparent,
                  border: Border.all(
                    color: running
                        ? t.primary.withValues(alpha: 0.55)
                        : t.border,
                    width: running ? 3 : 1.5,
                  ),
                ),
                child: Center(
                  child: Icon(
                    Icons.power_settings_new,
                    size: 64,
                    color: running ? t.primary : t.textMuted,
                  ),
                ),
              ),
            ),
          ),
        ),

        const SizedBox(height: 20),

        // Profile subtitle — tappable
        GestureDetector(
          onTap: busy ? null : () => ProfilesSheet.show(context),
          onSecondaryTap: active == null
              ? null
              : () => ServicePingSheet.show(context, label),
          child: MouseRegion(
            cursor: busy
                ? SystemMouseCursors.basic
                : SystemMouseCursors.click,
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(
                  label,
                  style: TextStyle(
                    color: t.text,
                    fontFamily: t.monoFamily,
                    fontSize: 14,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                if (active != null && hist.length >= 2) ...[
                  const SizedBox(width: 10),
                  SizedBox(
                    width: 48,
                    height: 16,
                    child: Sparkline(
                      samples: hist,
                      color: running
                          ? t.primary
                          : t.textMuted.withValues(alpha: 0.4),
                    ),
                  ),
                ],
                if (active != null && !dead) ...[
                  const SizedBox(width: 8),
                  Text(
                    rttText,
                    style: TextStyle(
                      color: t.textMuted,
                      fontFamily: t.monoFamily,
                      fontSize: 13,
                      fontWeight: FontWeight.w500,
                    ),
                  ),
                ],
                const SizedBox(width: 4),
                Icon(
                  Icons.keyboard_arrow_down,
                  size: 16,
                  color: t.textFaint,
                ),
              ],
            ),
          ),
        ),

        if (err != null) ...[
          const SizedBox(height: 16),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 24),
            child: SelectableText(
              err,
              textAlign: TextAlign.center,
              style: TextStyle(
                color: t.bad,
                fontFamily: t.monoFamily,
                fontSize: 11,
                height: 1.4,
              ),
            ),
          ),
        ],
      ],
    );
  }
}
