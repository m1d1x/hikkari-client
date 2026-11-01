import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../core/routing_presets.dart';
import '../state/app_state.dart';
import '../theme/tokens.dart';
import '../ui/nekko_switch.dart';
import '../ui/nekko_text_field.dart';
import 'service_icon.dart';

const _serviceAliases = <String, List<String>>{
  'steam': ['стим'],
  'faceit': ['фейсит', 'фасик', 'faceit', 'кс', 'кс2', 'cs2', 'cs', 'кска', 'античит', 'ac'],
  'discord': ['дискорд', 'дс'],
  'telegram': ['телеграм', 'телега', 'тг'],
  'youtube': ['ютуб', 'ютуп'],
  'openai': ['чатгпт', 'опенаи', 'гпт', 'gpt', 'chatgpt'],
  'claude': ['клод', 'антропик'],
  'gemini': ['джемини', 'гемини'],
  'instagram': ['инстаграм', 'инста'],
  'x': ['твиттер', 'икс', 'твит'],
  'spotify': ['спотифай', 'спотик'],
  'twitch': ['твич'],
  'notion': ['ноушен', 'нотион'],
  'figma': ['фигма'],
  'github': ['гитхаб', 'гит'],
  'google': ['гугл'],
  'sber': ['сбер', 'сбербанк', 'sber', 'сберонлайн'],
  'tbank': ['тинькофф', 'тиньк', 'тбанк', 'т-банк', 'tinkoff', 'tbank'],
  'vtb': ['втб', 'vtb', 'втб онлайн'],
  'alfa': ['альфа', 'альфабанк', 'альфа-банк', 'alfa'],
  'banks': ['банк', 'банки', 'газпромбанк', 'райффайзен', 'мир', 'юмани', 'точка'],
  'gosuslugi': ['госуслуги', 'налог', 'почта', 'мосуслуги', 'мосру', 'emias'],
  'transport-ru': [
    'ржд',
    'поезд',
    'билеты',
    'аэрофлот',
    's7',
    'победа',
    'авиабилеты',
    'самолет',
  ],
  'marketplaces': ['маркетплейсы', 'магазины', 'маркет'],
  'ozon': ['озон', 'ozon'],
  'wildberries': ['вайлдберриз', 'вб', 'wildberries', 'wb'],
  'avito': ['авито', 'avito'],
  'kinopoisk': ['кинопоиск', 'кино', 'сериалы'],
  'ivi': ['иви', 'ivi', 'кино'],
  'okko': ['окко', 'okko', 'кино'],
  'wink': ['винк', 'wink', 'ростелеком'],
  'streaming-ru': [
    'кино',
    'премьер',
    'premier',
    'старт',
    'start',
    'кион',
    'kion',
    'смотрим',
  ],
  'vk': ['вконтакте', 'вк', 'vk'],
  'vk-video': ['вк видео', 'видео вк', 'vk video'],
  'vk-play': ['вк плей', 'vk play', 'игры вк'],
  'mailru': ['мейл', 'майл', 'мэйл', 'почта мейл', 'облако мейл', 'mail'],
  'ok': ['одноклассники', 'ок', 'однокласники', 'ok'],
  'rutube': ['рутуб', 'rutube'],
  'yandex': ['яндекс', 'яша', 'почта', 'браузер'],
  'yandex-music': [
    'яндекс музыка',
    'яндекс звук',
    'музыка',
    'треки',
    'music',
    'яндексмузыка',
  ],
  'yandex-taxi': [
    'яндекс такси',
    'яндекс го',
    'яндекс go',
    'такси',
    'го',
    'go',
    'яндекс еда',
    'еда',
    'лавка',
    'доставка',
  ],
  'yandex-maps': ['яндекс карты', 'карты', 'навигатор', 'пробки', 'maps'],
  'yandex-disk': ['яндекс диск', 'диск', 'облако', 'disk'],
  'yandex-market': ['яндекс маркет', 'маркет'],
};

/// Ready-made groups share the rules list, but keep their own engine choices.
class ServicePresets extends StatefulWidget {
  final String category;
  const ServicePresets({super.key, this.category = 'domains'});

  @override
  State<ServicePresets> createState() => _ServicePresetsState();
}

class _ServicePresetsState extends State<ServicePresets> {
  final _searchCtl = TextEditingController();
  String _query = '';

  @override
  void dispose() {
    _searchCtl.dispose();
    super.dispose();
  }

  bool _matchesQuery(ServicePreset s, String platform, String q) {
    if (q.isEmpty) return true;
    if (s.name.toLowerCase().contains(q)) return true;
    if (s.id.toLowerCase().contains(q)) return true;
    final rules = _rules(s, platform, widget.category);
    if (rules.any((r) => r.toLowerCase().contains(q))) return true;
    final aliases = _serviceAliases[s.id];
    if (aliases != null && aliases.any((a) => a.contains(q) || q.contains(a))) {
      return true;
    }
    return false;
  }

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
    final q = _query.trim().toLowerCase();
    final allCategoryServices = catalog.services
        .where((s) => _rules(s, catalog.platform, widget.category).isNotEmpty)
        .toList();
    final services = allCategoryServices
        .where((s) => _matchesQuery(s, catalog.platform, q))
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
          const SizedBox(height: 10),
          if (allCategoryServices.isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(bottom: 10),
              child: NekkoTextField(
                key: const ValueKey('preset_search_field'),
                controller: _searchCtl,
                hint: 'поиск по сервисам…',
                prefixIcon: Icon(Icons.search, size: 16, color: t.textFaint),
                suffixIcon: _query.isNotEmpty
                    ? IconButton(
                        icon: Icon(Icons.close, size: 14, color: t.textMuted),
                        onPressed: () {
                          _searchCtl.clear();
                          setState(() => _query = '');
                        },
                        hoverColor: Colors.transparent,
                        splashColor: Colors.transparent,
                        highlightColor: Colors.transparent,
                      )
                    : null,
                onChanged: (v) => setState(() => _query = v),
              ),
            ),
          for (final service in services)
            _ServiceRow(
              key: ValueKey('${widget.category}_${service.id}'),
              service: service,
              catalog: catalog,
              category: widget.category,
              selected: catalog.selected(service, state.routing.mode),
              onChanged: state.presetsBusy
                  ? null
                  : (on) => state.setServicePreset(service.id, on),
            ),
          if (allCategoryServices.isEmpty)
            Text(
              'нет готовых правил для этой категории',
              textAlign: TextAlign.center,
              style: TextStyle(color: t.textFaint, fontSize: 11),
            )
          else if (services.isEmpty)
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 8),
              child: Text(
                'ничего не найдено',
                textAlign: TextAlign.center,
                style: TextStyle(color: t.textFaint, fontSize: 11),
              ),
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
    final (icon, brand) = resolveServiceIcon(widget.service.icon, t.textMuted);
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
