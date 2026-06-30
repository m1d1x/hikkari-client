import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../core/models.dart';
import '../state/app_state.dart';
import '../state/uptime_stats.dart';
import '../theme/tokens.dart';
import 'profile_summary_card.dart';

/// One profile row. Collapsed and home-selected cards share the same visual
/// component; expanded rows add stats, rename, and delete controls.
class ProfileTile extends StatefulWidget {
  final ProfileInfo profile;
  final bool expanded;
  final VoidCallback onToggleExpand;

  const ProfileTile({
    super.key,
    required this.profile,
    required this.expanded,
    required this.onToggleExpand,
  });

  @override
  State<ProfileTile> createState() => _ProfileTileState();
}

class _ProfileTileState extends State<ProfileTile> {
  bool _editing = false;
  late final TextEditingController _editCtrl;

  @override
  void initState() {
    super.initState();
    _editCtrl = TextEditingController(text: widget.profile.label);
  }

  @override
  void didUpdateWidget(ProfileTile old) {
    super.didUpdateWidget(old);
    if (old.profile.label != widget.profile.label && !_editing) {
      _editCtrl.text = widget.profile.label;
    }
  }

  @override
  void dispose() {
    _editCtrl.dispose();
    super.dispose();
  }

  Future<void> _switchProfile() async {
    final s = context.read<AppState>();
    if (s.busy) return;
    if (widget.profile.active) return;
    await s.switchProfile(widget.profile.name);
    if (!mounted) return;
    final error = context.read<AppState>().lastError;
    if (error == null) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(error)),
    );
  }

  Future<void> _saveLabel() async {
    final v = _editCtrl.text.trim();
    setState(() => _editing = false);
    if (v.isEmpty || v == widget.profile.label) return;
    await context.read<AppState>().renameProfile(widget.profile.name, v);
  }

  Future<void> _confirmDelete() async {
    final t = context.tokens;
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: t.surface,
        title: const Text('Удалить профиль?'),
        content: Text(widget.profile.label),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('Отмена'),
          ),
          TextButton(
            onPressed: () => Navigator.pop(ctx, true),
            style: TextButton.styleFrom(
              foregroundColor: t.bad,
              overlayColor: Colors.transparent,
            ),
            child: const Text('Удалить'),
          ),
        ],
      ),
    );
    if (ok != true) return;
    if (!mounted) return;
    await context.read<AppState>().deleteProfile(widget.profile.name);
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final s = context.watch<AppState>();
    final hist = s.uptime[widget.profile.name] ?? const <PingSample>[];

    return ProfileSummaryCard(
      title: widget.profile.label,
      samples: hist,
      selected: widget.profile.active,
      onTap: s.busy ? null : _switchProfile,
      margin: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
      padding: const EdgeInsets.fromLTRB(14, 11, 8, 11),
      chartHeight: 32,
      trailing: _ExpandButton(
        expanded: widget.expanded,
        onTap: widget.onToggleExpand,
      ),
      // _ExpandButton — 34×34, иначе адаптивная раскладка карточки даёт
      // на trailing 18px и trailing вылезает за правый бордер.
      trailingWidth: 34,
      footer: _detail(context, t, hist),
      footerVisible: widget.expanded,
    );
  }

  Widget _detail(BuildContext context, NekkoTokens t, List<PingSample> hist) {
    final pct = UptimeStats.uptimePct(hist);
    final rtt = UptimeStats.avgRtt(hist);
    return Container(
      padding: const EdgeInsets.fromLTRB(16, 4, 16, 14),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          _kv(
            context,
            t,
            'аптайм',
            pct < 0
                ? '—'
                : '$pct%  ·  ${rtt < 0 ? "—" : "$rtt ms"}',
          ),
          _kv(context, t, 'сервер',
              widget.profile.endpoint.isEmpty ? '—' : widget.profile.endpoint,
              mono: true),
          _kv(context, t, 'протокол',
              widget.profile.protocol.isEmpty ? '—' : widget.profile.protocol),
          _labelRow(context, t),
          if (!widget.profile.active)
            Padding(
              padding: const EdgeInsets.only(top: 10),
              child: Align(
                alignment: Alignment.centerLeft,
                child: TextButton.icon(
                  onPressed: _confirmDelete,
                  icon: const Icon(Icons.delete_outline, size: 16),
                  label: const Text('удалить профиль'),
                  style: TextButton.styleFrom(
                    foregroundColor: t.bad,
                    overlayColor: Colors.transparent,
                    padding:
                        const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                    minimumSize: Size.zero,
                    tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                  ),
                ),
              ),
            ),
        ],
      ),
    );
  }

  Widget _kv(BuildContext context, NekkoTokens t, String k, String v,
      {bool mono = false}) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 80,
            child: Text(k,
                style: TextStyle(color: t.textMuted, fontSize: 12)),
          ),
          Expanded(
            child: Text(
              v,
              style: TextStyle(
                color: t.text,
                fontSize: 12,
                fontFamily: mono ? t.monoFamily : null,
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _labelRow(BuildContext context, NekkoTokens t) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        children: [
          SizedBox(
            width: 80,
            child: Text('профиль',
                style: TextStyle(color: t.textMuted, fontSize: 12)),
          ),
          Expanded(
            child: _editing
                ? TextField(
                    controller: _editCtrl,
                    autofocus: true,
                    style: TextStyle(color: t.text, fontSize: 12),
                    decoration: InputDecoration(
                      isDense: true,
                      contentPadding: const EdgeInsets.symmetric(
                          horizontal: 8, vertical: 6),
                      border: OutlineInputBorder(
                        borderRadius: BorderRadius.circular(t.radius),
                      ),
                    ),
                    onSubmitted: (_) => _saveLabel(),
                    onTapOutside: (_) => _saveLabel(),
                  )
                : GestureDetector(
                    onDoubleTap: () => setState(() => _editing = true),
                    child: MouseRegion(
                      cursor: SystemMouseCursors.click,
                      child: Tooltip(
                        message: 'двойной клик — переименовать',
                        child: Text(
                          widget.profile.label,
                          style: TextStyle(color: t.text, fontSize: 12),
                        ),
                      ),
                    ),
                  ),
          ),
        ],
      ),
    );
  }
}

class _ExpandButton extends StatelessWidget {
  final bool expanded;
  final VoidCallback onTap;

  const _ExpandButton({required this.expanded, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return SizedBox(
      width: 34,
      height: 34,
      child: Material(
        color: Colors.transparent,
        borderRadius: BorderRadius.circular(10),
        child: InkWell(
          onTap: onTap,
          mouseCursor: SystemMouseCursors.click,
          borderRadius: BorderRadius.circular(10),
          child: AnimatedSwitcher(
            duration: const Duration(milliseconds: 120),
            child: Icon(
              expanded ? Icons.keyboard_arrow_up : Icons.keyboard_arrow_down,
              key: ValueKey(expanded),
              color: t.textMuted,
              size: 19,
            ),
          ),
        ),
      ),
    );
  }
}
