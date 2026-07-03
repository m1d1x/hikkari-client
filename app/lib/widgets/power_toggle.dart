import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../core/models.dart';
import '../state/app_state.dart';
import '../state/uptime_stats.dart';
import '../theme/tokens.dart';
import 'profiles_sheet.dart';
import 'service_ping_sheet.dart';
import 'sparkline.dart';

class PowerToggle extends StatefulWidget {
  const PowerToggle({super.key});

  @override
  State<PowerToggle> createState() => _PowerToggleState();
}

class _PowerToggleState extends State<PowerToggle> {
  bool _btnHover = false;
  bool _profileHover = false;

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
          child: MouseRegion(
            onEnter: (_) { if (!busy) setState(() => _btnHover = true); },
            onExit: (_) => setState(() => _btnHover = false),
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
                          ? t.primary.withValues(alpha: _btnHover ? 0.85 : 0.55)
                          : (_btnHover ? t.primary.withValues(alpha: 0.4) : t.border),
                      width: running ? 3 : 1.5,
                    ),
                  ),
                  child: Center(
                    child: TweenAnimationBuilder<Color?>(
                      tween: ColorTween(
                        end: running
                            ? t.primary
                            : (_btnHover ? t.primary.withValues(alpha: 0.7) : t.textMuted),
                      ),
                      duration: const Duration(milliseconds: 200),
                      builder: (_, color, __) => Icon(
                        Icons.power_settings_new,
                        size: 64,
                        color: color,
                      ),
                    ),
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
            onEnter: (_) { if (!busy) setState(() => _profileHover = true); },
            onExit: (_) => setState(() => _profileHover = false),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                TweenAnimationBuilder<Color?>(
                  tween: ColorTween(
                    end: _profileHover ? t.primary : t.text,
                  ),
                  duration: const Duration(milliseconds: 150),
                  builder: (_, color, __) => Text(
                    label,
                    style: TextStyle(
                      color: color,
                      fontFamily: t.monoFamily,
                      fontSize: 14,
                      fontWeight: FontWeight.w600,
                    ),
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
                TweenAnimationBuilder<Color?>(
                  tween: ColorTween(
                    end: _profileHover ? t.primary : t.textFaint,
                  ),
                  duration: const Duration(milliseconds: 150),
                  builder: (_, color, __) => Icon(
                    Icons.keyboard_arrow_down,
                    size: 16,
                    color: color,
                  ),
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
