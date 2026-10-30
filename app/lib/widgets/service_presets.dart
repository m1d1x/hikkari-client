import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:simple_icons/simple_icons.dart';

import '../core/routing_presets.dart';
import '../state/app_state.dart';
import '../theme/tokens.dart';
import '../ui/nekko_switch.dart';
import 'service_icon.dart';

/// Ready-made groups share the rules list, but keep their own engine choices.
class ServicePresets extends StatelessWidget {
  final String category;
  const ServicePresets({super.key, this.category = 'domains'});

  @override
  Widget build(BuildContext context) {
    final state = context.watch<AppState>();
    final catalog = state.presets;
    final t = context.tokens;
    if (catalog == null) {
      return Padding(
        padding: const EdgeInsets.all(14),
        child: Row(
          children: [
            Expanded(
              child: Text(
                state.presetError == null
                    ? 'загрузка готовых списков…'
                    : 'не удалось загрузить готовые списки',
                style: TextStyle(color: t.textMuted, fontSize: 11),
              ),
            ),
            if (state.presetError != null)
              IconButton(
                onPressed: state.refreshPresetState,
                tooltip: 'повторить',
                icon: const Icon(Icons.refresh, size: 16),
              ),
          ],
        ),
      );
    }
    final services = catalog.services
        .where((s) => _rules(s, catalog.platform, category).isNotEmpty)
        .toList();
    return Padding(
      padding: const EdgeInsets.all(12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text(
            'преднастроенные',
            textAlign: TextAlign.center,
            style: TextStyle(
              color: t.textSecondary,
              fontSize: 14,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(height: 10),
          Divider(height: 1, thickness: 1, color: t.border),
          const SizedBox(height: 12),
          for (final service in services)
            _ServiceRow(
              key: ValueKey('${category}_${service.id}'),
              service: service,
              catalog: catalog,
              category: category,
              selected: catalog.selected(service, state.routing.mode),
              onChanged: state.presetsBusy
                  ? null
                  : (on) => state.setServicePreset(service.id, on),
            ),
          if (services.isEmpty)
            Text(
              'нет готовых правил для этой категории',
              textAlign: TextAlign.center,
              style: TextStyle(color: t.textFaint, fontSize: 11),
            ),
          if (state.presetError != null)
            Text(
              'не удалось применить изменение: ${state.presetError}',
              style: TextStyle(color: t.bad, fontSize: 11),
            ),
          if (kRoutingDryRun)
            Text(
              'демо: маршрутизация не применяется',
              style: TextStyle(color: t.warn, fontSize: 10),
            ),
        ],
      ),
    );
  }
}

List<String> _rules(ServicePreset service, String platform, String category) =>
    switch (category) {
      'domains' => service.domains,
      'ips' => service.ips,
      _ => service.processes[platform] ?? const [],
    };

String _fileCount(int count) {
  final lastTwo = count % 100;
  final last = count % 10;
  final noun = lastTwo >= 11 && lastTwo <= 14
      ? 'файлов'
      : last == 1
      ? 'файл'
      : last >= 2 && last <= 4
      ? 'файла'
      : 'файлов';
  return '$count $noun';
}

class _ServiceRow extends StatefulWidget {
  final ServicePreset service;
  final RoutingPresets catalog;
  final String category;
  final bool selected;
  final ValueChanged<bool>? onChanged;
  const _ServiceRow({
    super.key,
    required this.service,
    required this.catalog,
    required this.category,
    required this.selected,
    required this.onChanged,
  });

