import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../core/models.dart';
import '../state/app_state.dart';
import '../state/uptime_stats.dart';
import '../theme/tokens.dart';
import '../ui/nekko_error_card.dart';
import 'add_profile_dialog.dart';
import 'morphing_word.dart';
import 'power_ring.dart';
import 'profile_flag_badge.dart';
import 'profiles_sheet.dart';
import 'service_ping_sheet.dart';
import 'sparkline.dart';
import 'vpn_toggle.dart';

class PowerToggle extends StatefulWidget {
  const PowerToggle({super.key});

  @override
  State<PowerToggle> createState() => _PowerToggleState();
}

class _PowerToggleState extends State<PowerToggle> {
  bool _btnHover = false;
  bool _profileHover = false;

  // The status word shown in place of the profile row: "подключаем" while a
  // press is in flight, "подключено" for a moment after it lands, then gone.
  VpnTransition _seenTransition = VpnTransition.none;
  String? _status;
  // The last word shown, so a leaving word keeps its colour.
  String _lastStatus = '';
  Timer? _statusTimer;

  static const _connectedHold = Duration(milliseconds: 2400);
  static const _disconnectedHold = Duration(milliseconds: 1400);

  @override
  void dispose() {
    _statusTimer?.cancel();
    super.dispose();
  }

