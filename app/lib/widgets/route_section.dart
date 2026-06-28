import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../state/app_state.dart';
import '../theme/tokens.dart';

/// "Маршрутизация" section — three categories of bypass-VPN rules with an
/// expandable body that hosts the tabs, input, and items list. Mirrors the
/// Svelte `.route` + `.items-block` blocks; collapse state and the active
/// tab live locally — the data itself comes from [AppState.whitelist].
class RouteSection extends StatefulWidget {
  const RouteSection({super.key});

  @override
  State<RouteSection> createState() => _RouteSectionState();
}

class _RouteSectionState extends State<RouteSection> {
  bool _open = false;
  String _tab = 'domains';
  final _input = TextEditingController();

  // Same labels / placeholders / hints the Svelte UI uses, kept in one
  // place so a future translation is a single-file edit.
  static const _labels = {
    'domains': 'домены',
    'ips': 'IP',
    'processes': 'приложения',
  };
  static const _placeholders = {
    'domains': 'github.com',
    'ips': '192.168.0.0/16',
    'processes': 'discord.exe',
  };
  static const _hints = {
    'domains':
        'example.com = все поддомены тоже. api.example.com = только этот',
    'ips': 'один IP или подсеть в CIDR-нотации',
    'processes': 'имя .exe — весь трафик процесса пойдёт мимо VPN',
  };

  @override
  void dispose() {
    _input.dispose();
    super.dispose();
  }

  List<String> _itemsFor(String tab, dynamic wl) {
    switch (tab) {
      case 'domains':
        return List<String>.from(wl.domains);
      case 'ips':
        return List<String>.from(wl.ips);
      default:
        return List<String>.from(wl.processes);
    }
  }

  Future<void> _add() async {
    final v = _input.text.trim();
    if (v.isEmpty) return;
    await context.read<AppState>().addRule(_tab, v);
    if (!mounted) return;
    _input.clear();
  }

