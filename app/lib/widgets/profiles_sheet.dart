import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../state/app_state.dart';
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

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final s = context.watch<AppState>();

    // Auto-dismiss on successful switch — when active profile changes mid-sheet.
    final active = s.activeProfileName;
    if (_lastActive == null) {
      _lastActive = active;
    } else if (active != null && active != _lastActive) {
      _lastActive = active;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) Navigator.of(context).maybePop();
      });
    }

    final q = _query.trim().toLowerCase();
    final all = s.profiles;
    final filtered = q.isEmpty
        ? all
        : all
            .where((p) =>
                p.label.toLowerCase().contains(q) ||
                p.name.toLowerCase().contains(q))
            .toList();

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
            const SizedBox(height: 4),
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
                        final realIndex = all.indexOf(p);
                        return ProfileTile(
                          key: ValueKey(p.name),
                          profile: p,
                          index: realIndex,
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
        padding: const EdgeInsets.only(top: 8, bottom: 6),
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
      padding: const EdgeInsets.fromLTRB(20, 6, 12, 10),
      child: Row(
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
              color: t.textFaint,
              fontSize: 12,
              fontFamily: t.monoFamily,
            ),
          ),
          const Spacer(),
          _autoChip(t, s),
          const SizedBox(width: 6),
          IconButton(
            tooltip: 'импорт по ссылке',
            iconSize: 16,
            padding: EdgeInsets.zero,
            constraints:
                const BoxConstraints(minWidth: 32, minHeight: 32),
            onPressed: () => ImportProfileDialog.show(context),
            icon: Icon(Icons.add, color: t.textMuted),
          ),
        ],
      ),
    );
  }

  // Tiny "auto" chip: when on, sing-box runs a urltest group and routes
  // through the lowest-latency live profile, auto-flipping when something
  // breaks. Engine reloads on toggle so changes take effect immediately.
  Widget _autoChip(NekkoTokens t, AppState s) {
    final on = s.autoMode;
    return InkWell(
      onTap: s.busy ? null : () => s.setAutoMode(!on),
      borderRadius: BorderRadius.circular(t.radius),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
        decoration: BoxDecoration(
          color: on ? t.surface2 : Colors.transparent,
          border: Border.all(color: on ? t.borderStrong : t.border),
          borderRadius: BorderRadius.circular(t.radius),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(
              on ? Icons.bolt : Icons.bolt_outlined,
              size: 12,
              color: on ? t.ok : t.textMuted,
            ),
            const SizedBox(width: 4),
            Text(
              'auto',
              style: TextStyle(
                color: on ? t.text : t.textMuted,
                fontSize: 11,
                fontFamily: t.monoFamily,
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _search(NekkoTokens t) => Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16),
        child: TextField(
          onChanged: (v) => setState(() => _query = v),
          style: TextStyle(color: t.text, fontSize: 13),
          decoration: InputDecoration(
            hintText: 'поиск',
            prefixIcon: Icon(Icons.search, size: 16, color: t.textMuted),
            prefixIconConstraints:
                const BoxConstraints(minWidth: 36, minHeight: 0),
            isDense: true,
            contentPadding:
                const EdgeInsets.symmetric(horizontal: 8, vertical: 10),
          ),
        ),
      );
}
