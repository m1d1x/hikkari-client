import 'package:flutter/material.dart';
import 'package:flutter/physics.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';

import '../core/models.dart';
import '../state/app_state.dart';
import '../theme/tokens.dart';
import '../ui/nekko_animated_height.dart';
import '../ui/nekko_dialog.dart';
import '../ui/nekko_segmented.dart';
import '../ui/nekko_text_field.dart';
import '../ui/nekko_toast.dart';
import 'app_picker_dialog.dart';
import 'import_routing_dialog.dart';
import 'service_presets.dart';

/// "Маршрутизация" section — master toggle + three category tabs + the
/// items list. Layout is a single card whose body collapses; expand state
/// is local (where the user is in the UI), but everything data-shaped
/// (enabled flag, mode, lists) lives in [AppState.routing]. The whitelist
/// vs. blacklist mode picker ([_ModeFlipButton]/[_ModeCaption]) has moved
/// to the settings sheet ([RouteSettingsSection]) — this section only
/// shows a tooltip on the header explaining whichever mode is active.
class RouteSection extends StatefulWidget {
  const RouteSection({super.key, this.spareHeight});

  /// Free height the page can still give this section before it starts to
  /// scroll, as laid out right now. Without it the whole reveal is treated as
  /// visible.
  final double Function()? spareHeight;

  @override
  State<RouteSection> createState() => _RouteSectionState();
}

/// What a reveal shows on screen. Each share is the part of a block's natural
/// height that fits above the bottom of the viewport, filled top-down:
/// controls, then the manual card, then the catalog. The rest of a long list
/// is below the fold and appears in one step once the motion has settled.
///
/// The section's bottom inset keeps a closed card off the window edge. When
/// the reveal fills the screen, the last visible block takes that inset over
/// as it grows ([edge]), so it reaches the edge exactly when the motion ends
/// and the header still moves with every pixel of growth.
@immutable
class _RevealPlan {
  const _RevealPlan({
    required this.distance,
    required this.edge,
    required this.controls,
    required this.manualGap,
    required this.manual,
    required this.presetsGap,
    required this.presets,
  });

  static const all = _RevealPlan(
    distance: 0,
    edge: 0,
    controls: 1,
    manualGap: _islandGap,
    manual: 1,
    presetsGap: _islandGap,
    presets: 1,
  );

  static const _islandGap = 12.0;

  /// Fill [room] pixels of header travel, plus up to [inset] pixels of the
  /// bottom inset, with blocks of the given natural heights.
  factory _RevealPlan.fit({
    required double room,
    required double inset,
    required double controls,
    required double manual,
    required double presets,
  }) {
    var left = room + inset;
    double take(double size) {
      final shown = left.clamp(0.0, size);
      left -= shown;
      return shown;
    }

    double share(double shown, double size) => size == 0 ? 1 : shown / size;
    final controlsShown = take(controls);
    final manualGap = take(_islandGap);
    final manualShown = take(manual);
    final presetsGap = take(_islandGap);
    final presetsShown = take(presets);
    final distance =
        controlsShown + manualGap + manualShown + presetsGap + presetsShown;
    return _RevealPlan(
      distance: distance,
      edge: (distance - room).clamp(0.0, inset),
      controls: share(controlsShown, controls),
      manualGap: manualGap,
      manual: share(manualShown, manual),
      presetsGap: presetsGap,
      presets: share(presetsShown, presets),
    );
  }

  /// Growth of the blocks between closed and the end of the motion.
  final double distance;

  /// Part of [distance] that replaces the bottom inset instead of moving the
  /// header.
  final double edge;
  final double controls;
  final double manualGap;
  final double manual;
  final double presetsGap;
  final double presets;
}

