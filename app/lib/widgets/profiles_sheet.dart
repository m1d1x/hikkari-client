import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../core/models.dart';
import '../state/app_state.dart';
import '../state/uptime_stats.dart';
import '../theme/tokens.dart';
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
            .where((p) =>
                p.label.toLowerCase().contains(q) ||
                p.name.toLowerCase().contains(q))
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
    final filtered = matched;

    return DraggableScrollableSheet(
      expand: false,
      initialChildSize: 0.6,
      minChildSize: 0.35,
      maxChildSize: 0.92,
      builder: (ctx, scrollCtrl) {
        return Column(
          children: [
            _grabber(t),
            _header(t, all.length),
            _search(t),
            const SizedBox(height: 10),
            Expanded(
              child: filtered.isEmpty
                  ? Center(
                      child: Text(
                        'ничего не найдено',
                        style: TextStyle(color: t.textMuted, fontSize: 12),
                      ),
                    )
                  : ListView.builder(
                      controller: scrollCtrl,
                      padding: const EdgeInsets.only(bottom: 16),
                      itemCount: filtered.length,
                      itemBuilder: (_, i) {
                        final p = filtered[i];
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
                Text(
                  'профили',
                  style: TextStyle(
                    color: t.text,
                    fontSize: 14,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                const SizedBox(width: 8),
                Text(
                  '$count',
                  style: TextStyle(
                    color: t.textMuted,
                    fontSize: 13,
                    fontWeight: FontWeight.w600,
                    fontFamily: t.monoFamily,
                  ),
                ),
              ],
            ),
            const Spacer(),
            _autoChip(t, s),
            const SizedBox(width: 6),
            _addProfileButton(t),
          ],
        ),
      ),
    );
  }

  // Заметная "+" вместо мелкого иконбаттона. Бордер + квадрат как у add-
  // кнопки в маршрутизации, чтобы кнопки в приложении выглядели одного
  // семейства.
  Widget _addProfileButton(NekkoTokens t) {
    return Tooltip(
      message: 'импорт по ссылке',
      child: Material(
        color: Colors.transparent,
        borderRadius: BorderRadius.circular(t.radius),
        child: InkWell(
          onTap: () => ImportProfileDialog.show(context),
          mouseCursor: SystemMouseCursors.click,
          borderRadius: BorderRadius.circular(t.radius),
          hoverColor: t.primary.withValues(alpha: 0.10),
          child: Container(
            width: 34,
            height: 34,
            alignment: Alignment.center,
            decoration: BoxDecoration(
              border: Border.all(color: t.borderStrong),
              borderRadius: BorderRadius.circular(t.radius),
            ),
            child: Icon(Icons.add, color: t.text, size: 18),
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
        child: Container(
          height: 34,
          padding: const EdgeInsets.symmetric(horizontal: 12),
          alignment: Alignment.center,
          decoration: BoxDecoration(
            color: on ? t.surface2 : Colors.transparent,
            border: Border.all(color: t.borderStrong),
            borderRadius: BorderRadius.circular(t.radius),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(
                on ? Icons.bolt : Icons.bolt_outlined,
                size: 14,
                color: on ? t.ok : t.text,
              ),
              const SizedBox(width: 6),
              Text(
                'auto',
                style: TextStyle(
                  color: t.text,
                  fontSize: 12,
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
    final textStyle = TextStyle(
      color: t.text,
      fontFamily: t.monoFamily,
      fontSize: 12,
      fontWeight: FontWeight.w500,
      letterSpacing: 0.02 * 12,
    );
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16),
      child: Container(
        height: 40,
        decoration: BoxDecoration(
          color: t.bg,
          border: Border.all(color: t.border),
          borderRadius: BorderRadius.circular(t.radius),
        ),
        clipBehavior: Clip.antiAlias,
        child: Material(
          type: MaterialType.transparency,
          child: TextField(
            onChanged: (v) => setState(() => _query = v),
            style: textStyle,
            cursorColor: t.text,
            cursorWidth: 1.5,
            cursorRadius: const Radius.circular(1),
            maxLines: 1,
            textAlignVertical: TextAlignVertical.center,
            decoration: InputDecoration(
              isDense: true,
              filled: true,
              fillColor: Colors.transparent,
              hoverColor: Colors.transparent,
              focusColor: Colors.transparent,
              hintText: 'поиск',
              hintStyle: textStyle.copyWith(color: t.textFaint),
              prefixIcon: Icon(Icons.search, size: 16, color: t.textMuted),
              prefixIconConstraints: const BoxConstraints(
                minWidth: 40,
                minHeight: 40,
              ),
              contentPadding:
                  const EdgeInsets.fromLTRB(0, 9, 0, 11),
              border: InputBorder.none,
              enabledBorder: InputBorder.none,
              focusedBorder: InputBorder.none,
            ),
          ),
        ),
      ),
    );
  }
}
