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
            if (all.isNotEmpty) ...[
              _search(t),
              const SizedBox(height: 10),
            ],
            Expanded(
              child: all.isEmpty
                  ? _emptyState(t)
                  : filtered.isEmpty
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
                padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 26),
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
                    Text(
                      'добавить профиль',
                      style: t.sectionTitle,
                    ),
                    const SizedBox(height: 6),
                    Text(
                      'вставьте HTTPS-подписку — каждый сервер добавится отдельно,\nили ссылку vless://, trojan://, ss://, hysteria2://',
                      textAlign: TextAlign.center,
                      style: TextStyle(
                        color: t.textMuted,
                        fontSize: NekkoTokens.sizeBody,
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
            _autoChip(t, s),
            const Spacer(),
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
            child: Icon(Icons.add, color: t.text, size: 20),
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
          height: 30,
          padding: const EdgeInsets.fromLTRB(9, 0, 13, 0),
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
