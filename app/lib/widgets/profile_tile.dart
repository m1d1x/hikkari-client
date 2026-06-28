import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../core/models.dart';
import '../state/app_state.dart';
import '../state/uptime_stats.dart';
import '../theme/tokens.dart';
import 'sparkline.dart';

/// One profile row. Collapsed: number, sparkline, RTT, active dot.
/// Expanded: stats (uptime %, server, protocol) + inline-editable label +
/// delete (only when not active). Mirrors the Svelte `.prow` block.
class ProfileTile extends StatefulWidget {
  final ProfileInfo profile;
  final int index;
  final bool expanded;
  final VoidCallback onToggleExpand;

  const ProfileTile({
    super.key,
    required this.profile,
    required this.index,
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
            style: TextButton.styleFrom(foregroundColor: t.bad),
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
    final active = widget.profile.active;
    final borderColor = active ? t.textMuted : t.border;

    return AnimatedContainer(
      duration: const Duration(milliseconds: 150),
      margin: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
      decoration: BoxDecoration(
        color: t.surface,
        border: Border.all(color: borderColor),
        borderRadius: BorderRadius.circular(t.radiusLg),
      ),
      clipBehavior: Clip.antiAlias,
      child: Column(
        children: [
          _rowMain(context, t, hist),
          AnimatedSize(
            duration: const Duration(milliseconds: 180),
            curve: Curves.easeOut,
            alignment: Alignment.topCenter,
            child: widget.expanded
                ? _detail(context, t, hist)
                : const SizedBox.shrink(),
          ),
        ],
      ),
    );
  }

  Widget _rowMain(BuildContext context, NekkoTokens t, List<PingSample> hist) {
    final s = context.read<AppState>();
    final rtt = UptimeStats.avgRtt(hist);
    final active = widget.profile.active;
    final lineColor = active ? t.ok : t.textMuted;

    return InkWell(
      onTap: s.busy ? null : _switchProfile,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(8, 10, 14, 10),
        child: Row(
          children: [
            // Number button — expand toggle, doesn't propagate to row tap.
            SizedBox(
              width: 36,
              height: 32,
              child: Material(
                color: Colors.transparent,
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(8),
                ),
                child: InkWell(
                  onTap: widget.onToggleExpand,
                  borderRadius: BorderRadius.circular(8),
                  child: Center(
                    child: Text(
                      '${widget.index + 1}',
                      style: TextStyle(
                        color: t.textMuted,
                        fontFamily: t.monoFamily,
                        fontWeight: FontWeight.w500,
                      ),
                    ),
                  ),
                ),
              ),
            ),
            const SizedBox(width: 8),
            Expanded(
              child: SizedBox(
                height: 32,
                child: Sparkline(samples: hist, color: lineColor),
              ),
            ),
            const SizedBox(width: 12),
            // RTT (avg).
            SizedBox(
              width: 56,
              child: Text(
                rtt < 0 ? '—' : '$rtt ms',
                textAlign: TextAlign.right,
                style: TextStyle(
                  color: t.text,
                  fontFamily: t.monoFamily,
                  fontSize: 12,
                ),
              ),
            ),
            const SizedBox(width: 8),
            // Active dot — same green as `--ok`, faded when not active.
            AnimatedContainer(
              duration: const Duration(milliseconds: 200),
              width: 8,
              height: 8,
              decoration: BoxDecoration(
                color: active ? t.ok : Colors.transparent,
                shape: BoxShape.circle,
                border: Border.all(
                  color: active ? Colors.transparent : t.border,
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _detail(BuildContext context, NekkoTokens t, List<PingSample> hist) {
    final pct = UptimeStats.uptimePct(hist);
    final rtt = UptimeStats.avgRtt(hist);
    return Container(
      padding: const EdgeInsets.fromLTRB(16, 4, 16, 14),
      decoration: BoxDecoration(
        border: Border(top: BorderSide(color: t.border)),
      ),
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
                    child: Tooltip(
                      message: 'двойной клик — переименовать',
                      child: Text(
                        widget.profile.label,
                        style: TextStyle(color: t.text, fontSize: 12),
                      ),
                    ),
                  ),
          ),
        ],
      ),
    );
  }
}
