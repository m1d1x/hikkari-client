import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../core/models.dart';
import '../state/app_state.dart';
import '../theme/tokens.dart';
import '../ui/nekko_segmented.dart';
import '../ui/nekko_animated_height.dart';
import '../ui/nekko_text_field.dart';
import 'app_picker_dialog.dart';
import 'service_presets.dart';

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

class _RouteSectionState extends State<RouteSection>
    with SingleTickerProviderStateMixin {
  late final AnimationController _expandController;
  bool _open = false;
  bool _headerHover = false;
  bool _presetsHover = false;
  bool _presetMenuOpen = false;
  String _tab = 'domains';
  final _input = TextEditingController();

  static const _placeholders = {
    'domains': 'example.com',
    'ips': '192.168.0.0/16',
  };
  static const _hints = {
    'domains':
        'example.com = все поддомены тоже. api.example.com = только этот',
    'ips': 'один ip или подсеть в cidr-нотации',
  };

  @override
  void initState() {
    super.initState();
    _expandController = AnimationController(
      duration: AppDurations.routingExpand,
      vsync: this,
    );
  }

  @override
  void dispose() {
    _expandController.dispose();
    _input.dispose();
    super.dispose();
  }

  void _toggleOpen() => _setOpen(!_open);

  void _setOpen(bool value) {
    if (_open == value) return;
    setState(() => _open = value);
    // Animate the visible factor itself. Retargeting starts at the current
    // height, including when a click interrupts the previous transition.
    // Scroll bounds follow layout; a competing animateTo would fight them.
    _expandController.animateTo(
      _open ? 1 : 0,
      duration: AppDurations.routingExpand,
      curve: AppCurves.fastInSlowOut,
    );
  }

  void _togglePresetMenu() {
    if (!_open) _setOpen(true);
    setState(() => _presetMenuOpen = !_presetMenuOpen);
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

  Future<void> _pickApp() async {
    final items = _itemsFor(
      'processes',
      context.read<AppState>().routing.active,
    );
    final exe = await AppPickerDialog.show(
      context,
      alreadyAdded: items.map((e) => e.toLowerCase()).toSet(),
    );
    if (exe != null && mounted) {
      await context.read<AppState>().addRule('processes', exe);
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final s = context.watch<AppState>();
    final items = _itemsFor(_tab, s.routing.active);

    final reversedItems = items.reversed.toList();

    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
      child: TapRegion(
        enabled: _open,
        behavior: HitTestBehavior.opaque,
        onTapOutside: (_) => _setOpen(false),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          mainAxisSize: MainAxisSize.min,
          children: [
            _island(
              t,
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [_head(t, s), _reveal(_tabHeight(_body(t, s)))],
              ),
            ),
            _itemsBlock(t, reversedItems),
            _expandingIsland(
              t,
              key: const ValueKey('route_presets_surface'),
              child: _tabHeight(ServicePresets(category: _tab)),
            ),
          ],
        ),
      ),
    );
  }

  Widget _tabHeight(Widget child) => NekkoAnimatedHeight(
    contentKey: _tab,
    enabled: _open,
    duration: AppDurations.routingDetails,
    curve: AppCurves.fastInSlowOut,
    child: child,
  );

  Widget _reveal(Widget child) => AnimatedBuilder(
    animation: _expandController,
    child: FadeTransition(opacity: _expandController, child: child),
    builder: (context, child) => Offstage(
      offstage: _expandController.value == 0,
      child: ClipRect(
        child: Align(
          alignment: Alignment.bottomCenter,
          heightFactor: _expandController.value,
          child: child,
        ),
      ),
    ),
  );

  // Animate the surface with its contents: rounded borders follow the current
  // card height, rather than being cut off by a mask over the entire section.
  Widget _expandingIsland(
    NekkoTokens t, {
    Key? key,
    required Widget child,
  }) => AnimatedBuilder(
    animation: _expandController,
    // Reserve the final border inset before revealing. Animating a
    // decoration border also animates padding and can rewrap text mid-flight.
    child: _reveal(Padding(padding: const EdgeInsets.all(1), child: child)),
    builder: (context, child) {
      final progress = _expandController.value;
      return Padding(
        padding: EdgeInsets.only(top: 12 * progress),
        child: Offstage(
          offstage: progress == 0,
          child: Container(
            key: key,
            decoration: BoxDecoration(
              color: t.surface,
              borderRadius: BorderRadius.circular(t.radiusLg),
            ),
            foregroundDecoration: BoxDecoration(
              border: Border.all(
                color: t.border.withValues(alpha: t.border.a * progress),
              ),
              borderRadius: BorderRadius.circular(t.radiusLg),
            ),
            clipBehavior: Clip.antiAlias,
            child: child,
          ),
        ),
      );
    },
  );

  // Header: title + chevron + master switch. Tap anywhere on the row
  // toggles collapse — except the action and switch, which absorb their taps.
  Widget _head(NekkoTokens t, AppState s) {
    return MouseRegion(
      onEnter: (_) => setState(() => _headerHover = true),
      onExit: (_) => setState(() => _headerHover = false),
      child: InkWell(
        onTap: _toggleOpen,
        mouseCursor: SystemMouseCursors.click,
        hoverColor: Colors.transparent,
        splashColor: Colors.transparent,
        highlightColor: Colors.transparent,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 17),
          child: Row(
            children: [
              const SizedBox(width: 3),
              AnimatedDefaultTextStyle(
                duration: AppDurations.fast,
                style: t.sectionTitle.copyWith(
                  color: _headerHover ? t.primary : t.text,
                ),
                child: const Text('маршрутизация'),
              ),
              const SizedBox(width: 6),
              AnimatedRotation(
                duration: AppDurations.routingExpand,
                curve: _open
                    ? AppCurves.fastInSlowOut
                    : AppCurves.fastInSlowOut.flipped,
                turns: _open ? 0.5 : 0,
                child: TweenAnimationBuilder<Color?>(
                  tween: ColorTween(
                    end: _headerHover ? t.primary : t.textMuted,
                  ),
                  duration: AppDurations.fast,
                  builder: (_, color, __) =>
                      Icon(Icons.keyboard_arrow_down, size: 16, color: color),
                ),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: Tooltip(
                  message: s.routingApplyError ?? '',
                  child: Align(
                    alignment: Alignment.centerLeft,
                    child: _ApplyStatusLabel(status: s.routingApply),
                  ),
                ),
              ),
              Tooltip(
                message: 'импорт и экспорт пресетов',
                child: MouseRegion(
                  cursor: SystemMouseCursors.click,
                  onEnter: (_) => setState(() => _presetsHover = true),
                  onExit: (_) => setState(() => _presetsHover = false),
                  child: GestureDetector(
                    key: const ValueKey('routing_presets_button'),
                    behavior: HitTestBehavior.opaque,
                    onTap: _togglePresetMenu,
                    child: SizedBox(
                      width: 28,
                      height: 28,
                      child: TweenAnimationBuilder<Color?>(
                        tween: ColorTween(
                          end: _presetsHover || _presetMenuOpen
                              ? t.primary
                              : t.textMuted,
                        ),
                        duration: AppDurations.fast,
                        builder: (_, color, _) =>
                            Icon(Icons.more_horiz, size: 20, color: color),
                      ),
                    ),
                  ),
                ),
              ),
              const SizedBox(width: 10),
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
          _presetMenu(t),
          _tabs(t),
          const SizedBox(height: 10),
          _adder(t),
        ],
      ),
    );
  }

  Widget _presetMenu(NekkoTokens t) => TweenAnimationBuilder<double>(
    tween: Tween(begin: 0, end: _presetMenuOpen ? 1 : 0),
    duration: AppDurations.routingDetails,
    curve: AppCurves.fastInSlowOut,
    builder: (_, progress, child) => Offstage(
      offstage: progress == 0,
      child: IgnorePointer(
        ignoring: !_presetMenuOpen,
        child: ClipRect(
          child: Align(
            alignment: Alignment.topCenter,
            heightFactor: progress,
            child: Opacity(
              opacity: progress,
              child: Transform.translate(
                offset: Offset(0, -8 * (1 - progress)),
                child: child,
              ),
            ),
          ),
        ),
      ),
    ),
    child: Padding(
      padding: const EdgeInsets.only(bottom: 10),
      child: Row(
        key: const ValueKey('routing_preset_actions'),
        children: const [
          Expanded(
            child: _PresetActionPill(
              label: 'импорт',
              icon: Icons.south_rounded,
            ),
          ),
          SizedBox(width: 6),
          Expanded(
            child: _PresetActionPill(
              label: 'экспорт',
              icon: Icons.north_rounded,
            ),
          ),
        ],
      ),
    ),
  );

  Widget _tabs(NekkoTokens t) {
    return NekkoSegmented(
      selected: _tab,
      onChanged: (v) => setState(() {
        _tab = v;
        _input.clear();
      }),
      segments: const [
        NekkoSegment('domains', 'домены'),
        NekkoSegment('processes', 'приложения'),
        NekkoSegment('ips', 'ip'),
      ],
    );
  }

  Widget _adder(NekkoTokens t) {
    if (_tab == 'processes') {
      return _ProcessAddButton(onTap: _pickApp);
    }
    return Row(
      children: [
        Expanded(
          child: NekkoTextField(
            controller: _input,
            hint: _placeholders[_tab] ?? '',
            tooltip: _hints[_tab] ?? '',
            textInputAction: TextInputAction.done,
            onSubmitted: (_) => _add(),
          ),
        ),
        const SizedBox(width: 6),
        _AddButton(onTap: _add),
      ],
    );
  }

  Widget _island(NekkoTokens t, {required Widget child}) => Container(
    decoration: BoxDecoration(
      color: t.surface,
      border: Border.all(color: t.border),
      borderRadius: BorderRadius.circular(t.radiusLg),
    ),
    clipBehavior: Clip.antiAlias,
    child: child,
  );

  Widget _itemsBlock(NekkoTokens t, List<String> items) {
    return _expandingIsland(
      t,
      key: const ValueKey('route_manual_surface'),
      child: _tabHeight(
        Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 11),
              child: Text(
                'ручные',
                textAlign: TextAlign.center,
                style: TextStyle(
                  color: t.textSecondary,
                  fontSize: 14,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ),
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 12),
              child: Divider(height: 1, thickness: 1, color: t.border),
            ),
            if (items.isEmpty)
              Padding(
                padding: const EdgeInsets.symmetric(
                  horizontal: 14,
                  vertical: 15,
                ),
                child: Text(
                  'добавьте домен, приложение или ip выше',
                  textAlign: TextAlign.center,
                  style: TextStyle(color: t.textFaint, fontSize: 11),
                ),
              ),
            for (var i = 0; i < items.length; i++) ...[
              if (i > 0) Divider(height: 1, thickness: 1, color: t.border),
              _ItemRow(value: items[i], onRemove: () => _remove(items[i])),
            ],
          ],
        ),
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
      RoutingApplyStatus.failed => ('не применено', t.bad),
      RoutingApplyStatus.preview => ('предпросмотр', t.textMuted),
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
        children: [...previousChildren, ?currentChild],
      ),
      child: label == null
          ? const SizedBox.shrink(key: ValueKey('idle'))
          : Text(
              label,
              key: ValueKey(label),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
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
  final ValueChanged<String?>? onHintChanged;
  const RouteSettingsSection({super.key, this.onHintChanged});

  @override
  Widget build(BuildContext context) {
    final s = context.watch<AppState>();

    return _ModeCaption(state: s, onHintChanged: onHintChanged);
  }
}