  @override
  State<_ServiceRow> createState() => _ServiceRowState();
}

class _ServiceRowState extends State<_ServiceRow>
    with SingleTickerProviderStateMixin {
  late final AnimationController _expandController;
  bool _hover = false;
  bool _expanded = false;

  @override
  void initState() {
    super.initState();
    _expandController = AnimationController(
      duration: AppDurations.routingDetails,
      vsync: this,
    );
  }

  @override
  void dispose() {
    _expandController.dispose();
    super.dispose();
  }

  void _toggleDetails() {
    setState(() => _expanded = !_expanded);
    _expandController.animateTo(
      _expanded ? 1 : 0,
      duration: AppDurations.routingDetails,
      curve: AppCurves.fastInSlowOut,
    );
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final (icon, brand) = switch (widget.service.icon) {
      'steam' => (SimpleIcons.steam, const Color(0xFFC7D0D8)),
      'discord' => (SimpleIcons.discord, const Color(0xFF5865F2)),
      'telegram' => (SimpleIcons.telegram, const Color(0xFF26A5E4)),
      _ => (Icons.apps, t.textMuted),
    };
    final color = widget.selected ? brand : t.textFaint;
    final rules = _rules(
      widget.service,
      widget.catalog.platform,
      widget.category,
    );
    final nameColor = widget.selected ? t.text : t.textFaint;
    final subtitleColor = widget.selected ? t.textSecondary : t.textFaint;
    final actionColor = widget.selected ? t.textMuted : t.primary;
    final surfaceColor = widget.selected
        ? Color.alphaBlend(Colors.white.withValues(alpha: 0.05), t.surface2)
        : t.bg;
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: MouseRegion(
        cursor: SystemMouseCursors.click,
        onEnter: (_) => setState(() => _hover = true),
        onExit: (_) => setState(() => _hover = false),
        child: AnimatedContainer(
          key: ValueKey('preset_surface_${widget.service.id}'),
          duration: AppDurations.fast,
          curve: AppCurves.fastInSlowOut,
          decoration: BoxDecoration(
            color: Color.alphaBlend(
              actionColor.withValues(alpha: _hover ? 0.08 : 0),
              surfaceColor,
            ),
            borderRadius: BorderRadius.circular(t.radiusLg),
            border: Border.all(
              color: _hover
                  ? actionColor.withValues(alpha: 0.35)
                  : widget.selected
                  ? Color.alphaBlend(
                      brand.withValues(alpha: 0.35),
                      t.borderStrong,
                    )
                  : Colors.transparent,
              width: 1.0,
            ),
          ),
          child: Material(
            color: Colors.transparent,
            borderRadius: BorderRadius.circular(t.radiusLg),
            clipBehavior: Clip.antiAlias,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              mainAxisSize: MainAxisSize.min,
              children: [
                Semantics(
                  button: true,
                  toggled: widget.selected,
                  enabled: widget.onChanged != null,
                  child: InkWell(
                    key: ValueKey('preset_action_${widget.service.id}'),
                    onTap: widget.onChanged == null
                        ? null
                        : () => widget.onChanged!(!widget.selected),
                    onSecondaryTap: _toggleDetails,
                    onLongPress: _toggleDetails,
                    mouseCursor: SystemMouseCursors.click,
                    hoverColor: Colors.transparent,
                    child: Stack(
                      alignment: Alignment.center,
                      children: [
                        AnimatedOpacity(
                          opacity: _hover && widget.onChanged != null ? 0 : 1,
                          duration: AppDurations.fast,
                          curve: AppCurves.fastInSlowOut,
                          child: Padding(
                            padding: const EdgeInsets.symmetric(
                              horizontal: 12,
                              vertical: 12,
                            ),
                            child: Row(
                              children: [
                                TweenAnimationBuilder<Color?>(
                                  tween: ColorTween(end: color),
                                  duration: AppDurations.normal,
                                  curve: AppCurves.fastInSlowOut,
                                  builder: (context, tint, child) =>
                                      ServiceIconBadge(
                                        icon: icon,
                                        color: tint ?? color,
                                      ),
                                ),
                                const SizedBox(width: 10),
                                Expanded(
                                  child: Column(
                                    crossAxisAlignment:
                                        CrossAxisAlignment.start,
                                    children: [
                                      AnimatedDefaultTextStyle(
                                        duration: AppDurations.normal,
                                        curve: AppCurves.fastInSlowOut,
                                        style: TextStyle(
                                          color: nameColor,
                                          fontFamily: t.monoFamily,
                                          fontSize: 13,
                                          fontWeight: widget.selected
                                              ? FontWeight.w700
                                              : FontWeight.w600,
                                        ),
                                        child: Text(
                                          widget.service.name.toLowerCase(),
                                          overflow: TextOverflow.ellipsis,
                                        ),
                                      ),
                                      const SizedBox(height: 2),
                                      AnimatedDefaultTextStyle(
                                        duration: AppDurations.normal,
                                        curve: AppCurves.fastInSlowOut,
                                        style: TextStyle(
                                          color: subtitleColor,
                                          fontSize: 11,
                                        ),
                                        child: Text(
                                          widget.category == 'processes'
                                              ? _fileCount(rules.length)
                                              : '${rules.length} ${widget.category == 'domains' ? 'доменов' : 'ip / подсетей'}',
                                          maxLines: 2,
                                          overflow: TextOverflow.ellipsis,
                                        ),
                                      ),
                                    ],
                                  ),
                                ),
                                const SizedBox(width: 8),
                                GestureDetector(
                                  behavior: HitTestBehavior.opaque,
                                  onTap: _toggleDetails,
                                  child: AnimatedRotation(
                                    turns: _expanded ? 0.5 : 0,
                                    duration: AppDurations.routingDetails,
                                    curve: AppCurves.fastInSlowOut,
                                    child: Icon(
                                      Icons.keyboard_arrow_down,
                                      size: 16,
                                      color: _hover ? t.textMuted : t.textFaint,
                                    ),
                                  ),
                                ),
                              ],
                            ),
                          ),
                        ),
                        Positioned.fill(
                          child: IgnorePointer(
                            child: ExcludeSemantics(
                              child: AnimatedOpacity(
                                key: ValueKey(
                                  'preset_hover_${widget.service.id}',
                                ),
                                opacity: _hover && widget.onChanged != null
                                    ? 1
                                    : 0,
                                duration: AppDurations.fast,
                                curve: AppCurves.fastInSlowOut,
                                child: Center(
                                  child: Row(
                                    mainAxisSize: MainAxisSize.min,
                                    children: [
                                      SizedBox(
                                        key: ValueKey(
                                          'preset_hover_power_${widget.service.id}',
                                        ),
                                        width: 32,
                                        height: 32,
                                        child: Stack(
                                          alignment: Alignment.center,
                                          children: [
                                            Icon(
                                              Icons.power_settings_new,
                                              size: 26,
                                              color: actionColor,
                                            ),
                                            if (widget.selected) ...[
                                              Transform.rotate(
                                                angle: -0.785398,
                                                child: Container(
                                                  width: 30,
                                                  height: 4.5,
                                                  decoration: BoxDecoration(
                                                    color: surfaceColor,
                                                    borderRadius:
                                                        BorderRadius.circular(
                                                          2,
                                                        ),
                                                  ),
                                                ),
                                              ),
                                              Transform.rotate(
                                                angle: -0.785398,
                                                child: Container(
                                                  width: 28,
                                                  height: 2.2,
                                                  decoration: BoxDecoration(
                                                    color: actionColor,
                                                    borderRadius:
                                                        BorderRadius.circular(
                                                          1.5,
                                                        ),
                                                  ),
                                                ),
                                              ),
                                            ],
                                          ],
                                        ),
                                      ),
                                      const SizedBox(width: 8),
                                      AnimatedDefaultTextStyle(
                                        duration: AppDurations.normal,
                                        curve: AppCurves.fastInSlowOut,
                                        style: TextStyle(
                                          color: actionColor,
                                          fontSize: 12,
                                          fontWeight: FontWeight.w600,
                                          letterSpacing: 0.3,
                                        ),
                                        child: Text(
                                          widget.selected
                                              ? 'отключить'
                                              : 'включить',
                                          key: ValueKey(
                                            'preset_hover_action_${widget.service.id}',
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
                      ],
                    ),
                  ),
                ),
                ClipRect(
                  child: AnimatedBuilder(
                    animation: _expandController,
                    builder: (context, child) => Offstage(
                      offstage: _expandController.value == 0,
                      child: Align(
                        alignment: Alignment.bottomCenter,
                        heightFactor: _expandController.value,
                        child: child,
                      ),
                    ),
                    child: Padding(
                      padding: const EdgeInsets.fromLTRB(14, 0, 14, 14),
                      // Read-only text does not need EditableText's viewport,
                      // text-input/selection lifecycle or a nested scrollable.
                      child: SelectionArea(
                        child: Align(
                          alignment: Alignment.centerLeft,
                          child: Text(
                            rules.join('\n'),
                            style: TextStyle(
                              color: t.textMuted,
                              fontSize: 12,
                              height: 1.6,
                            ),
                          ),
                        ),
                      ),
                    ),
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

/// Delivery preferences belong to the settings sheet, not routing controls.
class PresetSettingsSection extends StatefulWidget {
  final ValueChanged<String?>? onHintChanged;
  const PresetSettingsSection({super.key, this.onHintChanged});

  @override
  State<PresetSettingsSection> createState() => _PresetSettingsSectionState();
}

class _PresetSettingsSectionState extends State<PresetSettingsSection> {
  @override
  Widget build(BuildContext context) {
    final state = context.watch<AppState>();
    final catalog = state.presets;
    final t = context.tokens;
    return MouseRegion(
      onEnter: (_) => widget.onHintChanged?.call(
        'домены, ip и приложения обновляются автоматически.',
      ),
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
                    'автообновление списков',
                    style: TextStyle(
                      color: t.text,
                      fontSize: 14,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ),
                NekkoSwitch(
                  value: catalog?.syncEnabled ?? false,
                  onChanged: catalog == null || state.presetsBusy
                      ? null
                      : state.setPresetSync,
                ),
              ],
            ),
            if (state.presetError != null ||
                (catalog?.error.isNotEmpty ?? false))
              Text(
                state.presetError ?? catalog!.error,
                style: TextStyle(color: t.bad, fontSize: 11),
              ),
            if (catalog == null && state.presetError != null)
              TextButton(
                onPressed: state.refreshPresetState,
                child: const Text('повторить'),
              ),
          ],
        ),
      ),
    );
  }
}