  /// Follows the press through its phases. Runs during build: it only sets
  /// fields that this same build reads, and the hold timer rebuilds later.
  void _trackStatus(VpnTransition now, bool running) {
    if (now == _seenTransition) return;
    final was = _seenTransition;
    _seenTransition = now;
    _statusTimer?.cancel();
    switch (now) {
      case VpnTransition.connecting:
        _status = 'подключаем';
      case VpnTransition.disconnecting:
        _status = 'отключаем';
      case VpnTransition.none:
        final landed =
            (was == VpnTransition.connecting && running) ||
            (was == VpnTransition.disconnecting && !running);
        if (!landed) {
          // The start failed: the error card speaks instead.
          _status = null;
          return;
        }
        _status = running ? 'подключено' : 'отключено';
        _statusTimer = Timer(running ? _connectedHold : _disconnectedHold, () {
          if (mounted) setState(() => _status = null);
        });
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final s = context.watch<AppState>();
    final running = s.isRunning;
    final busy = s.busy;
    final err = s.lastError;
    final transition = s.vpnTransition;
    _trackStatus(transition, running);
    final phase = switch (transition) {
      VpnTransition.connecting => PowerRingPhase.connecting,
      VpnTransition.disconnecting => PowerRingPhase.disconnecting,
      VpnTransition.none => running ? PowerRingPhase.on : PowerRingPhase.off,
    };
    final status = _status;
    if (status != null) _lastStatus = status;
    final statusMuted = _lastStatus.startsWith('отключ');

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

    final noProfiles = s.profiles.isEmpty;
    final label =
        active?.displayLabel ??
        (noProfiles ? 'добавить профиль' : 'выбрать профиль');
    final protocol = active == null ? '' : protocolCaption(active.protocol);
    final countryCode = active == null ? null : s.countryCodeFor(active.name);
    final countryLoading = active != null && s.isCountryLoading(active.name);

    final rttText = dead
        ? 'down'
        : checking
        ? '…'
        : '$rtt ms';

    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        // Power button
        MouseRegion(
          onEnter: (_) {
            if (!busy) setState(() => _btnHover = true);
          },
          onExit: (_) => setState(() => _btnHover = false),
          child: PowerRing(
            phase: phase,
            size: 176,
            accent: t.primary,
            idleBorder: t.border,
            idleIcon: t.textMuted,
            hover: _btnHover,
            child: SizedBox(
              width: 176,
              height: 176,
              child: Material(
                color: Colors.transparent,
                shape: const CircleBorder(),
                clipBehavior: Clip.antiAlias,
                child: InkWell(
                  onTap: busy
                      ? null
                      : noProfiles
                      ? () => AddProfileDialog.show(context)
                      : () => toggleVpnWithNotice(context, s),
                  onSecondaryTap: kDebugMode
                      ? () => s.simulateEngineFailure()
                      : null,
                  onLongPress: kDebugMode
                      ? () => s.simulateEngineFailure()
                      : null,
                  mouseCursor: busy
                      ? SystemMouseCursors.basic
                      : SystemMouseCursors.click,
                  // The mark is painted by [PowerRing] underneath.
                  child: const SizedBox.expand(),
                ),
              ),
            ),
          ),
        ),

        SizedBox(height: err != null ? 8 : 18),

        // Profile block — one large tappable zone (vertically padded, easy to
        // hit with a thumb). [InkWell] supplies the pointer cursor and hover
        // state, so no separate MouseRegion is needed.
        //
        // Both rows are [MainAxisSize.min] and the [Column] centres them: the
        // chevron just lives at the end of the HERO row (so it's part of what
        // gets centred), and the flag+IP row centres on its own below. No fake
        // gutters — everything is honestly centred by its own content width.
        Stack(
          alignment: Alignment.center,
          children: [
            AnimatedOpacity(
              opacity: status == null ? 1 : 0,
              duration: const Duration(milliseconds: 900),
              // Back in only after the word has sunk away; out at once.
              curve: status == null
                  ? const Interval(0.6, 1, curve: Curves.easeOut)
                  : const Interval(0, 0.3, curve: Curves.easeIn),
              child: _profileBlock(
                context,
                t: t,
                s: s,
                busy: busy,
                noProfiles: noProfiles,
                active: active,
                label: label,
                hist: hist,
                running: running,
                dead: dead,
                rttText: rttText,
                protocol: protocol,
                countryCode: countryCode,
                countryLoading: countryLoading,
                err: err,
              ),
            ),
            IgnorePointer(
              child: MorphingWord(
                text: status,
                // Each pair is one length ("подключаем"/"подключено",
                // "отключаем"/"отключено"), so the word stays centred
                // and only its ending changes.
                reserve: statusMuted ? 'отключено' : 'подключено',
                style: TextStyle(
                  color: statusMuted ? t.textMuted : t.primary,
                  fontFamily: t.monoFamily,
                  fontSize: 17,
                  fontWeight: FontWeight.w700,
                  letterSpacing: 1.2,
                ),
              ),
            ),
          ],
        ),

        if (err != null) ...[
          const SizedBox(height: 8),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 24),
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 360),
              child: NekkoErrorCard(
                title: 'Не удалось подключиться',
                summary: NekkoErrorCard.summarize(err),
                details: err,
                maxLines: 2,
                tokens: t,
              ),
            ),
          ),
        ],
      ],
    );
  }

  Widget _profileBlock(
    BuildContext context, {
    required NekkoTokens t,
    required AppState s,
    required bool busy,
    required bool noProfiles,
    required ProfileInfo? active,
    required String label,
    required List<PingSample> hist,
    required bool running,
    required bool dead,
    required String rttText,
    required String protocol,
    required String? countryCode,
    required bool countryLoading,
    required String? err,
  }) {
    return Material(
      color: Colors.transparent,
      borderRadius: BorderRadius.circular(t.radiusLg),
      clipBehavior: Clip.antiAlias,
      child: InkWell(
        onTap: busy
            ? null
            : noProfiles
            ? () => AddProfileDialog.show(context)
            : () => ProfilesSheet.show(context),
        onSecondaryTap: active == null
            ? null
            : () => ServicePingSheet.show(context, label),
        onHover: busy ? null : (h) => setState(() => _profileHover = h),
        borderRadius: BorderRadius.circular(t.radiusLg),
        hoverColor: t.primary.withValues(alpha: 0.05),
        mouseCursor: busy ? SystemMouseCursors.basic : SystemMouseCursors.click,
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
                    tween: ColorTween(end: _profileHover ? t.primary : t.text),
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
                  if (active != null) ...[
                    const SizedBox(width: 11),
                    SizedBox(
                      width: 50,
                      height: 17,
                      child: Sparkline(
                        samples: hist,
                        placeholderSeed: label,
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
                      noProfiles ? Icons.add : Icons.keyboard_arrow_down,
                      size: 18,
                      color: color,
                    ),
                  ),
                ],
              ),

              // Info row — the server's country flag and the protocol. The
              // address (often the camouflage domain) is not shown.
              if (active != null &&
                  (protocol.isNotEmpty ||
                      countryCode != null ||
                      countryLoading)) ...[
                SizedBox(height: err != null ? 4 : 8),
                Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    if (countryCode != null || countryLoading)
                      ProfileFlagBadge(
                        countryCode: countryCode,
                        loading: countryLoading,
                        width: 16,
                        height: 12,
                      ),
                    if ((countryCode != null || countryLoading) &&
                        protocol.isNotEmpty)
                      const SizedBox(width: 8),
                    if (protocol.isNotEmpty)
                      Text(
                        protocol,
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
    );
  }
}