/// The whole setting cell owns hover; its explanation follows the controls.
class _ModeCaption extends StatefulWidget {
  final AppState state;
  final ValueChanged<String?>? onHintChanged;
  const _ModeCaption({required this.state, required this.onHintChanged});

  @override
  State<_ModeCaption> createState() => _ModeCaptionState();
}

class _ModeCaptionState extends State<_ModeCaption> {
  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final state = widget.state;
    final sentence = !state.routing.enabled
        ? 'маршрутизация выключена — весь трафик идёт через vpn'
        : (state.routing.mode == RoutingMode.bypass
              ? 'через vpn идёт всё, кроме того что в списке'
              : 'через vpn идёт только то, что в списке');
    return MouseRegion(
      onEnter: (_) => widget.onHintChanged?.call(sentence),
      onExit: (_) => widget.onHintChanged?.call(null),
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 2),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              children: [
                Expanded(
                  child: Text(
                    'режим маршрутизации',
                    style: TextStyle(
                      color: t.text,
                      fontSize: 14,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ),
                _ModeFlipButton(state: state),
              ],
            ),
          ],
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
      child: MouseRegion(
        cursor: dim ? SystemMouseCursors.basic : SystemMouseCursors.click,
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
            padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 0),
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

class _ProcessAddButton extends StatefulWidget {
  final VoidCallback onTap;
  const _ProcessAddButton({required this.onTap});

