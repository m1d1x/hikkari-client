import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../core/models.dart';
import '../state/app_state.dart';
import '../theme/tokens.dart';

/// "Маршрутизация" section — master toggle + three category tabs + the
/// items list. Layout is a single card whose body collapses; expand state
/// is local (where the user is in the UI), but everything data-shaped
/// (enabled flag, mode, lists) lives in [AppState.routing]. The whitelist
/// vs. blacklist mode picker ([_ModeFlipButton]/[_ModeCaption]) has moved
/// to the settings sheet ([RouteSettingsSection]) — this section only
/// shows a tooltip on the header explaining whichever mode is active.
class RouteSection extends StatefulWidget {
  const RouteSection({super.key});

  @override
  State<RouteSection> createState() => _RouteSectionState();
}

class _RouteSectionState extends State<RouteSection> {
  bool _open = false;
  String _tab = 'domains';
  final _input = TextEditingController();

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
    'processes': 'имя .exe — весь трафик процесса',
  };

  @override
  void dispose() {
    _input.dispose();
    super.dispose();
  }

  List<String> _itemsFor(String tab, RouteList list) {
    switch (tab) {
      case 'domains':
        return list.domains;
      case 'ips':
        return list.ips;
      default:
        return list.processes;
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

  static String _tooltipFor(RoutingState routing) {
    if (!routing.enabled) {
      return 'маршрутизация выключена — весь трафик идёт через vpn';
    }
    return routing.mode == RoutingMode.bypass
        ? 'через vpn идёт всё, кроме того что в списке'
        : 'через vpn идёт только то, что в списке';
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final s = context.watch<AppState>();
    final items = _itemsFor(_tab, s.routing.active);

    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
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
                _head(t, s),
                AnimatedSize(
                  duration: const Duration(milliseconds: 200),
                  curve: Curves.easeOut,
                  alignment: Alignment.topCenter,
                  child: _open ? _body(t, s) : const SizedBox.shrink(),
                ),
              ],
            ),
          ),
          if (_open && items.isNotEmpty) ...[
            const SizedBox(height: 12),
            _itemsBlock(t, items),
          ],
        ],
      ),
    );
  }

  // Header: title + chevron + master switch. Tap anywhere on the row
  // toggles collapse — except the switch, which absorbs its own tap and
  // doesn't bubble. A tooltip over the whole row explains the active mode.
  Widget _head(NekkoTokens t, AppState s) {
    return Tooltip(
      message: _tooltipFor(s.routing),
      waitDuration: const Duration(milliseconds: 400),
      child: InkWell(
        onTap: () => setState(() => _open = !_open),
        mouseCursor: SystemMouseCursors.click,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 17),
          child: Row(
            children: [
              // Optical alignment: rounded tab pills below read as starting
              // slightly right of their true edge, so nudge the title to
              // match rather than sit flush with the container padding.
              const SizedBox(width: 3),
              Text(
                'маршрутизация',
                style: TextStyle(
                  color: t.text,
                  fontSize: 14,
                  fontWeight: FontWeight.w700,
                ),
              ),
              const SizedBox(width: 6),
              AnimatedRotation(
                duration: const Duration(milliseconds: 150),
                turns: _open ? 0.5 : 0,
                child: Icon(
                  Icons.keyboard_arrow_down,
                  size: 16,
                  color: t.textMuted,
                ),
              ),
              const SizedBox(width: 8),
              _ApplyStatusLabel(status: s.routingApply),
              const Spacer(),
              _IosSwitch(
                value: s.routing.enabled,
                onChanged: (v) => s.setRoutingEnabled(v),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _body(NekkoTokens t, AppState s) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 0, 12, 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          _tabs(t),
          const SizedBox(height: 10),
          _adder(t),
        ],
      ),
    );
  }

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

  Widget _adder(NekkoTokens t) {
    return Row(
      children: [
        Expanded(
          child: _RouteInput(
            controller: _input,
            placeholder: _placeholders[_tab] ?? '',
            hint: _hints[_tab] ?? '',
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


/// Compact status word in the header — shows what's happening with the
/// pending routing edit: waiting out the debounce window, restarting the
/// tunnel to pick the change up, or freshly applied. Empty (and takes no
/// space) once it fades back to idle.
class _ApplyStatusLabel extends StatelessWidget {
  final RoutingApplyStatus status;
  const _ApplyStatusLabel({required this.status});

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final (label, color) = switch (status) {
      RoutingApplyStatus.idle => (null, t.textMuted),
      RoutingApplyStatus.pending => ('ожидает', t.textMuted),
      RoutingApplyStatus.applying => ('применяется…', t.warn),
      RoutingApplyStatus.applied => ('обновлено', t.primary),
    };
    return AnimatedSwitcher(
      duration: const Duration(milliseconds: 180),
      transitionBuilder: (child, anim) =>
          FadeTransition(opacity: anim, child: child),
      // Default layoutBuilder stacks children centered, so a shorter label
      // fading in over a longer outgoing one visibly shifts sideways. Pin
      // everything to the left edge instead — the row's own alignment stays
      // put no matter how the label's width changes mid-transition.
      layoutBuilder: (currentChild, previousChildren) => Stack(
        alignment: Alignment.centerLeft,
        children: [
          ...previousChildren,
          ?currentChild,
        ],
      ),
      child: label == null
          ? const SizedBox.shrink(key: ValueKey('idle'))
          : Text(
              label,
              key: ValueKey(label),
              style: TextStyle(
                color: color,
                fontSize: 11,
                fontWeight: FontWeight.w500,
              ),
            ),
    );
  }
}

/// Whitelist vs. blacklist mode picker, for the settings sheet. Everything
/// else about routing (master switch, category tabs, item lists) stays on
/// the home screen in [RouteSection] — only the proxy/bypass choice and its
/// explanation moved here, since most users never need to touch it.
class RouteSettingsSection extends StatelessWidget {
  const RouteSettingsSection({super.key});

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final s = context.watch<AppState>();

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Row(
          children: [
            Text(
              'режим маршрутизации',
              style: TextStyle(
                color: t.text,
                fontSize: 13,
                fontWeight: FontWeight.w500,
              ),
            ),
            const Spacer(),
            _ModeFlipButton(state: s),
          ],
        ),
        const SizedBox(height: 8),
        _ModeCaption(state: s),
      ],
    );
  }
}

/// Passive caption: just tells the user what the current routing mode means.
/// The actual flip lives in [_ModeFlipButton] above it, so this row is
/// non-interactive — it reads as documentation that follows the button.
class _ModeCaption extends StatelessWidget {
  final AppState state;
  const _ModeCaption({required this.state});

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final dim = !state.routing.enabled;
    final sentence = !state.routing.enabled
        ? 'маршрутизация выключена — весь трафик идёт через vpn'
        : (state.routing.mode == RoutingMode.bypass
            ? 'через vpn идёт всё, кроме того что в списке'
            : 'через vpn идёт только то, что в списке');
    return AnimatedSwitcher(
      duration: const Duration(milliseconds: 140),
      transitionBuilder: (child, anim) =>
          FadeTransition(opacity: anim, child: child),
      child: Padding(
        key: ValueKey(sentence),
        padding: const EdgeInsets.symmetric(horizontal: 4),
        child: Text(
          sentence,
          style: TextStyle(
            color: dim ? t.textFaint : t.textMuted,
            fontSize: 12,
            height: 1.35,
          ),
        ),
      ),
    );
  }
}

/// Mode-flip button. Compact pill that swaps proxy↔bypass on tap. Disabled
/// (and dimmed) when master is off.
class _ModeFlipButton extends StatefulWidget {
  final AppState state;
  const _ModeFlipButton({required this.state});

  @override
  State<_ModeFlipButton> createState() => _ModeFlipButtonState();
}

class _ModeFlipButtonState extends State<_ModeFlipButton> {
  bool _hover = false;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final s = widget.state;
    final dim = !s.routing.enabled;
    final nextMode = s.routing.mode == RoutingMode.bypass
        ? RoutingMode.proxy
        : RoutingMode.bypass;
    final tooltipText = s.routing.mode == RoutingMode.bypass
        ? 'через vpn пойдёт ТОЛЬКО список'
        : 'через vpn пойдёт ВСЁ, кроме списка';
    final label = s.routing.mode == RoutingMode.bypass ? 'bypass' : 'proxy';

    // ⬇⬇⬇ ПЕСОЧНИЦА — крути эти строки, жми `r` в flutter run, смотри что вышло.
    final bg = dim
        ? Colors.transparent
        : (_hover
            ? t.primary.withValues(alpha: 0.40)
            : t.primary.withValues(alpha: 0.15));
    final fg = dim ? t.textFaint : (_hover ? t.text : t.textMuted);
    // ⬆⬆⬆

    return Opacity(
      opacity: dim ? 0.5 : 1,
      child: Tooltip(
        message: dim ? '' : tooltipText,
        waitDuration: const Duration(milliseconds: 500),
        child: MouseRegion(
          cursor: dim
              ? SystemMouseCursors.basic
              : SystemMouseCursors.click,
          onEnter: (_) {
            if (!dim) setState(() => _hover = true);
          },
          onExit: (_) => setState(() => _hover = false),
          child: GestureDetector(
            behavior: HitTestBehavior.opaque,
            onTap: dim ? null : () => s.setRoutingMode(nextMode),
            child: AnimatedContainer(
              duration: const Duration(milliseconds: 120),
              height: 28,
              padding:
                  const EdgeInsets.symmetric(horizontal: 10, vertical: 0),
              decoration: BoxDecoration(
                color: bg,
                borderRadius: BorderRadius.circular(t.radius),
              ),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  AnimatedSwitcher(
                    duration: const Duration(milliseconds: 140),
                    transitionBuilder: (child, anim) =>
                        FadeTransition(opacity: anim, child: child),
                    child: Text(
                      label,
                      key: ValueKey(label),
                      style: TextStyle(
                        color: fg,
                        fontSize: 12,
                        fontWeight: FontWeight.w500,
                        height: 1,
                      ),
                    ),
                  ),
                  const SizedBox(width: 6),
                  Icon(Icons.swap_horiz, size: 16, color: fg),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class _RouteInput extends StatefulWidget {
  final TextEditingController controller;
  final String placeholder;
  final String hint;
  final ValueChanged<String> onSubmitted;

  const _RouteInput({
    required this.controller,
    required this.placeholder,
    required this.hint,
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
      letterSpacing: 0.02 * 12,
    );

    return Tooltip(
      message: widget.hint,
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
            controller: widget.controller,
            focusNode: _focusNode,
            style: textStyle,
            cursorColor: t.text,
            cursorWidth: 1.5,
            cursorRadius: const Radius.circular(1),
            maxLines: 1,
            textInputAction: TextInputAction.done,
            onSubmitted: widget.onSubmitted,
            decoration: InputDecoration(
              isDense: true,
              filled: true,
              fillColor: Colors.transparent,
              hoverColor: Colors.transparent,
              focusColor: Colors.transparent,
              contentPadding:
                  const EdgeInsets.fromLTRB(14, 13, 14, 9),
              border: InputBorder.none,
              enabledBorder: InputBorder.none,
              focusedBorder: InputBorder.none,
              hintText: widget.placeholder,
              hintStyle: textStyle.copyWith(color: t.textFaint),
            ),
          ),
        ),
      ),
    );
  }
}

/// iOS-style toggle. 46×28 track, 24×24 knob.
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
            color: value ? t.primary : t.surface2,
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
      padding: const EdgeInsets.fromLTRB(14, 10, 12, 10),
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
