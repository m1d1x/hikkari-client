import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../core/models.dart';
import '../state/app_state.dart';
import '../state/uptime_stats.dart';
import '../theme/tokens.dart';
import 'profile_flag_badge.dart';
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
    final endpoint = active?.endpoint ?? '';
    final countryCode = active == null ? null : s.countryCodeFor(active.name);
    final countryLoading =
        active != null && s.isCountryLoading(active.name);

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
          width: 176,
          height: 176,
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
                        size: 68,
                        color: color,
                      ),
                    ),
                  ),
                ),
              ),
            ),
          ),
        ),

        const SizedBox(height: 18),

        // Profile block — one large tappable zone (vertically padded, easy to
        // hit with a thumb). [InkWell] supplies the pointer cursor and hover
        // state, so no separate MouseRegion is needed.
        //
        // Both rows are [MainAxisSize.min] and the [Column] centres them: the
        // chevron just lives at the end of the HERO row (so it's part of what
        // gets centred), and the flag+IP row centres on its own below. No fake
        // gutters — everything is honestly centred by its own content width.
        Material(
          color: Colors.transparent,
          borderRadius: BorderRadius.circular(t.radiusLg),
          clipBehavior: Clip.antiAlias,
          child: InkWell(
            onTap: busy ? null : () => ProfilesSheet.show(context),
            onSecondaryTap: active == null
                ? null
                : () => ServicePingSheet.show(context, label),
            onHover: busy
                ? null
                : (h) => setState(() => _profileHover = h),
            borderRadius: BorderRadius.circular(t.radiusLg),
            hoverColor: t.primary.withValues(alpha: 0.05),
            mouseCursor: busy
                ? SystemMouseCursors.basic
                : SystemMouseCursors.click,
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 22, vertical: 14),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  // HERO row — label + sparkline + rtt + chevron.
                  Row(
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
                            fontSize: 16,
                            fontWeight: FontWeight.w700,
                          ),
                        ),
                      ),
                      if (active != null && hist.length >= 2) ...[
                        const SizedBox(width: 11),
                        SizedBox(
                          width: 50,
                          height: 17,
                          child: Sparkline(
                            samples: hist,
                            color: running
                                ? t.primary
                                : t.textMuted.withValues(alpha: 0.4),
                          ),
                        ),
                      ],
                      if (active != null && !dead) ...[
                        const SizedBox(width: 9),
                        Text(
                          rttText,
                          style: TextStyle(
                            color: t.textMuted,
                            fontFamily: t.monoFamily,
                            fontSize: 14,
                            fontWeight: FontWeight.w500,
                          ),
                        ),
                      ],
                      const SizedBox(width: 6),
                      TweenAnimationBuilder<Color?>(
                        tween: ColorTween(
                          end: _profileHover ? t.primary : t.textFaint,
                        ),
                        duration: const Duration(milliseconds: 150),
                        builder: (_, color, __) => Icon(
                          Icons.keyboard_arrow_down,
                          size: 18,
                          color: color,
                        ),
                      ),
                    ],
                  ),

                  // Info row — flag + server IP, grouped together and centred.
                  if (active != null && endpoint.isNotEmpty) ...[
                    const SizedBox(height: 10),
                    Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        ProfileFlagBadge(
                          countryCode: countryCode,
                          loading: countryLoading,
                          width: 16,
                          height: 12,
                        ),
                        const SizedBox(width: 8),
                        Text(
                          endpoint,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(
                            color: t.textMuted.withValues(alpha: 0.55),
                            fontFamily: t.monoFamily,
                            fontSize: 12,
                            fontWeight: FontWeight.w500,
                            letterSpacing: 0.2,
                          ),
                        ),
                      ],
                    ),
                  ],
                ],
              ),
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