  @override
  State<_ProcessAddButton> createState() => _ProcessAddButtonState();
}

class _ProcessAddButtonState extends State<_ProcessAddButton> {
  bool _hover = false;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return MouseRegion(
      cursor: SystemMouseCursors.click,
      onEnter: (_) => setState(() => _hover = true),
      onExit: (_) => setState(() => _hover = false),
      child: GestureDetector(
        onTap: widget.onTap,
        behavior: HitTestBehavior.opaque,
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 150),
          height: 40,
          decoration: BoxDecoration(
            color: t.bg,
            border: Border.all(color: t.border),
            borderRadius: BorderRadius.circular(t.radius),
          ),
          child: Row(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              TweenAnimationBuilder<Color?>(
                tween: ColorTween(end: _hover ? t.primary : t.textMuted),
                duration: const Duration(milliseconds: 150),
                builder: (_, color, __) =>
                    Icon(Icons.add, color: color, size: 18),
              ),
              const SizedBox(width: 6),
              TweenAnimationBuilder<Color?>(
                tween: ColorTween(end: _hover ? t.primary : t.textMuted),
                duration: const Duration(milliseconds: 150),
                builder: (_, color, __) => Text(
                  'добавить приложение',
                  style: TextStyle(
                    color: color,
                    fontSize: 12,
                    fontWeight: FontWeight.w500,
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _AddButton extends StatefulWidget {
  final VoidCallback onTap;
  const _AddButton({required this.onTap});

  @override
  State<_AddButton> createState() => _AddButtonState();
}

class _AddButtonState extends State<_AddButton> {
  bool _hover = false;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return MouseRegion(
      cursor: SystemMouseCursors.click,
      onEnter: (_) => setState(() => _hover = true),
      onExit: (_) => setState(() => _hover = false),
      child: GestureDetector(
        onTap: widget.onTap,
        behavior: HitTestBehavior.opaque,
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 150),
          width: 40,
          height: 40,
          alignment: Alignment.center,
          decoration: BoxDecoration(
            color: t.bg,
            border: Border.all(color: t.border),
            borderRadius: BorderRadius.circular(t.radius),
          ),
          child: TweenAnimationBuilder<Color?>(
            tween: ColorTween(end: _hover ? t.primary : t.textMuted),
            duration: const Duration(milliseconds: 150),
            builder: (_, color, __) => Icon(Icons.add, color: color, size: 22),
          ),
        ),
      ),
    );
  }
}

class _ItemRow extends StatefulWidget {
  final String value;
  final VoidCallback onRemove;
  const _ItemRow({required this.value, required this.onRemove});

  @override
  State<_ItemRow> createState() => _ItemRowState();
}

class _ItemRowState extends State<_ItemRow> {
  bool _removeHovered = false;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return Padding(
      padding: const EdgeInsets.fromLTRB(14, 10, 12, 10),
      child: Row(
        children: [
          Expanded(
            child: Text(
              widget.value,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(
                color: t.text,
                fontFamily: t.monoFamily,
                fontSize: 13,
              ),
            ),
          ),
          IconButton(
            onPressed: widget.onRemove,
            onHover: (hovered) => setState(() => _removeHovered = hovered),
            icon: AnimatedContainer(
              duration: AppDurations.fast,
              curve: AppCurves.fastInSlowOut,
              child: Icon(
                Icons.close,
                size: 16,
                color: _removeHovered ? t.text : t.textMuted,
              ),
            ),
            highlightColor: Colors.transparent,
            splashColor: Colors.transparent,
            hoverColor: Colors.transparent,
            visualDensity: VisualDensity.compact,
          ),
        ],
      ),
    );
  }
}

/// Visual preview: file import/export will be connected after design approval.
class _PresetActionPill extends StatefulWidget {
  const _PresetActionPill({required this.label, required this.icon});

  final String label;
  final IconData icon;

  @override
  State<_PresetActionPill> createState() => _PresetActionPillState();
}

class _PresetActionPillState extends State<_PresetActionPill> {
  bool _hovered = false;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return MouseRegion(
      cursor: SystemMouseCursors.click,
      onEnter: (_) => setState(() => _hovered = true),
      onExit: (_) => setState(() => _hovered = false),
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: () {},
        child: AnimatedContainer(
          duration: AppDurations.fast,
          height: 34,
          decoration: BoxDecoration(
            color: _hovered ? t.surface2 : t.bg,
            border: Border.all(
              color: t.primary.withValues(alpha: _hovered ? .35 : 0),
            ),
            borderRadius: BorderRadius.circular(t.radius),
          ),
          child: Row(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              Icon(
                widget.icon,
                size: 14,
                color: _hovered ? t.primary : t.textMuted,
              ),
              const SizedBox(width: 7),
              Text(
                widget.label,
                style: TextStyle(
                  color: _hovered ? t.primary : t.textMuted,
                  fontSize: 12,
                  fontWeight: FontWeight.w500,
                  height: 1,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
