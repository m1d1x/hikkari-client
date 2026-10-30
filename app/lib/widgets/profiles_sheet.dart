import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../core/models.dart';
import '../state/app_state.dart';
import '../state/uptime_stats.dart';
import '../theme/tokens.dart';
import '../ui/nekko_text_field.dart';
import 'import_profile_dialog.dart';
import 'profile_tile.dart';

/// Bottom sheet with the full profile list. Opened from
/// [CurrentProfileCard]; closes itself on switch so the user lands back on
/// the main screen with the new active profile visible in the card.
class ProfilesSheet extends StatefulWidget {
  const ProfilesSheet({super.key});

  static Future<void> show(BuildContext context) {
    final t = context.tokens;
    final state = context.read<AppState>();
    return showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      constraints: const BoxConstraints(maxWidth: 1000),
      backgroundColor: t.surface,
      barrierColor: Colors.black54,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(t.radiusLg)),
      ),
      builder: (_) => ChangeNotifierProvider<AppState>.value(
        value: state,
        child: const ProfilesSheet(),
      ),
    );
  }

  @override
  State<ProfilesSheet> createState() => _ProfilesSheetState();
}

class _ProfilesSheetState extends State<ProfilesSheet> {
  String? _expanded;
  String _query = '';
  String? _lastActive;
  bool _lastActiveInitialized = false;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final s = context.watch<AppState>();