  Future<void> _remove(String v) async {
    await context.read<AppState>().removeRule(_tab, v);
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final s = context.watch<AppState>();
    final items = _itemsFor(_tab, s.whitelist);

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _head(t),
        AnimatedSize(
          duration: const Duration(milliseconds: 200),
          curve: Curves.easeOut,
          alignment: Alignment.topCenter,
          child: _open ? _body(t) : const SizedBox.shrink(),
        ),
        if (_open && items.isNotEmpty) _itemsBlock(t, items),
      ],
    );
  }

  Widget _head(NekkoTokens t) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(20, 10, 20, 4),
      child: Row(
        children: [
          Expanded(
            child: GestureDetector(
              onTap: () => setState(() => _open = !_open),
              behavior: HitTestBehavior.opaque,
              child: Text(
                'маршрутизация',
                style: TextStyle(
                  color: t.text,
                  fontSize: 13,
                  letterSpacing: 0.4,
                  fontWeight: FontWeight.w500,
                ),
              ),
            ),
          ),
          _Pill(open: _open, onTap: () => setState(() => _open = !_open)),
        ],
      ),
    );
  }

  Widget _body(NekkoTokens t) {
    return Container(
      margin: const EdgeInsets.fromLTRB(16, 8, 16, 8),
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: t.surface,
        border: Border.all(color: t.border),
        borderRadius: BorderRadius.circular(t.radius),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          _tabs(t),
          const SizedBox(height: 10),
          _adder(t),
          const SizedBox(height: 6),
          Text(
            _hints[_tab] ?? '',
            style: TextStyle(color: t.textFaint, fontSize: 11),
          ),
        ],
      ),
    );
  }

  Widget _tabs(NekkoTokens t) {
    return Row(
      children: [
        for (final k in const ['domains', 'ips', 'processes']) ...[
          Expanded(
            child: GestureDetector(
              onTap: () => setState(() {
                _tab = k;
                _input.clear();
              }),
              behavior: HitTestBehavior.opaque,
              child: Container(
                padding: const EdgeInsets.symmetric(vertical: 8),
                decoration: BoxDecoration(
                  color: _tab == k ? t.surface2 : Colors.transparent,
                  borderRadius: BorderRadius.circular(8),
                  border: Border.all(
                    color: _tab == k ? t.borderStrong : Colors.transparent,
                  ),
                ),
                child: Center(
                  child: Text(
                    _labels[k]!,
                    style: TextStyle(
                      color: _tab == k ? t.text : t.textMuted,
                      fontSize: 12,
                      fontWeight: FontWeight.w500,
                    ),
                  ),
                ),
              ),
            ),
          ),
          if (k != 'processes') const SizedBox(width: 6),
        ],
      ],
    );
  }

  Widget _adder(NekkoTokens t) {
    return Row(
      children: [
        Expanded(
          child: TextField(
            controller: _input,
            style: TextStyle(
              color: t.text,
              fontFamily: t.monoFamily,
              fontSize: 13,
            ),
            decoration: InputDecoration(
              hintText: _placeholders[_tab],
              isDense: true,
            ),
            onSubmitted: (_) => _add(),
          ),
        ),
        const SizedBox(width: 8),
        SizedBox(
          width: 40,
          height: 40,
          child: FilledButton(
            onPressed: _add,
            style: FilledButton.styleFrom(
              padding: EdgeInsets.zero,
              backgroundColor: t.surface2,
              foregroundColor: t.text,
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(t.radius),
                side: BorderSide(color: t.border),
              ),
            ),
            child: const Icon(Icons.add, size: 18),
          ),
        ),
      ],
    );
  }

  Widget _itemsBlock(NekkoTokens t, List<String> items) {
    return Container(
      margin: const EdgeInsets.fromLTRB(16, 0, 16, 8),
      decoration: BoxDecoration(
        color: t.surface,
        border: Border.all(color: t.border),
        borderRadius: BorderRadius.circular(t.radius),
      ),
      child: Column(
        children: [
          for (var i = 0; i < items.length; i++) ...[
            if (i > 0) Divider(height: 1, color: t.border),
            _ItemRow(value: items[i], onRemove: () => _remove(items[i])),
          ],
        ],
      ),
    );
  }
}

class _Pill extends StatelessWidget {
  final bool open;
  final VoidCallback onTap;
  const _Pill({required this.open, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return GestureDetector(
      onTap: onTap,
      behavior: HitTestBehavior.opaque,
      child: AnimatedContainer(
        duration: const Duration(milliseconds: 200),
        width: 36,
        height: 22,
        padding: const EdgeInsets.all(2),
        decoration: BoxDecoration(
          color: open ? t.ok.withValues(alpha: 0.25) : t.surface2,
          border: Border.all(color: open ? t.ok : t.border),
          borderRadius: BorderRadius.circular(20),
        ),
        child: AnimatedAlign(
          duration: const Duration(milliseconds: 200),
          curve: Curves.easeOut,
          alignment: open ? Alignment.centerRight : Alignment.centerLeft,
          child: Container(
            width: 16,
            height: 16,
            decoration: BoxDecoration(
              color: open ? t.ok : t.textMuted,
              shape: BoxShape.circle,
            ),
          ),
        ),
      ),
    );
  }
}

class _ItemRow extends StatelessWidget {
  final String value;
  final VoidCallback onRemove;
  const _ItemRow({required this.value, required this.onRemove});

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 8, 6, 8),
      child: Row(
        children: [
          Expanded(
            child: Tooltip(
              message: value,
              child: Text(
                value,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(
                  color: t.text,
                  fontFamily: t.monoFamily,
                  fontSize: 12,
                ),
              ),
            ),
          ),
          IconButton(
            onPressed: onRemove,
            icon: const Icon(Icons.close, size: 16),
            color: t.textMuted,
            visualDensity: VisualDensity.compact,
            tooltip: 'удалить',
          ),
        ],
      ),
    );
  }
}
