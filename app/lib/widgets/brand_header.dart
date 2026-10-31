import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:url_launcher/url_launcher.dart';

import '../state/app_state.dart';
import '../ui/nekko_segmented.dart';
import '../ui/nekko_toast.dart';
import '../theme/tokens.dart';
import 'route_section.dart';
import 'service_presets.dart';
import 'update_banner.dart';

/// Fallback client version displayed in settings when [UpdateStatus.installed]
/// is not populated yet.
const String kFallbackClientVersion = '1.0.9';

/// The top row: the settings action on the right and, when an update is
/// ready, the [UpdateBanner] beside it. The old brand/IP header was
/// intentionally removed so the home screen starts with the product itself.
class BrandHeader extends StatelessWidget {
  /// The settings icon's footprint: 24px glyph + 14px padding each side.
  static const double _iconBox = 52;

  /// Below this window width the update card starts at the left edge
  /// instead of being centred.
  static const double _centredFrom = 420;

  static final Uri donateUri = Uri.parse('https://hikkari.net');

  final VoidCallback onOpenLogs;
  final Future<bool> Function(Uri uri)? onLaunchUrl;

  const BrandHeader({
    super.key,
    required this.onOpenLogs,
    this.onLaunchUrl,
  });

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final h = context.watch<AppState>().health;
    final state = context.read<AppState>();
    final status = h?.status ?? 'off';
    final ip = h?.ip ?? '';
    final msg = h?.message ?? '';
    final details = [
      'настройки',
      if (ip.isNotEmpty) ip,
      if (msg.isNotEmpty) msg,
    ].join('\n');

    final baseColor = _statusColor(t, status);
    final wide = MediaQuery.sizeOf(context).width >= _centredFrom;
    return Padding(
      padding: const EdgeInsets.fromLTRB(14, 14, 14, 0),
      child: Row(
        children: [
          // The update card shares the top row with the settings icon
          // instead of pushing the content down; empty when there is none.
          // A blank as wide as the icon on the left keeps it centred —
          // unless the window is so narrow that the card needs that room.
          SizedBox(width: wide ? _iconBox : 0),
          const Expanded(child: UpdateBanner()),
          _HoverIcon(
            tooltip: details,
            onPressed: () => _showSettings(context, state),
            icon: Icons.settings_outlined,
            size: 24,
            color: baseColor,
            hoverColor: t.primary,
            padding: const EdgeInsets.all(14),
          ),
        ],
      ),
    );
  }

  Color _statusColor(NekkoTokens t, String status) => switch (status) {
    'connecting' => t.warn,
    'broken' => t.bad,
    _ => t.textMuted,
  };

  Future<void> _showSettings(BuildContext context, AppState state) {
    final t = context.tokens;
    return showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      backgroundColor: t.surface,
      barrierColor: Colors.black54,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(t.radiusLg)),
      ),
      builder: (_) => ChangeNotifierProvider<AppState>.value(
        value: state,
        child: _SettingsSheet(
          onOpenLogs: onOpenLogs,
          onLaunchUrl: onLaunchUrl,
        ),
      ),
    );
  }
}

class _SettingsSheet extends StatefulWidget {
  final VoidCallback onOpenLogs;
  final Future<bool> Function(Uri uri)? onLaunchUrl;

  const _SettingsSheet({required this.onOpenLogs, this.onLaunchUrl});

  @override
  State<_SettingsSheet> createState() => _SettingsSheetState();
}

class _SettingsSheetState extends State<_SettingsSheet> {
  String? _hint;
  int _hintVersion = 0;
  bool _logsHover = false;
  bool _donateHover = false;

  // Seven quick taps on the title flip the tester mode; a pause starts over.
  static const _testerTaps = 7;
  static const _testerTapGap = Duration(milliseconds: 1500);
  int _titleTaps = 0;
  DateTime _lastTitleTap = DateTime(0);

