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
  // place so a future translation is a single-file edit. All-lowercase on
  // purpose: matches the Svelte global text-transform: lowercase rule that
  // gives the original UI its compact look.
  static const _labels = {
    'domains': 'домены',
    'ips': 'ip',
    'processes': 'приложения',
  };
  static const _placeholders = {
    'domains': 'example.com',
    'ips': '192.168.0.0/16',
    'processes': 'discord.exe',
  };
  static const _hints = {
    'domains':
        'example.com = все поддомены тоже. api.example.com = только этот',
    'ips': 'один ip или подсеть в cidr-нотации',
    'processes': 'имя .exe — весь трафик процесса пойдёт мимо vpn',
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

  // .route-head: padding 14 16, title 13/500.
  Widget _head(NekkoTokens t) {
    return InkWell(
      onTap: () => setState(() => _open = !_open),
      mouseCursor: SystemMouseCursors.click,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
        child: Row(
          children: [
            Expanded(
              child: Text(
                'маршрутизация',
                style: TextStyle(
                  color: t.text,
                  fontSize: 13,
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

  // .route-body: padding 0 12 12, gap 10.
  Widget _body(NekkoTokens t) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 0, 12, 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          _tabs(t),
          const SizedBox(height: 10),
          _adder(t),
          const SizedBox(height: 10),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 2),
            child: Text(
              _hints[_tab] ?? '',
              style: TextStyle(
                color: t.textFaint,
                fontSize: 11,
                letterSpacing: 0.02 * 11,
                height: 1.4,
              ),
            ),
          ),
        ],
      ),
    );
  }

  // Tabs strip — visible track in page-bg colour (darker than the card),
  // with the active tab filled in surface-2 (slightly lighter than the
  // card). Exact port of `.tabs` from App.svelte#690-703.
  Widget _tabs(NekkoTokens t) {
    return Container(
      height: 40,
      padding: const EdgeInsets.all(4),
      decoration: BoxDecoration(
        color: t.bg,
        border: Border.all(color: t.border),
        borderRadius: BorderRadius.circular(t.radius),
      ),
      child: Row(
        children: [
          for (var i = 0; i < 3; i++) ...[
            if (i > 0) const SizedBox(width: 4),
            Expanded(
              child: _TabPill(
                tab: const ['domains', 'processes', 'ips'][i],
                label: _labels[const ['domains', 'processes', 'ips'][i]]!,
                active: _tab == const ['domains', 'processes', 'ips'][i],
                onTap: () => setState(() {
                  _tab = const ['domains', 'processes', 'ips'][i];
                  _input.clear();
                }),
              ),
            ),
          ],
        ],
      ),
    );
  }

  // .adder ports App.svelte#705-733: input and + button both 40px tall,
  // bg = page bg (darker than card), thin border. 6px gap between them.
  Widget _adder(NekkoTokens t) {
    return Row(
      children: [
        Expanded(
          child: _RouteInput(
            controller: _input,
            placeholder: _placeholders[_tab] ?? '',
            onSubmitted: (_) => _add(),
          ),
        ),
        const SizedBox(width: 6),
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

class _RouteInput extends StatefulWidget {
  final TextEditingController controller;
  final String placeholder;
  final ValueChanged<String> onSubmitted;

  const _RouteInput({
    required this.controller,
    required this.placeholder,
    required this.onSubmitted,
  });

  @override
  State<_RouteInput> createState() => _RouteInputState();
}

class _RouteInputState extends State<_RouteInput> {
  final _focusNode = FocusNode();

  @override
  void dispose() {
    _focusNode.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final textStyle = TextStyle(
      color: t.text,
      fontFamily: t.monoFamily,
      fontSize: 12,
      fontWeight: FontWeight.w500,
      height: 1.0,
      letterSpacing: 0.02 * 12,
    );

    return GestureDetector(
      behavior: HitTestBehavior.opaque,
      onTap: _focusNode.requestFocus,
      child: Container(
        height: 40,
        padding: const EdgeInsets.symmetric(horizontal: 14),
        decoration: BoxDecoration(
          color: t.bg,
          border: Border.all(color: t.border),
          borderRadius: BorderRadius.circular(t.radius),
        ),
        child: Center(
          child: SizedBox(
            width: double.infinity,
            height: 18,
            child: Stack(
              alignment: Alignment.centerLeft,
              children: [
                ValueListenableBuilder<TextEditingValue>(
                  valueListenable: widget.controller,
                  builder: (context, value, _) {
                    if (value.text.isNotEmpty) return const SizedBox.shrink();
                    return IgnorePointer(
                      child: Text(
                        widget.placeholder,
                        maxLines: 1,
                        overflow: TextOverflow.clip,
                        style: textStyle.copyWith(color: t.textFaint),
                      ),
                    );
                  },
                ),
                Positioned.fill(
                  child: EditableText(
                    controller: widget.controller,
                    focusNode: _focusNode,
                    style: textStyle,
                    cursorColor: t.text,
                    backgroundCursorColor: t.textFaint,
                    maxLines: 1,
                    textInputAction: TextInputAction.done,
                    onSubmitted: widget.onSubmitted,
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

/// iOS-style toggle ported from `.switch` in App.svelte#674-688:
/// 46×28 track, 24×24 knob, surface-2 off / --ok on, #ddd off-knob / #fff on.
class _IosSwitch extends StatelessWidget {
  final bool value;
  final ValueChanged<bool> onChanged;
  const _IosSwitch({required this.value, required this.onChanged});

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return MouseRegion(
      cursor: SystemMouseCursors.click,
      child: GestureDetector(
        onTap: () => onChanged(!value),
        behavior: HitTestBehavior.opaque,
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 250),
          curve: Curves.easeOut,
          width: 46,
          height: 28,
          padding: const EdgeInsets.all(2),
          decoration: BoxDecoration(
            color: value ? t.ok : t.surface2,
            borderRadius: BorderRadius.circular(999),
          ),
          child: AnimatedAlign(
            duration: const Duration(milliseconds: 250),
            curve: Curves.easeOut,
            alignment: value ? Alignment.centerRight : Alignment.centerLeft,
            child: Container(
              width: 24,
              height: 24,
              decoration: BoxDecoration(
                color: value ? Colors.white : const Color(0xFFDDDDDD),
                shape: BoxShape.circle,
                boxShadow: const [
                  BoxShadow(
                    color: Color(0x4D000000),
                    blurRadius: 2,
                    offset: Offset(0, 1),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class _TabPill extends StatelessWidget {
  final String tab;
  final String label;
  final bool active;
  final VoidCallback onTap;
  const _TabPill({
    required this.tab,
    required this.label,
    required this.active,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return MouseRegion(
      cursor: SystemMouseCursors.click,
      child: GestureDetector(
        onTap: onTap,
        behavior: HitTestBehavior.opaque,
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 150),
          decoration: BoxDecoration(
            // Active = surface-2 fill, no border (matches CSS exactly).
            color: active ? t.surface2 : Colors.transparent,
            borderRadius: BorderRadius.circular(10),
          ),
          child: Center(
            child: Text(
              label,
              style: TextStyle(
                color: active ? t.text : t.textMuted,
                fontSize: 12,
                fontWeight: FontWeight.w500,
                height: 1,
              ),
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
    return MouseRegion(
      cursor: SystemMouseCursors.click,
      child: GestureDetector(
        onTap: onTap,
        behavior: HitTestBehavior.opaque,
        child: Container(
          width: 40,
          height: 40,
          alignment: Alignment.center,
          decoration: BoxDecoration(
            color: t.bg,
            border: Border.all(color: t.border),
            borderRadius: BorderRadius.circular(t.radius),
          ),
          child: Icon(Icons.add, color: t.textMuted, size: 22),
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
