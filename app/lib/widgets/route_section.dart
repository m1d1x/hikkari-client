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

    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          // Single card holds header + (optionally) body. Matches the
          // visual unity of the Svelte original — toggle and tabs feel
          // like one component, not two stacked cards.
          Container(
            decoration: BoxDecoration(
              color: t.surface,
              border: Border.all(color: t.border),
              borderRadius: BorderRadius.circular(t.radiusLg),
            ),
            clipBehavior: Clip.antiAlias,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                _head(t),
                AnimatedSize(
                  duration: const Duration(milliseconds: 200),
                  curve: Curves.easeOut,
                  alignment: Alignment.topCenter,
                  child:
                      _open ? _body(t) : const SizedBox.shrink(),
                ),
              ],
            ),
          ),
          if (_open && items.isNotEmpty) ...[
            const SizedBox(height: 8),
            _itemsBlock(t, items),
          ],
        ],
      ),
    );
  }

  Widget _head(NekkoTokens t) {
    return InkWell(
      onTap: () => setState(() => _open = !_open),
      child: Padding(
        padding: const EdgeInsets.fromLTRB(16, 14, 12, 14),
        child: Row(
          children: [
            Expanded(
              child: Text(
                'маршрутизация',
                style: TextStyle(
                  color: t.text,
                  fontSize: 14,
                  letterSpacing: 0.3,
                  fontWeight: FontWeight.w500,
                ),
              ),
            ),
            _IosSwitch(
              value: _open,
              onChanged: (v) => setState(() => _open = v),
            ),
          ],
        ),
      ),
    );
  }

  Widget _body(NekkoTokens t) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 0, 12, 14),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          _tabs(t),
          const SizedBox(height: 12),
          _adder(t),
          const SizedBox(height: 8),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 4),
            child: Text(
              _hints[_tab] ?? '',
              style: TextStyle(
                color: t.textFaint,
                fontSize: 12,
                fontFamily: t.monoFamily,
                height: 1.4,
              ),
            ),
          ),
        ],
      ),
    );
  }

  // Pill tabs. Active = filled darker surface + strong border, label in
  // primary text colour. Inactive = transparent, no border, muted label —
  // matches the look in the reference screenshot.
  Widget _tabs(NekkoTokens t) {
    return Container(
      padding: const EdgeInsets.all(4),
      decoration: BoxDecoration(
        color: t.surface2,
        borderRadius: BorderRadius.circular(t.radius),
      ),
      child: Row(
        children: [
          for (final k in const ['domains', 'ips', 'processes'])
            Expanded(
              child: _TabPill(
                label: _labels[k]!,
                active: _tab == k,
                onTap: () => setState(() {
                  _tab = k;
                  _input.clear();
                }),
              ),
            ),
        ],
      ),
    );
  }

  Widget _adder(NekkoTokens t) {
    return Row(
      children: [
        Expanded(
          child: Container(
            decoration: BoxDecoration(
              color: t.surface2,
              borderRadius: BorderRadius.circular(t.radius),
            ),
            child: TextField(
              controller: _input,
              style: TextStyle(
                color: t.text,
                fontFamily: t.monoFamily,
                fontSize: 14,
              ),
              cursorColor: t.text,
              decoration: InputDecoration(
                hintText: _placeholders[_tab],
                hintStyle: TextStyle(
                  color: t.textFaint,
                  fontFamily: t.monoFamily,
                  fontSize: 14,
                ),
                filled: false,
                border: InputBorder.none,
                enabledBorder: InputBorder.none,
                focusedBorder: InputBorder.none,
                isDense: true,
                contentPadding:
                    const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
              ),
              onSubmitted: (_) => _add(),
            ),
          ),
        ),
        const SizedBox(width: 8),
        _AddButton(onTap: _add),
      ],
    );
  }

  Widget _itemsBlock(NekkoTokens t, List<String> items) {
    return Container(
      decoration: BoxDecoration(
        color: t.surface,
        border: Border.all(color: t.border),
        borderRadius: BorderRadius.circular(t.radiusLg),
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

/// iOS-style toggle. Bigger than the Material Switch, no Material chrome,
/// matches the look of the Svelte `.switch` element.
class _IosSwitch extends StatelessWidget {
  final bool value;
  final ValueChanged<bool> onChanged;
  const _IosSwitch({required this.value, required this.onChanged});

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return GestureDetector(
      onTap: () => onChanged(!value),
      behavior: HitTestBehavior.opaque,
      child: AnimatedContainer(
        duration: const Duration(milliseconds: 200),
        curve: Curves.easeOut,
        width: 50,
        height: 30,
        padding: const EdgeInsets.all(3),
        decoration: BoxDecoration(
          color: value ? t.ok : t.surface2,
          borderRadius: BorderRadius.circular(20),
          border: Border.all(
            color: value ? Colors.transparent : t.border,
          ),
        ),
        child: AnimatedAlign(
          duration: const Duration(milliseconds: 200),
          curve: Curves.easeOut,
          alignment: value ? Alignment.centerRight : Alignment.centerLeft,
          child: Container(
            width: 22,
            height: 22,
            decoration: const BoxDecoration(
              color: Colors.white,
              shape: BoxShape.circle,
              boxShadow: [
                BoxShadow(
                  color: Color(0x33000000),
                  blurRadius: 4,
                  offset: Offset(0, 1),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _TabPill extends StatelessWidget {
  final String label;
  final bool active;
  final VoidCallback onTap;
  const _TabPill({
    required this.label,
    required this.active,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return GestureDetector(
      onTap: onTap,
      behavior: HitTestBehavior.opaque,
      child: AnimatedContainer(
        duration: const Duration(milliseconds: 180),
        padding: const EdgeInsets.symmetric(vertical: 10),
        decoration: BoxDecoration(
          color: active ? t.surface : Colors.transparent,
          borderRadius: BorderRadius.circular(t.radius - 2),
          border: Border.all(
            color: active ? t.borderStrong : Colors.transparent,
          ),
        ),
        child: Center(
          child: Text(
            label,
            style: TextStyle(
              color: active ? t.text : t.textMuted,
              fontSize: 13,
              fontWeight: active ? FontWeight.w600 : FontWeight.w500,
            ),
          ),
        ),
      ),
    );
  }
}

class _AddButton extends StatelessWidget {
  final VoidCallback onTap;
  const _AddButton({required this.onTap});

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return SizedBox(
      width: 46,
      height: 46,
      child: Material(
        color: t.surface2,
        borderRadius: BorderRadius.circular(t.radius),
        clipBehavior: Clip.antiAlias,
        child: InkWell(
          onTap: onTap,
          child: Icon(Icons.add, color: t.text, size: 20),
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
      padding: const EdgeInsets.fromLTRB(14, 10, 8, 10),
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
                  fontSize: 13,
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
