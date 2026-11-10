import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:url_launcher/url_launcher.dart';

import '../core/models.dart';
import '../state/app_state.dart';
import '../state/uptime_stats.dart';
import '../theme/tokens.dart';
import '../ui/nekko_context_menu.dart';
import '../ui/nekko_dialog.dart';
import '../ui/nekko_text_field.dart';
import '../ui/nekko_toast.dart';
import 'add_profile_dialog.dart';
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

class _ProfilesSheetState extends State<ProfilesSheet>
    with WidgetsBindingObserver {
  String? _expanded;
  String _query = '';
  String? _lastActive;
  bool _lastActiveInitialized = false;
  late final AppState _state;

  // Servers are measured only while this list is on screen: open, and the app
  // not in the background.
  @override
  void initState() {
    super.initState();
    _state = context.read<AppState>();
    WidgetsBinding.instance.addObserver(this);
    _state.setServerListOpen(true);
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    _state.setServerListOpen(state == AppLifecycleState.resumed);
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _state.setServerListOpen(false);
    super.dispose();
  }

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
      if (s.autoMode ? p.name == s.autoProfileName : p.active) return (0, 0);
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
      final ac = a.capacity, bc = b.capacity;
      final am = ac?.fresh ?? false, bm = bc?.fresh ?? false;
      if (am != bm) return am ? -1 : 1;
      if (am && bm && ac!.mbps != bc!.mbps) return bc.mbps.compareTo(ac.mbps);
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
              onTap: () => AddProfileDialog.show(context),
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
        // Narrow window: «добавить» keeps only its icon, so the title, the
        // add button and «авто» stay on one line.
        child: LayoutBuilder(
          builder: (context, box) => Row(
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
              _addProfileButton(t, compact: box.maxWidth < 300),
              const Spacer(),
              _autoChip(t, s),
            ],
          ),
        ),
      ),
    );
  }

  Widget _addProfileButton(NekkoTokens t, {bool compact = false}) {
    return Tooltip(
      message: 'добавить профиль',
      child: Material(
        color: Colors.transparent,
        borderRadius: BorderRadius.circular(t.radius),
        child: InkWell(
          onTap: () => AddProfileDialog.show(context),
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
                Icon(Icons.add, color: t.text, size: compact ? 16 : 14),
                if (!compact) const SizedBox(width: 5),
                if (!compact)
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

  // Auto mode owns routing until a manual profile selection disables it.
  Widget _autoChip(NekkoTokens t, AppState s) {
    final on = s.autoMode;
    final eligible = s.profiles.any((p) => p.autoEligible);
    return Tooltip(
      message: on
          ? 'Выбор сервера вручную выключит авто'
          : eligible
          ? 'Автовыбор внутри подписки hikkari'
          : 'Доступно с подпиской hikkari',
      child: Material(
        color: Colors.transparent,
        borderRadius: BorderRadius.circular(t.radius),
        child: InkWell(
          onTap: s.busy || (!on && !eligible) ? null : () => s.setAutoMode(!on),
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
                  color: on
                      ? t.primary
                      : eligible
                      ? t.text
                      : t.textMuted,
                ),
                const SizedBox(width: 1),
                Text(
                  'авто',
                  style: TextStyle(
                    color: on
                        ? t.primary
                        : eligible
                        ? t.text
                        : t.textMuted,
                    fontSize: NekkoTokens.sizeLabel,
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

  void _menu(BuildContext context, Offset at) {
    final renewUrl = info.renewUrl;
    NekkoContextMenu.show(context, at, [
      if (renewUrl != null)
        NekkoMenuItem(
          label: 'продлить',
          onTap: () =>
              launchUrl(renewUrl, mode: LaunchMode.externalApplication),
        ),
      NekkoMenuItem(
        label: 'удалить',
        destructive: true,
        onTap: () => _confirmDelete(context),
      ),
    ]);
  }

  Future<void> _confirmDelete(BuildContext context) async {
    final state = context.read<AppState>();
    final ok = await NekkoDialog.confirm(
      context,
      title: 'удалить подписку?',
      message: '${info.displayTitle}: профилей ${profiles.length}',
      confirmLabel: 'удалить',
    );
    if (ok != true || !context.mounted) return;
    for (final p in profiles) {
      await state.deleteProfile(p.name);
      if (state.lastError != null) break;
    }
    if (!context.mounted || state.lastError == null) return;
    NekkoToast.show(context, state.lastError!);
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final isHikkari = info.isHikkari;
    final fullTitle = info.displayTitle;
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
    final renewUrl = info.renewUrl;
    // Seven days or less, or 100 GB or less, left: the renewal is what to do
    // next, so the button stays on screen. Otherwise the values themselves
    // are the (hover) renew target.
    final leftBytes = info.totalBytes > 0
        ? (info.totalBytes - usedBytes).clamp(0, info.totalBytes)
        : null;
    final daysDue = daysLeft != null && daysLeft <= 7;
    final trafficDue = leftBytes != null && leftBytes <= 100 * 1000000000;
    final renewDue = daysDue || trafficDue;
    final trafficLabel = leftBytes != null
        ? '${_formatGigabytes(leftBytes)} ГБ'
        : usedBytes > 0
        ? '${_formatGigabytes(usedBytes)} ГБ'
        : '';
    final borderColor = isHikkari
        ? t.primary.withValues(alpha: 0.6)
        : t.borderStrong;
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 4),
      child: CustomPaint(
        foregroundPainter: _DashedSubscriptionBorder(
          color: borderColor,
          radius: t.radiusLg,
        ),
        child: Column(
          children: [
            GestureDetector(
              behavior: HitTestBehavior.opaque,
              onSecondaryTapUp: (d) => _menu(context, d.globalPosition),
              child: Padding(
                padding: const EdgeInsets.fromLTRB(24, 10, 24, 4),
                // Three columns: the title on the left, the period in the
                // middle of the card, the traffic on the right. The two
                // sides take equal width, so the period sits at the true
                // centre whatever the title or traffic length.
                child: Row(
                  children: [
                    Expanded(
                      child: Align(
                        alignment: Alignment.centerLeft,
                        child: Tooltip(
                          message: fullTitle,
                          waitDuration: const Duration(milliseconds: 300),
                          child: Text(
                            fullTitle,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            style: TextStyle(
                              color: isHikkari ? t.primary : t.text,
                              fontSize: 12,
                              fontWeight: FontWeight.w700,
                            ),
                          ),
                        ),
                      ),
                    ),
                    if (periodLabel.isNotEmpty)
                      Padding(
                        padding: const EdgeInsets.symmetric(horizontal: 12),
                        child: _HeaderValue(
                          text: periodLabel,
                          color: daysDue ? t.primary : t.textMuted,
                          // With the static button up it is the one renew
                          // target; the values stay plain.
                          url: renewDue ? null : renewUrl,
                        ),
                      ),
                    Expanded(
                      child: Align(
                        alignment: Alignment.centerRight,
                        child: Row(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            if (trafficLabel.isNotEmpty)
                              Flexible(
                                child: _HeaderValue(
                                  text: trafficLabel,
                                  color: trafficDue
                                      ? t.primary
                                      : t.textSecondary,
                                  mono: true,
                                  url: renewDue ? null : renewUrl,
                                  gapAfter: renewUrl != null && renewDue
                                      ? 10
                                      : 0,
                                ),
                              ),
                            if (renewUrl != null && renewDue)
                              _RenewButton(url: renewUrl),
                          ],
                        ),
                      ),
                    ),
                  ],
                ),
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

/// A header value (period, traffic). With a renew target, hovering it puts
/// the renew button in its place; the slot is sized for the wider of the two
/// so nothing around it moves. A value that does not fit whole is left out
/// rather than cut to «5 д…».
class _HeaderValue extends StatefulWidget {
  final String text;
  final Color color;
  final bool mono;
  final Uri? url;

  /// Space after the value, dropped with it when it does not fit.
  final double gapAfter;

  const _HeaderValue({
    required this.text,
    required this.color,
    required this.url,
    this.mono = false,
    this.gapAfter = 0,
  });

  @override
  State<_HeaderValue> createState() => _HeaderValueState();
}

class _HeaderValueState extends State<_HeaderValue> {
  bool _hover = false;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final url = widget.url;
    final style = TextStyle(
      color: widget.color,
      fontFamily: widget.mono ? t.monoFamily : null,
      fontSize: 12,
      fontWeight: FontWeight.w600,
    );
    final label = Text(widget.text, maxLines: 1, softWrap: false, style: style);
    return LayoutBuilder(
      builder: (context, box) {
        final scaler = MediaQuery.textScalerOf(context);
        final textWidth = _textWidth(widget.text, style, scaler);
        final need = url == null
            ? textWidth
            : textWidth > _RenewButton.width(context)
            ? textWidth
            : _RenewButton.width(context);
        if (need + widget.gapAfter > box.maxWidth) {
          return const SizedBox.shrink();
        }
        if (url == null) {
          return Padding(
            padding: EdgeInsets.only(right: widget.gapAfter),
            child: SizedBox(
              height: _RenewButton.height,
              child: Center(child: label),
            ),
          );
        }
        // The value rolls up and out, the button rolls in from below: one
        // short vertical swap inside the slot, clipped to it.
        const roll = Duration(milliseconds: 160);
        const curve = Curves.easeOutCubic;
        return MouseRegion(
          onEnter: (_) => setState(() => _hover = true),
          onExit: (_) => setState(() => _hover = false),
          child: ClipRect(
            child: SizedBox(
              width: need,
              height: _RenewButton.height,
              child: Stack(
                alignment: Alignment.center,
                children: [
                  AnimatedSlide(
                    duration: roll,
                    curve: curve,
                    offset: Offset(0, _hover ? -0.6 : 0),
                    child: AnimatedOpacity(
                      duration: roll,
                      curve: curve,
                      opacity: _hover ? 0 : 1,
                      child: label,
                    ),
                  ),
                  AnimatedSlide(
                    duration: roll,
                    curve: curve,
                    offset: Offset(0, _hover ? 0 : 0.6),
                    child: AnimatedOpacity(
                      duration: roll,
                      curve: curve,
                      opacity: _hover ? 1 : 0,
                      child: IgnorePointer(
                        ignoring: !_hover,
                        child: SizedBox(
                          width: need,
                          child: _RenewButton(url: url, hovered: _hover),
                        ),
                      ),
                    ),
                  ),
                ],
              ),
            ),
          ),
        );
      },
    );
  }
}

double _textWidth(String text, TextStyle style, TextScaler scaler) =>
    (TextPainter(
      text: TextSpan(text: text, style: style),
      textDirection: TextDirection.ltr,
      textScaler: scaler,
      maxLines: 1,
    )..layout()).width;

/// Opens the landing's checkout for this subscription's next period: static
/// when the period or traffic is nearly spent, otherwise in place of a
/// hovered value. The token rides in the URL's fragment, which the browser
/// never sends.
///
/// Hover is the landing's primary button: the soft fill fades out and a
/// dashed primary contour fades in and runs around the edge.
class _RenewButton extends StatefulWidget {
  final Uri url;

  /// Shown in place of a hovered value: the pointer is already over it, so
  /// it starts in the hover look rather than waiting for its own enter.
  final bool hovered;

  const _RenewButton({required this.url, this.hovered = false});

  static const height = 26.0;
  static const _padding = 10.0;
  static const _style = TextStyle(fontSize: 12, fontWeight: FontWeight.w600);

  /// The button's natural width, for slots that must fit it.
  static double width(BuildContext context) =>
      _textWidth(
        'продлить',
        _style.copyWith(fontFamily: context.tokens.monoFamily),
        MediaQuery.textScalerOf(context),
      ) +
      2 * _padding;

  @override
  State<_RenewButton> createState() => _RenewButtonState();
}

class _RenewButtonState extends State<_RenewButton>
    with SingleTickerProviderStateMixin {
  // One lap of the dash pattern every 800 ms, as on the landing.
  late final AnimationController _run = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 800),
  );
  bool _ownHover = false;

  bool get _hover => _ownHover || widget.hovered;

  @override
  void initState() {
    super.initState();
    _sync();
  }

  @override
  void didUpdateWidget(_RenewButton old) {
    super.didUpdateWidget(old);
    _sync();
  }

  void _setHover(bool v) {
    setState(() => _ownHover = v);
    _sync();
  }

  // The contour only runs while it is visible.
  void _sync() {
    if (_hover) {
      if (!_run.isAnimating) _run.repeat();
    } else {
      _run.stop();
    }
  }

  @override
  void dispose() {
    _run.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    const fade = Duration(milliseconds: 220);
    const curve = AppCurves.hover;
    final radius = BorderRadius.circular(t.radius);
    return MouseRegion(
      cursor: SystemMouseCursors.click,
      onEnter: (_) => _setHover(true),
      onExit: (_) => _setHover(false),
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: () =>
            launchUrl(widget.url, mode: LaunchMode.externalApplication),
        child: Stack(
          children: [
            AnimatedContainer(
              duration: fade,
              curve: curve,
              height: _RenewButton.height,
              padding: const EdgeInsets.symmetric(
                horizontal: _RenewButton._padding,
              ),
              alignment: Alignment.center,
              decoration: BoxDecoration(
                color: t.primary.withValues(alpha: _hover ? 0 : 0.12),
                borderRadius: radius,
              ),
              child: Text(
                'продлить',
                maxLines: 1,
                softWrap: false,
                style: _RenewButton._style.copyWith(
                  color: t.primary,
                  fontFamily: t.monoFamily,
                ),
              ),
            ),
            Positioned.fill(
              child: IgnorePointer(
                child: AnimatedOpacity(
                  duration: fade,
                  curve: curve,
                  opacity: _hover ? 1 : 0,
                  child: CustomPaint(
                    painter: _RunningDashes(
                      run: _run,
                      color: t.primary,
                      radius: t.radius,
                    ),
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// A dashed rounded contour whose dashes run along the edge. The pattern is
/// stretched to a whole number of dash and gap pairs, so the seam where the
/// path closes never shows at any button width.
class _RunningDashes extends CustomPainter {
  final Animation<double> run;
  final Color color;
  final double radius;

  _RunningDashes({required this.run, required this.color, required this.radius})
    : super(repaint: run);

  static const _dash = 5.0, _gap = 4.0, _stroke = 1.5;

  @override
  void paint(Canvas canvas, Size size) {
    final rect = Rect.fromLTWH(
      _stroke / 2,
      _stroke / 2,
      size.width - _stroke,
      size.height - _stroke,
    );
    final path = Path()
      ..addRRect(RRect.fromRectAndRadius(rect, Radius.circular(radius)));
    final paint = Paint()
      ..color = color
      ..style = PaintingStyle.stroke
      ..strokeWidth = _stroke
      ..strokeCap = StrokeCap.round;
    for (final metric in path.computeMetrics()) {
      final len = metric.length;
      final pairs = (len / (_dash + _gap)).round().clamp(1, 1 << 20);
      final period = len / pairs;
      final dash = period * _dash / (_dash + _gap);
      // Two pairs per lap, as the landing's −28 offset on its 14 pattern.
      final shift = run.value * 2 * period;
      for (var i = -1; i < pairs; i++) {
        final start = i * period + shift;
        final end = start + dash;
        final a = start.clamp(0.0, len);
        final b = end.clamp(0.0, len);
        if (b > a) canvas.drawPath(metric.extractPath(a, b), paint);
        // The part of a dash that runs past the end continues at the start.
        if (end > len) {
          canvas.drawPath(metric.extractPath(0, end - len), paint);
        }
      }
    }
  }

  @override
  bool shouldRepaint(_RunningDashes old) =>
      old.color != color || old.radius != radius || old.run != run;
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