class _RouteSectionState extends State<RouteSection>
    with SingleTickerProviderStateMixin {
  // Visible progress: 0 closed, 1 when everything that fits is shown. Every
  // block grows linearly with it, so the header follows the spring exactly.
  late final AnimationController _reveal = AnimationController(vsync: this)
    ..addListener(_onTick);
  _RevealPlan _plan = _RevealPlan.all;
  // Open and at rest: blocks take their natural height, including the part of
  // a long catalog below the fold.
  bool _settled = false;
  // Growth that fits on screen from the closed state.
  double _closedRoom = double.infinity;
  double? _closedHeight;
  ScrollPosition? _scroll;
  double _target = 0;
  // Content is sliding under the cursor: rows must not flash hover as they
  // pass. Cleared once the card is within a pixel of rest, not after the
  // spring's sub-pixel tail, so a click right after opening lands.
  bool _passing = false;
  final _controlsSize = GlobalKey();
  final _manualSize = GlobalKey();
  final _presetsSize = GlobalKey();
  final _presetsTail = ValueNotifier<double>(0);
  // Part of the bottom inset, and past it, that the catalog's reveal has
  // taken over; see [CatalogRevealScope.edge].
  final _presetsEdge = ValueNotifier<double>(0);
  bool _open = false;
  bool _headerHover = false;
  bool _presetsHover = false;
  bool _presetMenuOpen = false;
  String _tab = 'domains';
  final _input = TextEditingController();

  // One instance per tab: keys keep each category's element and state
  // separate, so switching tabs mounts a fresh catalog instead of reusing
  // the previous tab's expansion and search. Rebuilding this section (the
  // reveal changes state as it starts and settles) must not rebuild every
  // catalog row. The catalog watches [AppState] itself.
  static const _catalogs = {
    'domains': ServicePresets(
      key: ValueKey('routing_presets_domains'),
      category: 'domains',
    ),
    'processes': ServicePresets(
      key: ValueKey('routing_presets_processes'),
      category: 'processes',
    ),
    'ips': ServicePresets(
      key: ValueKey('routing_presets_ips'),
      category: 'ips',
    ),
  };

  // Keeps the closed card off the window edge; see [_RevealPlan.edge].
  static const _bottomInset = 16.0;

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
  void didChangeDependencies() {
    super.didChangeDependencies();
    _scroll = Scrollable.maybeOf(context)?.position;
  }

  @override
  void dispose() {
    _reveal.dispose();
    _presetsTail.dispose();
    _presetsEdge.dispose();
    _input.dispose();
    super.dispose();
  }

  void _toggleOpen() => _setOpen(!_open);

  void _setOpen(bool value) {
    if (_open == value) return;
    final current = context.size?.height ?? 0;
    if (_reveal.isDismissed) _closedHeight = current;
    final growth = current - (_closedHeight ?? current);
    final overflow = _scroll?.hasContentDimensions ?? false
        ? _scroll!.maxScrollExtent
        : 0.0;
    final spare = widget.spareHeight?.call() ?? double.infinity;
    // From rest the plan is measured from the real layout: free space above,
    // minus what already overflows, plus what this section has grown by.
    // Mid-flight the current plan stays, so reversing keeps every height.
    if (!_reveal.isAnimating) {
      _closedRoom = spare - overflow + growth;
      double height(GlobalKey key) => key.currentContext?.size?.height ?? 0;
      _plan = _RevealPlan.fit(
        // Closing a scrolled page also shows what is scrolled past the top.
        room: _closedRoom + (value ? 0 : _scroll?.pixels ?? 0),
        inset: _bottomInset,
        controls: height(_controlsSize),
        manual: height(_manualSize),
        presets: height(_presetsSize),
      );
      _settled = false;
    }
    setState(() => _open = value);
    _animateTo(value ? 1 : 0);
  }

  void _animateTo(double target) {
    _target = target;
    final left = (target - _reveal.value).abs() * _plan.distance;
    if (left < .5) {
      _reveal.value = target;
      _onSettled(target);
      return;
    }
    // A spring keeps the current velocity when a click reverses the motion.
    final pixel = 1 / _plan.distance;
    _passing = true;
    _reveal
        .animateWith(
          SpringSimulation(
            AppMotion.travelSpring(left),
            _reveal.value,
            target,
            _reveal.velocity,
            tolerance: Tolerance(distance: pixel / 2, velocity: pixel * 40),
          ),
        )
        .then((_) => _onSettled(target));
  }

  void _onSettled(double target) {
    if (!mounted) return;
    setState(() {
      _reveal.value = target;
      _settled = target == 1;
      _passing = false;
    });
  }

  void _onTick() {
    if (_passing &&
        (_target - _reveal.value).abs() * _plan.distance < 1 &&
        mounted) {
      setState(() => _passing = false);
    }
    _keepScrollInBounds();
  }

  // Collapsing a scrolled page: the scroll offset shrinks with the content,
  // so the viewport never sees out-of-range content and its spring-back.
  void _keepScrollInBounds() {
    final scroll = _scroll;
    if (_open || scroll == null || scroll.pixels <= 0) return;
    if (!_closedRoom.isFinite) return;
    final limit = _reveal.value * (_plan.distance - _plan.edge) - _closedRoom;
    final bound = limit > 0 ? limit : 0.0;
    if (scroll.pixels > bound) scroll.jumpTo(bound);
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

  // Empty manual list: the caption says what the list is for, in the words
  // of the active mode and the noun of the active tab, instead of a bare
  // hint that reads as clutter.
  String _manualCaption(AppState s) {
    if (!s.routing.enabled) {
      return 'маршрутизация выключена: весь трафик идёт через vpn';
    }
    final noun = switch (_tab) {
      'domains' => 'домены',
      'ips' => 'ip',
      _ => 'приложения',
    };
    return s.routing.mode == RoutingMode.bypass
        ? 'добавьте сюда $noun, которые должны идти мимо vpn'
        : 'добавьте сюда $noun, которые должны идти только через vpn';
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

  Future<void> _resetRules(BuildContext context) async {
    final confirmed = await NekkoDialog.confirm(
      context,
      title: 'Сброс маршрутизации',
      message: 'Сбросить все правила к стандартным?',
      confirmLabel: 'сбросить',
    );
    if (!confirmed || !context.mounted) return;

    try {
      final state = context.read<AppState>();
      await state.resetToDefaults();
      if (!context.mounted) return;
      NekkoToast.show(context, 'Правила сброшены к стандартным');
    } catch (e) {
      if (!context.mounted) return;
      NekkoToast.show(
        context,
        'не удалось сбросить правила: $e',
        icon: Icons.error_outline_rounded,
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final s = context.watch<AppState>();
    final items = _itemsFor(_tab, s.routing.active);

    final reversedItems = items.reversed.toList();

    return AnimatedBuilder(
      animation: Listenable.merge([_reveal, _presetsEdge]),
      builder: (context, child) => Padding(
        padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
        // The inset goes negative while the catalog's reveal carries the
        // card's bottom edge past the window: the card then hangs below
        // the section by that much instead of making the page scroll.
        child: _BottomInset(
          inset:
              (_settled
                  ? _bottomInset
                  : _bottomInset - _plan.edge * _reveal.value) -
              _presetsEdge.value,
          child: child,
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        mainAxisSize: MainAxisSize.min,
        children: [
          _island(
            t,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                _head(t, s),
                _revealed(
                  KeyedSubtree(
                    key: _controlsSize,
                    child: _tabHeight(_body(t, s)),
                  ),
                  () => _plan.controls,
                ),
              ],
            ),
          ),
          _itemsBlock(t, s, reversedItems),
          _expandingIsland(
            t,
            key: const ValueKey('route_presets_surface'),
            measurementKey: _presetsSize,
            gap: () => _plan.presetsGap,
            share: () => _plan.presets,
            tail: _presetsTail,
            child: CatalogRevealScope(
              tail: _presetsTail,
              taken: _presetsEdge,
              spare: _presetsSpare,
              // The bottom inset, then the rounded corners and the border.
              edge: _bottomInset + t.radiusLg + 1,
              child: _tabHeight(_catalogs[_tab]!),
            ),
          ),
        ],
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

  // Visible part of a block: its share of the on-screen growth at the current
  // progress, or all of it once open has settled.
  double _shown(double share) => _settled ? 1 : share * _reveal.value;

  // Content slides under the cursor while it moves; hover must not flicker
  // across every row it passes. The header stays clickable to reverse.
  Widget _revealed(Widget child, double Function() share) => AnimatedBuilder(
    animation: _reveal,
    child: IgnorePointer(
      ignoring: _passing,
      // Only the clip moves while revealing: the content keeps its recorded
      // picture instead of repainting every row on every frame.
      child: FadeTransition(
        opacity: _reveal,
        child: RepaintBoundary(child: child),
      ),
    ),
    builder: (context, child) {
      final shown = _shown(share());
      return Offstage(
        offstage: shown == 0,
        child: ClipRect(
          child: Align(
            alignment: Alignment.topCenter,
            heightFactor: shown,
            child: child,
          ),
        ),
      );
    },
  );

  // Animate the surface with its contents: rounded borders follow the current
  // card height, rather than being cut off by a mask over the entire section.
  Widget _expandingIsland(
    NekkoTokens t, {
    Key? key,
    required GlobalKey measurementKey,
    required double Function() gap,
    required double Function() share,
    ValueNotifier<double>? tail,
    required Widget child,
  }) => AnimatedBuilder(
    animation: _reveal,
    // Reserve the final border inset before revealing. Animating a
    // decoration border also animates padding and can rewrap text mid-flight.
    child: _revealed(
      Padding(
        key: measurementKey,
        padding: const EdgeInsets.all(1),
        child: child,
      ),
      share,
    ),
    builder: (context, child) {
      final progress = _reveal.value;
      return Padding(
        padding: EdgeInsets.only(
          top: _settled ? _RevealPlan._islandGap : gap() * progress,
        ),
        child: Offstage(
          offstage: _shown(share()) == 0,
          child: _clipTail(
            tail,
            Container(
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
        ),
      );
    },
  );

  // Free height above the card: the page does not scroll until it is used.
  double _presetsSpare() => widget.spareHeight?.call() ?? double.infinity;

  // A catalog that opens taller than the window lays out at full height at
  // the tap; the surface shows only what its reveal has reached.
  Widget _clipTail(ValueNotifier<double>? tail, Widget child) => tail == null
      ? child
      : ClipRect(clipper: CatalogTailClipper(tail), child: child);

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
              RotationTransition(
                turns: _reveal.drive(Tween(begin: 0, end: .5)),
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
                // whole pixels only: a fractional offset leaves glyphs
                // subpixel-blurred, then they snap when it lands on 0
                offset: Offset(0, (-8 * (1 - progress)).roundToDouble()),
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
        children: [
          Expanded(
            child: _PresetActionPill(
              key: const ValueKey('routing_preset_import_button'),
              label: 'импорт',
              icon: Icons.south_rounded,
              onTap: () => ImportRoutingDialog.show(context),
            ),
          ),
          const SizedBox(width: 6),
          Expanded(
            child: _PresetActionPill(
              key: const ValueKey('routing_preset_export_button'),
              label: 'экспорт',
              icon: Icons.north_rounded,
              onTap: () async {
                try {
                  final state = context.read<AppState>();
                  final data = await state.exportRoutingSnapshot();
                  await Clipboard.setData(ClipboardData(text: data));
                  if (!context.mounted) return;
                  NekkoToast.show(context, 'Правила скопированы');
                } catch (e) {
                  if (!context.mounted) return;
                  NekkoToast.show(
                    context,
                    'не удалось экспортировать правила: $e',
                    icon: Icons.error_outline_rounded,
                  );
                }
              },
            ),
          ),
          const SizedBox(width: 6),
          _PresetActionIcon(
            key: const ValueKey('reset_routing_button_pill'),
            icon: Icons.refresh_rounded,
            tooltip: 'сбросить к стандартным',
            onTap: () => _resetRules(context),
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

  Widget _itemsBlock(NekkoTokens t, AppState s, List<String> items) {
    return _expandingIsland(
      t,
      key: const ValueKey('route_manual_surface'),
      measurementKey: _manualSize,
      gap: () => _plan.manualGap,
      share: () => _plan.manual,
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
                  _manualCaption(s),
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
        ? 'маршрутизация выключена: весь трафик идёт через vpn'
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
  const _PresetActionPill({
    super.key,
    required this.label,
    required this.icon,
    this.onTap,
  });

  final String label;
  final IconData icon;
  final VoidCallback? onTap;

  @override
  State<_PresetActionPill> createState() => _PresetActionPillState();
}

class _PresetActionPillState extends State<_PresetActionPill> {
  bool _hovered = false;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final color = _hovered ? t.primary : t.textMuted;
    return MouseRegion(
      cursor: SystemMouseCursors.click,
      onEnter: (_) => setState(() => _hovered = true),
      onExit: (_) => setState(() => _hovered = false),
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: widget.onTap,
        child: AnimatedContainer(
          duration: AppDurations.hover,
          curve: AppCurves.hover,
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
              TweenAnimationBuilder<Color?>(
                tween: ColorTween(end: color),
                duration: AppDurations.hover,
                curve: AppCurves.hover,
                builder: (_, c, __) => Icon(widget.icon, size: 14, color: c),
              ),
              const SizedBox(width: 7),
              TweenAnimationBuilder<Color?>(
                tween: ColorTween(end: color),
                duration: AppDurations.hover,
                curve: AppCurves.hover,
                builder: (_, c, __) => Text(
                  widget.label,
                  style: TextStyle(
                    color: c,
                    fontSize: 12,
                    fontWeight: FontWeight.w500,
                    height: 1,
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

class _PresetActionIcon extends StatefulWidget {
  const _PresetActionIcon({
    super.key,
    required this.icon,
    required this.tooltip,
    this.onTap,
  });

  final IconData icon;
  final String tooltip;
  final VoidCallback? onTap;

  @override
  State<_PresetActionIcon> createState() => _PresetActionIconState();
}

class _PresetActionIconState extends State<_PresetActionIcon> {
  bool _hovered = false;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final color = _hovered ? t.primary : t.textMuted;
    return Tooltip(
      message: widget.tooltip,
      waitDuration: const Duration(milliseconds: 300),
      child: MouseRegion(
        cursor: SystemMouseCursors.click,
        onEnter: (_) => setState(() => _hovered = true),
        onExit: (_) => setState(() => _hovered = false),
        child: GestureDetector(
          behavior: HitTestBehavior.opaque,
          onTap: widget.onTap,
          child: AnimatedContainer(
            duration: AppDurations.hover,
            curve: AppCurves.hover,
            width: 34,
            height: 34,
            decoration: BoxDecoration(
              color: _hovered ? t.surface2 : t.bg,
              border: Border.all(
                color: t.primary.withValues(alpha: _hovered ? .35 : 0),
              ),
              borderRadius: BorderRadius.circular(t.radius),
            ),
            child: Center(
              child: TweenAnimationBuilder<Color?>(
                tween: ColorTween(end: color),
                duration: AppDurations.hover,
                curve: AppCurves.hover,
                builder: (_, c, __) => Icon(widget.icon, size: 15, color: c),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

/// Bottom space under the section that may be negative: the child keeps its
/// full height and paints past the section's own bottom.
class _BottomInset extends SingleChildRenderObjectWidget {
  const _BottomInset({required this.inset, required super.child});

  final double inset;

  @override
  RenderObject createRenderObject(BuildContext context) =>
      _RenderBottomInset(inset);

  @override
  void updateRenderObject(BuildContext context, _RenderBottomInset render) {
    render.inset = inset;
  }
}

class _RenderBottomInset extends RenderProxyBox {
  _RenderBottomInset(this._inset);

  double _inset;
  set inset(double value) {
    if (value == _inset) return;
    _inset = value;
    markNeedsLayout();
  }

  double _extend(double height) => height + _inset > 0 ? height + _inset : 0;

  @override
  double computeMinIntrinsicHeight(double width) =>
      _extend(super.computeMinIntrinsicHeight(width));

  @override
  double computeMaxIntrinsicHeight(double width) =>
      _extend(super.computeMaxIntrinsicHeight(width));

  @override
  Size computeDryLayout(BoxConstraints constraints) {
    final content = child?.getDryLayout(constraints.loosen()) ?? Size.zero;
    return constraints.constrain(Size(content.width, _extend(content.height)));
  }

  @override
  void performLayout() {
    final content = child;
    if (content == null) {
      size = constraints.smallest;
      return;
    }
    // The child sizes to its content whatever the section is stretched to;
    // the section adds the inset below it.
    content.layout(
      constraints.copyWith(minHeight: 0, maxHeight: double.infinity),
      parentUsesSize: true,
    );
    size = constraints.constrain(
      Size(content.size.width, _extend(content.size.height)),
    );
  }
}