  Future<void> _tapTitle() async {
    final now = DateTime.now();
    _titleTaps = now.difference(_lastTitleTap) > _testerTapGap
        ? 1
        : _titleTaps + 1;
    _lastTitleTap = now;
    if (_titleTaps < _testerTaps) return;
    _titleTaps = 0;
    final state = context.read<AppState>();
    final on = !(state.presets?.tester ?? false);
    await state.setPresetTester(on);
    if (!mounted) return;
    final failed = (state.presets?.tester ?? false) != on;
    NekkoToast.show(
      context,
      failed
          ? 'не удалось переключить режим тестера'
          : on
          ? 'режим тестера включён'
          : 'режим тестера выключен',
      icon: failed ? Icons.error_outline_rounded : Icons.science_outlined,
    );
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final state = context.watch<AppState>();
    final h = state.health;
    final status = h?.status ?? 'off';
    final ip = h?.ip ?? '';
    final msg = h?.message ?? '';
    final installed = state.updateStatus.installed.trim();
    final rawVersion = installed.isEmpty ? kFallbackClientVersion : installed;
    final versionLabel = rawVersion.startsWith('v')
        ? 'hikkari $rawVersion'
        : 'hikkari v$rawVersion';
    final statusLabel = switch (status) {
      'ok' => 'подключено',
      'connecting' => 'подключаюсь',
      'broken' => 'ошибка туннеля',
      _ => 'отключено',
    };

    return SafeArea(
      top: false,
      child: SingleChildScrollView(
        child: Padding(
          padding: const EdgeInsets.fromLTRB(20, 12, 20, 20),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Center(
                child: Container(
                  width: 36,
                  height: 4,
                  decoration: BoxDecoration(
                    color: t.borderStrong,
                    borderRadius: BorderRadius.circular(2),
                  ),
                ),
              ),
              const SizedBox(height: 18),
              Center(
                child: GestureDetector(
                  behavior: HitTestBehavior.opaque,
                  onTap: _tapTitle,
                  child: Text(
                    'настройки',
                    style: t.sectionTitle.copyWith(
                      color: context.watch<AppState>().presets?.tester ?? false
                          ? t.primary
                          : null,
                    ),
                  ),
                ),
              ),
              const SizedBox(height: 14),
              _InfoRow(label: 'статус', value: statusLabel, loading: false),
              const SizedBox(height: 8),
              Row(
                crossAxisAlignment: CrossAxisAlignment.center,
                children: [
                  SizedBox(
                    width: 88,
                    child: Text(
                      'движок',
                      style: TextStyle(
                        color: t.textMuted,
                        fontSize: 13,
                        fontWeight: FontWeight.w500,
                      ),
                    ),
                  ),
                  Expanded(
                    child: IgnorePointer(
                      ignoring: context.watch<AppState>().switchingEngine,
                      child: Opacity(
                        opacity: context.watch<AppState>().switchingEngine
                            ? 0.6
                            : 1,
                        child: NekkoSegmented(
                          selected: context.watch<AppState>().engineId,
                          onChanged: (id) =>
                              context.read<AppState>().switchEngine(id),
                          segments: [
                            const NekkoSegment('sing-box', 'sing-box'),
                            NekkoSegment(
                              'xray',
                              'xray',
                              enabled: xrayAvailable,
                              hint: xrayAvailable
                                  ? null
                                  : 'Xray на Windows пока недоступен',
                            ),
                          ],
                          fontSize: 13,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                    ),
                  ),
                ],
              ),
              if (context.watch<AppState>().lastError case final error?)
                Padding(
                  padding: const EdgeInsets.only(top: 4),
                  child: Text(
                    error,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(color: t.bad, fontSize: 11),
                  ),
                ),
              const SizedBox(height: 5),
              _InfoRow(
                label: 'ip',
                value: ip,
                loading: context.watch<AppState>().switchingEngine,
              ),
              _InfoRow(
                label: 'сообщение',
                value: msg,
                loading: context.watch<AppState>().switchingEngine,
              ),
              const SizedBox(height: 16),
              Divider(height: 1, color: t.border),
              const SizedBox(height: 16),
              RouteSettingsSection(onHintChanged: _setHint),
              const SizedBox(height: 10),
              PresetSettingsSection(onHintChanged: _setHint),
              const SizedBox(height: 8),
              SizedBox(
                height: 42,
                child: AnimatedSwitcher(
                  duration: AppDurations.fast,
                  switchInCurve: AppCurves.fastInSlowOut,
                  switchOutCurve: Curves.easeIn,
                  child: _hint == null
                      ? SizedBox.shrink(key: ValueKey('hint-${_hintVersion}'))
                      : Align(
                          key: ValueKey('hint-${_hintVersion}'),
                          alignment: Alignment.topLeft,
                          child: Text(
                            _hint!,
                            maxLines: 2,
                            overflow: TextOverflow.ellipsis,
                            style: TextStyle(
                              color: t.textMuted,
                              fontSize: 12,
                              height: 1.35,
                            ),
                          ),
                        ),
                ),
              ),
              const SizedBox(height: 4),
              MouseRegion(
                cursor: SystemMouseCursors.click,
                onEnter: (_) => setState(() => _logsHover = true),
                onExit: (_) => setState(() => _logsHover = false),
                child: GestureDetector(
                  behavior: HitTestBehavior.opaque,
                  onTap: () {
                    Navigator.of(context).maybePop();
                    widget.onOpenLogs();
                  },
                  child: AnimatedContainer(
                    duration: AppDurations.fast,
                    curve: AppCurves.fastInSlowOut,
                    padding: const EdgeInsets.symmetric(vertical: 12),
                    decoration: BoxDecoration(
                      color: _logsHover ? t.surface2 : Colors.transparent,
                      borderRadius: BorderRadius.circular(t.radius),
                      border: Border.all(
                        color: _logsHover
                            ? t.primary.withValues(alpha: .55)
                            : t.borderStrong,
                      ),
                    ),
                    child: Row(
                      mainAxisAlignment: MainAxisAlignment.center,
                      children: [
                        Icon(Icons.terminal, size: 16, color: t.text),
                        const SizedBox(width: 8),
                        Text(
                          'логи',
                          style: TextStyle(
                            color: t.text,
                            fontSize: 13,
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                      ],
                    ),
                  ),
                ),
              ),
              const SizedBox(height: 10),
              Row(
                children: [
                  Expanded(
                    child: Align(
                      alignment: Alignment.centerLeft,
                      child: MouseRegion(
                        cursor: SystemMouseCursors.click,
                        onEnter: (_) => setState(() => _donateHover = true),
                        onExit: (_) => setState(() => _donateHover = false),
                        child: GestureDetector(
                          behavior: HitTestBehavior.opaque,
                          onTap: _openDonate,
                          child: AnimatedContainer(
                            duration: AppDurations.fast,
                            curve: AppCurves.fastInSlowOut,
                            padding: const EdgeInsets.symmetric(
                              horizontal: 8,
                              vertical: 4,
                            ),
                            decoration: BoxDecoration(
                              color: _donateHover
                                  ? t.primary.withValues(alpha: 0.12)
                                  : Colors.transparent,
                              borderRadius: BorderRadius.circular(6),
                            ),
                            child: Row(
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                Icon(
                                  _donateHover
                                      ? Icons.favorite_rounded
                                      : Icons.favorite_border_rounded,
                                  size: 14,
                                  color: t.primary,
                                ),
                                const SizedBox(width: 6),
                                Flexible(
                                  child: Text(
                                    'поддержать проект',
                                    maxLines: 1,
                                    overflow: TextOverflow.ellipsis,
                                    style: TextStyle(
                                      color: _donateHover
                                          ? t.text
                                          : t.textMuted,
                                      fontSize: 11.5,
                                      fontWeight: FontWeight.w500,
                                    ),
                                  ),
                                ),
                              ],
                            ),
                          ),
                        ),
                      ),
                    ),
                  ),
                  const SizedBox(width: 8),
                  Text(
                    versionLabel,
                    style: TextStyle(
                      color: t.textFaint,
                      fontFamily: t.monoFamily,
                      fontSize: 11.5,
                      fontWeight: FontWeight.w500,
                    ),
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }

  Future<void> _openDonate() async {
    final launcher = widget.onLaunchUrl;
    if (launcher != null) {
      await launcher(BrandHeader.donateUri);
    } else {
      await launchUrl(
        BrandHeader.donateUri,
        mode: LaunchMode.externalApplication,
      );
    }
  }

  void _setHint(String? hint) {
    if (_hint == hint) return;
    setState(() {
      _hint = hint;
      _hintVersion++;
    });
  }
}

class _InfoRow extends StatelessWidget {
  final String label;
  final String value;
  final bool loading;

  const _InfoRow({
    required this.label,
    required this.value,
    required this.loading,
  });

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 88,
            child: Text(
              label,
              style: TextStyle(
                color: t.textMuted,
                fontSize: 13,
                fontWeight: FontWeight.w500,
              ),
            ),
          ),
          Expanded(
            child: Padding(
              padding: const EdgeInsets.only(left: 8),
              child: SizedBox(
                height: 20,
                child: Align(
                  alignment: Alignment.centerLeft,
                  child: AnimatedSwitcher(
                    duration: const Duration(milliseconds: 190),
                    reverseDuration: const Duration(milliseconds: 140),
                    switchInCurve: Curves.easeOutCubic,
                    switchOutCurve: Curves.easeInCubic,
                    layoutBuilder: (current, previous) => Stack(
                      alignment: Alignment.centerLeft,
                      children: [...previous, if (current != null) current],
                    ),
                    transitionBuilder: (child, animation) => ClipRect(
                      child: SizeTransition(
                        sizeFactor: animation,
                        axis: Axis.horizontal,
                        alignment: Alignment.centerLeft,
                        child: FadeTransition(opacity: animation, child: child),
                      ),
                    ),
                    child: loading
                        ? const _LoadingDots(key: ValueKey('loading'))
                        : value.isEmpty
                        ? Text(
                            '—',
                            key: const ValueKey('empty'),
                            style: TextStyle(
                              color: t.textFaint,
                              fontSize: 13,
                              fontFamily: t.monoFamily,
                            ),
                          )
                        : SelectableText(
                            value,
                            key: ValueKey(value),
                            style: TextStyle(
                              color: t.text,
                              fontSize: 13,
                              fontWeight: FontWeight.w600,
                              fontFamily: t.monoFamily,
                            ),
                          ),
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

class _LoadingDots extends StatefulWidget {
  const _LoadingDots({super.key});

  @override
  State<_LoadingDots> createState() => _LoadingDotsState();
}

class _LoadingDotsState extends State<_LoadingDots>
    with SingleTickerProviderStateMixin {
  late final AnimationController _controller = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 900),
  )..repeat();

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return SizedBox(
      key: const ValueKey('loading'),
      width: 22,
      height: 16,
      child: Align(
        alignment: Alignment.centerLeft,
        child: Transform.scale(
          scale: .8,
          alignment: Alignment.centerLeft,
          child: SizedBox(
            width: 28,
            child: AnimatedBuilder(
              animation: _controller,
              builder: (_, __) => Row(
                mainAxisAlignment: MainAxisAlignment.spaceBetween,
                children: List.generate(3, (index) {
                  final phase = (_controller.value - index / 3) % 1;
                  final pulse = 1 - (phase * 2 - 1).abs();
                  return Opacity(
                    opacity: .25 + .75 * pulse,
                    child: Transform.scale(
                      scale: .65 + .75 * pulse,
                      child: Container(
                        width: 5,
                        height: 5,
                        decoration: BoxDecoration(
                          color: t.text,
                          shape: BoxShape.circle,
                        ),
                      ),
                    ),
                  );
                }),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class _HoverIcon extends StatefulWidget {
  final String tooltip;
  final VoidCallback onPressed;
  final IconData icon;
  final double size;
  final Color color;
  final Color hoverColor;
  final EdgeInsets padding;

  const _HoverIcon({
    required this.tooltip,
    required this.onPressed,
    required this.icon,
    required this.size,
    required this.color,
    required this.hoverColor,
    required this.padding,
  });

  @override
  State<_HoverIcon> createState() => _HoverIconState();
}

class _HoverIconState extends State<_HoverIcon> {
  bool _hover = false;

  @override
  Widget build(BuildContext context) {
    return Tooltip(
      message: widget.tooltip,
      waitDuration: const Duration(milliseconds: 400),
      child: MouseRegion(
        cursor: SystemMouseCursors.click,
        onEnter: (_) => setState(() => _hover = true),
        onExit: (_) => setState(() => _hover = false),
        child: GestureDetector(
          onTap: widget.onPressed,
          behavior: HitTestBehavior.opaque,
          child: Padding(
            padding: widget.padding,
            child: TweenAnimationBuilder<Color?>(
              tween: ColorTween(end: _hover ? widget.hoverColor : widget.color),
              duration: const Duration(milliseconds: 150),
              builder: (_, color, __) =>
                  Icon(widget.icon, size: widget.size, color: color),
            ),
          ),
        ),
      ),
    );
  }
}