    // Auto-dismiss on successful switch — when active profile changes mid-sheet.
    final active = s.activeProfileName;
    if (!_lastActiveInitialized) {
      _lastActive = active;
      _lastActiveInitialized = true;
    } else if (active != null && active != _lastActive) {
      _lastActive = active;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) Navigator.of(context).maybePop();
      });
    }

    final q = _query.trim().toLowerCase();
    final all = s.profiles;
    final matched = q.isEmpty
        ? List.of(all)
        : all
              .where(
                (p) =>
                    p.label.toLowerCase().contains(q) ||
                    p.name.toLowerCase().contains(q) ||
                    (p.subscription?.title.toLowerCase().contains(q) ?? false),
              )
              .toList();

    // Sort: active profile pinned to top, then alive profiles by latest
    // recent-avg rtt (lowest first), dead/unknown at the bottom. Keeps the
    // list useful when most profiles are degraded — the user always sees
    // workable options first.
    // Sort tiers, lower = higher in the list:
    //   0  active profile
    //   1  alive (recent avg rtt ≤ 500ms)
    //   2  checking (history < 3 samples — too early to judge)
    //   3  dead
    // Inside each tier we sub-sort by recent rtt so the fastest survives
    // at the top.
    (int, int) qualityKey(ProfileInfo p) {
      if (p.active) return (0, 0);
      final hist = s.uptime[p.name] ?? const <PingSample>[];
      if (hist.length < 3) return (2, 0);
      final r = UptimeStats.recentAvgRtt(hist);
      if (r < 0 || r > 500) return (3, r < 0 ? 1 << 30 : r);
      return (1, r);
    }

    matched.sort((a, b) {
      final ka = qualityKey(a);
      final kb = qualityKey(b);
      if (ka.$1 != kb.$1) return ka.$1.compareTo(kb.$1);
      return ka.$2.compareTo(kb.$2);
    });
    final List<Object> entries = [];
    final seenSubscriptions = <String>{};
    for (final profile in matched) {
      final subscription = profile.subscription;
      if (subscription == null || subscription.id.isEmpty) {
        entries.add(profile);
      } else if (seenSubscriptions.add(subscription.id)) {
        entries.add(
          _SubscriptionGroup(
            subscription,
            matched
                .where((p) => p.subscription?.id == subscription.id)
                .toList(),
          ),
        );
      }
    }

    return DraggableScrollableSheet(
      expand: false,
      initialChildSize: 0.9,
      minChildSize: 0.35,
      maxChildSize: 0.95,
      builder: (ctx, scrollCtrl) {
        return Column(
          children: [
            _grabber(t),
            _header(t, all.length),
            if (all.isNotEmpty) ...[_search(t), const SizedBox(height: 10)],
            Expanded(
              child: all.isEmpty
                  ? _emptyState(t)
                  : entries.isEmpty
                  ? Center(
                      child: Text(
                        'ничего не найдено',
                        style: TextStyle(color: t.textMuted, fontSize: 12),
                      ),
                    )
                  : ListView.builder(
                      controller: scrollCtrl,
                      padding: const EdgeInsets.only(bottom: 16),
                      itemCount: entries.length,
                      itemBuilder: (_, i) {
                        final entry = entries[i];
                        if (entry is _SubscriptionGroup) {
                          return _SubscriptionCard(
                            key: ValueKey('subscription-${entry.info.id}'),
                            info: entry.info,
                            profiles: entry.profiles,
                            expandedProfile: _expanded,
                            onToggleProfile: (name) => setState(() {
                              _expanded = _expanded == name ? null : name;
                            }),
                          );
                        }
                        final p = entry as ProfileInfo;
                        return ProfileTile(
                          key: ValueKey(p.name),
                          profile: p,
                          expanded: _expanded == p.name,
                          onToggleExpand: () => setState(() {
                            _expanded = _expanded == p.name ? null : p.name;
                          }),
                        );
                      },
                    ),
            ),
          ],
        );
      },
    );
  }

  Widget _grabber(NekkoTokens t) => Padding(
    padding: const EdgeInsets.only(top: 6, bottom: 2),
    child: Container(
      width: 36,
      height: 4,
      decoration: BoxDecoration(
        color: t.borderStrong,
        borderRadius: BorderRadius.circular(2),
      ),
    ),
  );

  // Пусто = ни одного профиля. Большая понятная кнопка-плашка в стиле
  // активного профиля (тёмная заливка + primary-аутлайн), чтобы сразу
  // было ясно единственное осмысленное действие — импортировать профиль.
  Widget _emptyState(NekkoTokens t) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 4, 16, 24),
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Material(
            color: const Color(0xFF1A1014),
            borderRadius: BorderRadius.circular(t.radiusLg),
            clipBehavior: Clip.antiAlias,
            child: InkWell(
              onTap: () => ImportProfileDialog.show(context),
              mouseCursor: SystemMouseCursors.click,
              borderRadius: BorderRadius.circular(t.radiusLg),
              hoverColor: t.primary.withValues(alpha: 0.06),
              child: Container(
                width: double.infinity,
                padding: const EdgeInsets.symmetric(
                  horizontal: 20,
                  vertical: 26,
                ),
                decoration: BoxDecoration(
                  border: Border.all(color: t.primary.withValues(alpha: 0.32)),
                  borderRadius: BorderRadius.circular(t.radiusLg),
                ),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Container(
                      width: 52,
                      height: 52,
                      alignment: Alignment.center,
                      decoration: BoxDecoration(
                        color: t.primary.withValues(alpha: 0.12),
                        shape: BoxShape.circle,
                      ),
                      child: Icon(Icons.add, color: t.primary, size: 30),
                    ),
                    const SizedBox(height: 14),
                    Text('добавить профиль', style: t.sectionTitle),
                    const SizedBox(height: 6),
                    Text(
                      'вставьте HTTPS-подписку = каждый сервер добавится отдельно,\nили ключ vless://, trojan://, ss://, hysteria2://',
                      textAlign: TextAlign.center,
                      style: TextStyle(
                        color: t.textMuted,
                        fontSize: NekkoTokens.sizeBody,
                        fontWeight: FontWeight.w600,
                        height: 1.4,
                      ),
                    ),
                  ],
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _header(NekkoTokens t, int count) {
    final s = context.watch<AppState>();
    return Padding(
      padding: const EdgeInsets.fromLTRB(20, 2, 12, 8),
      // Вся полоса фикс-высотой 34 = высоте боковых кнопок. В Row caXis
      // == center всё ровно по середине. Заголовок и счётчик внутри —
      // отдельный Row с baseline-выравниванием (у них разный fontSize).
      child: SizedBox(
        height: 34,
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.center,
          children: [
            Row(
              crossAxisAlignment: CrossAxisAlignment.baseline,
              textBaseline: TextBaseline.alphabetic,
              children: [
                Text('профили', style: t.sectionTitle),
                const SizedBox(width: 8),
                Text(
                  '$count',
                  style: TextStyle(
                    color: t.textMuted,
                    fontSize: NekkoTokens.sizeSubtitle,
                    fontWeight: FontWeight.w600,
                    fontFamily: t.monoFamily,
                  ),
                ),
              ],
            ),
            const SizedBox(width: 12),
            _addProfileButton(t),
            const Spacer(),
            _autoChip(t, s),
          ],
        ),
      ),
    );
  }

  Widget _addProfileButton(NekkoTokens t) {
    return Tooltip(
      message: 'добавить профиль',
      child: Material(
        color: Colors.transparent,
        borderRadius: BorderRadius.circular(t.radius),
        child: InkWell(
          onTap: () => ImportProfileDialog.show(context),
          mouseCursor: SystemMouseCursors.click,
          borderRadius: BorderRadius.circular(t.radius),
          hoverColor: t.primary.withValues(alpha: 0.10),
          child: Container(
            height: 34,
            padding: const EdgeInsets.symmetric(horizontal: 10),
            alignment: Alignment.center,
            decoration: BoxDecoration(
              border: Border.all(color: t.borderStrong),
              borderRadius: BorderRadius.circular(t.radius),
            ),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.center,
              children: [
                Icon(Icons.add, color: t.text, size: 14),
                const SizedBox(width: 5),
                Text(
                  'добавить',
                  style: TextStyle(
                    color: t.text,
                    fontSize: 12,
                    fontWeight: FontWeight.w600,
                    fontFamily: t.monoFamily,
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  // Tiny "auto" chip: when on, sing-box runs a urltest group and routes
  // through the lowest-latency live profile, auto-flipping when something
  // breaks. Engine reloads on toggle so changes take effect immediately.
  Widget _autoChip(NekkoTokens t, AppState s) {
    final on = s.autoMode;
    return Material(
      color: Colors.transparent,
      borderRadius: BorderRadius.circular(t.radius),
      child: InkWell(
        onTap: s.busy ? null : () => s.setAutoMode(!on),
        borderRadius: BorderRadius.circular(t.radius),
        mouseCursor: SystemMouseCursors.click,
        hoverColor: t.primary.withValues(alpha: 0.10),
        // Включённое состояние = как активный профиль: тёмная primary-
        // заливка, primary-аутлайн, иконка и текст в primary. Иначе по
        // одной только смене иконки было не считать, что режим активен.
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 150),
          height: 34,
          padding: const EdgeInsets.symmetric(horizontal: 10),
          alignment: Alignment.center,
          decoration: BoxDecoration(
            color: on ? const Color(0xFF1A1014) : Colors.transparent,
            border: Border.all(
              color: on ? t.primary.withValues(alpha: 0.32) : t.borderStrong,
            ),
            borderRadius: BorderRadius.circular(t.radius),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(
                on ? Icons.bolt : Icons.bolt_outlined,
                size: 16,
                color: on ? t.primary : t.text,
              ),
              const SizedBox(width: 1),
              Text(
                'auto',
                style: TextStyle(
                  color: on ? t.primary : t.text,
                  fontSize: NekkoTokens.sizeLabel,
                  fontWeight: FontWeight.w600,
                  fontFamily: t.monoFamily,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _search(NekkoTokens t) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16),
      child: NekkoTextField(
        hint: 'поиск',
        prefixIcon: Icon(Icons.search, size: 16, color: t.textMuted),
        onChanged: (v) => setState(() => _query = v),
      ),
    );
  }
}

class _SubscriptionGroup {
  final SubscriptionInfo info;
  final List<ProfileInfo> profiles;

  const _SubscriptionGroup(this.info, this.profiles);
}

class _SubscriptionCard extends StatelessWidget {
  final SubscriptionInfo info;
  final List<ProfileInfo> profiles;
  final String? expandedProfile;
  final ValueChanged<String> onToggleProfile;

  const _SubscriptionCard({
    super.key,
    required this.info,
    required this.profiles,
    required this.expandedProfile,
    required this.onToggleProfile,
  });

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final title = info.title.isEmpty ? 'подписка' : info.title.toLowerCase();
    final expires = info.expiresAt > 0
        ? DateTime.fromMillisecondsSinceEpoch(info.expiresAt * 1000)
        : null;
    final remaining = expires?.difference(DateTime.now());
    final daysLeft = remaining == null
        ? null
        : remaining.isNegative
        ? -1
        : (remaining.inSeconds + Duration.secondsPerDay - 1) ~/
              Duration.secondsPerDay;
    final periodLabel = daysLeft == null
        ? ''
        : daysLeft < 0
        ? 'истекла'
        : daysLeft == 0
        ? 'меньше дня'
        : _daysLabel(daysLeft);
    final usedBytes = info.uploadBytes + info.downloadBytes;
    final trafficLabel = info.totalBytes > 0
        ? '${_formatGigabytes((info.totalBytes - usedBytes).clamp(0, info.totalBytes))} ГБ осталось'
        : usedBytes > 0
        ? '${_formatGigabytes(usedBytes)} ГБ'
        : '';
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 4),
      child: CustomPaint(
        foregroundPainter: _DashedSubscriptionBorder(
          color: t.borderStrong,
          radius: t.radiusLg,
        ),
        child: Column(
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(24, 13, 24, 7),
              child: Row(
                children: [
                  Expanded(
                    child: Text(
                      title,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        color: t.text,
                        fontSize: 12,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                  ),
                  if (periodLabel.isNotEmpty)
                    Expanded(
                      child: Center(
                        child: Text(
                          periodLabel,
                          style: TextStyle(
                            color: t.textMuted,
                            fontSize: 12,
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                      ),
                    ),
                  if (trafficLabel.isNotEmpty)
                    Expanded(
                      child: Text(
                        trafficLabel,
                        textAlign: TextAlign.right,
                        style: TextStyle(
                          color: t.textSecondary,
                          fontFamily: t.monoFamily,
                          fontSize: 12,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                    ),
                ],
              ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(0, 2, 0, 7),
              child: Column(
                children: [
                  for (final p in profiles)
                    ProfileTile(
                      key: ValueKey('subscription-${info.id}-${p.name}'),
                      profile: p,
                      expanded: expandedProfile == p.name,
                      onToggleExpand: () => onToggleProfile(p.name),
                    ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

String _daysLabel(int days) {
  final mod100 = days % 100;
  final mod10 = days % 10;
  final unit = mod100 >= 11 && mod100 <= 14
      ? 'дней'
      : mod10 == 1
      ? 'день'
      : mod10 >= 2 && mod10 <= 4
      ? 'дня'
      : 'дней';
  return '$days $unit';
}

String _formatGigabytes(int bytes) {
  final gigabytes = bytes / 1000000000;
  return gigabytes >= 10
      ? gigabytes.toStringAsFixed(0)
      : gigabytes.toStringAsFixed(1).replaceAll(RegExp(r'\.0$'), '');
}

class _DashedSubscriptionBorder extends CustomPainter {
  final Color color;
  final double radius;

  const _DashedSubscriptionBorder({required this.color, required this.radius});

  @override
  void paint(Canvas canvas, Size size) {
    const strokeWidth = 1.5;
    final path = Path()
      ..addRRect(
        RRect.fromRectAndRadius(
          Rect.fromLTWH(
            strokeWidth / 2,
            strokeWidth / 2,
            size.width - strokeWidth,
            size.height - strokeWidth,
          ),
          Radius.circular(radius),
        ),
      );
    final paint = Paint()
      ..color = color
      ..style = PaintingStyle.stroke
      ..strokeWidth = strokeWidth
      ..strokeCap = StrokeCap.round;
    for (final metric in path.computeMetrics()) {
      for (var start = 0.0; start < metric.length; start += 9) {
        final end = (start + 5).clamp(0.0, metric.length).toDouble();
        canvas.drawPath(metric.extractPath(start, end), paint);
      }
    }
  }

  @override
  bool shouldRepaint(_DashedSubscriptionBorder oldDelegate) =>
      oldDelegate.color != color || oldDelegate.radius != radius;
}
