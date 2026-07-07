import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:simple_icons/simple_icons.dart';

import '../core/models.dart';
import '../state/app_state.dart';
import '../state/uptime_stats.dart';
import '../theme/tokens.dart';
import 'server_cleanliness_card.dart';
import 'service_icon.dart';
import 'sparkline.dart';

/// Метаданные одного пингуемого сервиса. `samples` больше не генерируются
/// локально — они подтягиваются из реальной истории проб на стороне движка
/// (`AppState.serviceUptimeHistory()`, ключ `"<activeTag>|<name>"`), поэтому
/// пережидают переоткрытие шита.
class _ServicePing {
  final String name;
  final String host;
  final IconData icon;
  final Color color;
  final bool custom;
  // Для кастомных сервисов иконка тянется по сети (favicon домена). Если null —
  // рисуем статичную [icon]. У встроенных сервисов всегда null (иконки заданы
  // вручную в `_defaultServices()`).
  final String? faviconUrl;
  List<PingSample> samples;

  _ServicePing({
    required this.name,
    required this.host,
    required this.icon,
    required this.color,
    this.custom = false,
    this.faviconUrl,
    this.samples = const [],
  });

  /// URL, через который движок гоняет Clash-API delay-пробу. Совпадает по
  /// форме с движковым `DefaultDelayURL` (`.../generate_204`) — лёгкий
  /// Android-style connectivity-эндпоинт, отдающий 204 без тела.
  String get probeUrl => 'https://$host/generate_204';
}

/// Нормализует то, что юзер ввёл в диалог "свой эндпоинт", в чистый hostname:
/// срезает схему (`https://`), путь/квери и завершающий слэш, приводит к
/// нижнему регистру. `https://www.reddit.com/r/x` → `www.reddit.com`.
String _hostFromInput(String raw) {
  var s = raw.trim().toLowerCase();
  final scheme = s.indexOf('://');
  if (scheme != -1) s = s.substring(scheme + 3);
  final slash = s.indexOf('/');
  if (slash != -1) s = s.substring(0, slash);
  return s;
}

/// Короткое человекочитаемое имя из хоста для кастомных сервисов: убираем
/// `www.`, берём "содержательную" часть и капитализируем.
/// `discord.com` → `Discord`, `www.reddit.com` → `Reddit`,
/// `chat.openai.com` → `Openai`. Не public-suffix-list — простое правило:
/// это последняя часть перед TLD (для 2 частей) или предпоследняя (для 3+,
/// чтобы `chat.openai.com` дал `openai`, а не `chat`).
String _shortNameFromHost(String host) {
  var h = host;
  if (h.startsWith('www.')) h = h.substring(4);
  final parts = h.split('.').where((p) => p.isNotEmpty).toList();
  if (parts.isEmpty) return host;
  // 1 часть (localhost / ip-огрызок) — как есть; 2 части (domain.tld) — domain;
  // 3+ (sub.domain.tld) — берём часть перед TLD (domain).
  final String pick =
      parts.length >= 2 ? parts[parts.length - 2] : parts.first;
  if (pick.isEmpty) return host;
  return pick[0].toUpperCase() + pick.substring(1);
}

/// Извлекает host из полного URL (обратная к `probeUrl`).
String _hostFromUrl(String url) {
  var s = url;
  final scheme = s.indexOf('://');
  if (scheme != -1) s = s.substring(scheme + 3);
  final slash = s.indexOf('/');
  if (slash != -1) s = s.substring(0, slash);
  return s;
}

/// URL фавикона домена через публичный резолвер Google. Без доп. зависимостей —
/// грузится обычным `Image.network`.
String _faviconUrlForHost(String host) =>
    'https://www.google.com/s2/favicons?domain=$host&sz=64';

/// Bottom sheet: пинги до популярных сервисов вместо технических подробностей
/// профиля. Открывается по ПКМ на карточке профиля. Пробы реальные —
/// туннелируются через активный VPN-профиль (Clash-API delay), история
/// хранится на стороне движка.
class ServicePingSheet extends StatefulWidget {
  final String profileLabel;

  const ServicePingSheet({super.key, required this.profileLabel});

  static Future<void> show(BuildContext context, String profileLabel) {
    final t = context.tokens;
    final state = context.read<AppState>();
    return showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      backgroundColor: t.surface,
      barrierColor: Colors.black54,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(t.radiusLg)),
      ),
      builder: (_) => ChangeNotifierProvider<AppState>.value(
        value: state,
        child: ServicePingSheet(profileLabel: profileLabel),
      ),
    );
  }

  @override
  State<ServicePingSheet> createState() => _ServicePingSheetState();
}

class _ServicePingSheetState extends State<ServicePingSheet> {
  late final List<_ServicePing> _services;

  @override
  void initState() {
    super.initState();
    _services = _defaultServices();
    _loadCustomServices();
  }

  Future<void> _loadCustomServices() async {
    final state = context.read<AppState>();
    final custom = await state.core.listCustomServices();
    if (!mounted) return;
    setState(() {
      for (final svc in custom) {
        final host = _hostFromUrl(svc.url);
        _services.add(_ServicePing(
          name: svc.name,
          host: host,
          icon: Icons.public,
          color: Colors.grey,
          custom: true,
          faviconUrl: _faviconUrlForHost(host),
        ));
      }
    });
  }

  // Brand glyphs from `simple_icons` + their official colors, so the row
  // icons read as "the actual service" rather than generic Material glyphs.
  // ChatGPT has no simple_icons entry (no dedicated brand glyph shipped in
  // that pack) — kept as a Material fallback.
  List<_ServicePing> _defaultServices() {
    return [
      _ServicePing(
        name: 'YouTube',
        host: 'youtube.com',
        icon: SimpleIcons.youtube,
        color: SimpleIconColors.youtube,
      ),
      _ServicePing(
        name: 'Telegram',
        host: 'telegram.org',
        icon: SimpleIcons.telegram,
        color: SimpleIconColors.telegram,
      ),
      _ServicePing(
        name: 'Discord',
        host: 'discord.com',
        icon: SimpleIcons.discord,
        color: SimpleIconColors.discord,
      ),
      _ServicePing(
        name: 'Google',
        host: 'google.com',
        icon: SimpleIcons.google,
        color: SimpleIconColors.google,
      ),
      _ServicePing(
        name: 'Instagram',
        host: 'instagram.com',
        icon: SimpleIcons.instagram,
        color: SimpleIconColors.instagram,
      ),
      _ServicePing(
        name: 'Steam',
        host: 'steampowered.com',
        icon: SimpleIcons.steam,
        // Steam's own brand color is pure black — invisible on our dark
        // surface, so we use a light neutral instead of the official hex.
        color: const Color(0xFFC7D0D8),
      ),
      _ServicePing(
        name: 'ChatGPT',
        host: 'chat.openai.com',
        icon: Icons.chat_bubble_outline,
        color: const Color(0xFF7FD9B0),
      ),
    ];
  }

  Future<bool> _confirmDismiss(_ServicePing svc) async {
    // Свой эндпоинт удаляем сразу (юзер сам его добавил — меньше трения).
    // Встроенный сервис — с подтверждением, свайп легко зацепить случайно.
    if (svc.custom) return true;
    final t = context.tokens;
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: t.surface,
        title: const Text('Убрать сервис?'),
        content: Text(svc.name),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('Отмена'),
          ),
          TextButton(
            onPressed: () => Navigator.pop(ctx, true),
            style: TextButton.styleFrom(
              foregroundColor: t.bad,
              overlayColor: Colors.transparent,
            ),
            child: const Text('Убрать'),
          ),
        ],
      ),
    );
    return ok ?? false;
  }

  Future<void> _hostDialog(BuildContext context, {String initial = ''}) {
    final t = context.tokens;
    final ctrl = TextEditingController(text: initial);
    return showDialog<String>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: t.surface,
        title: Text(initial.isEmpty ? 'Свой эндпоинт' : 'Редактировать'),
        content: TextField(
          controller: ctrl,
          autofocus: true,
          style: TextStyle(color: t.text, fontFamily: t.monoFamily),
          decoration: const InputDecoration(hintText: 'example.com'),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: const Text('Отмена'),
          ),
          TextButton(
            onPressed: () => Navigator.pop(ctx, ctrl.text.trim()),
            child: Text(initial.isEmpty ? 'Добавить' : 'Сохранить'),
          ),
        ],
      ),
    ).then((raw) {
      if (raw == null || raw.isEmpty || raw == initial) return;
      if (!mounted) return;
      final host = _hostFromInput(raw);
      if (host.isEmpty) return;
      final name = _shortNameFromHost(host);
      final state = context.read<AppState>();
      setState(() {
        if (initial.isEmpty) {
          _services.add(_ServicePing(
            name: name,
            host: host,
            icon: Icons.public,
            color: Colors.grey,
            custom: true,
            faviconUrl: _faviconUrlForHost(host),
          ));
          state.core.addCustomService(name, 'https://$host');
        } else {
          final i = _services.indexWhere((s) => s.host == initial);
          if (i == -1) return;
          final old = _services[i];
          if (old.custom) {
            state.core.removeCustomService(old.name);
          }
          final newName = old.custom ? _shortNameFromHost(host) : old.name;
          _services[i] = _ServicePing(
            name: newName,
            host: host,
            icon: old.icon,
            color: old.color,
            samples: old.samples,
            custom: old.custom,
            faviconUrl: old.custom ? _faviconUrlForHost(host) : old.faviconUrl,
          );
          if (old.custom) {
            state.core.addCustomService(newName, 'https://$host');
          }
        }
      });
    });
  }

  void _addCustomEndpoint() => _hostDialog(context);

  // Тап по карточке — редактирование хоста. Разрешено для любого сервиса,
  // не только кастомных: юзер может захотеть перепривязать пинг встроенного
  // YouTube/Telegram на свой домен/зеркало.
  void _editEndpoint(_ServicePing svc) =>
      _hostDialog(context, initial: svc.host);

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final s = context.watch<AppState>();
    final tag = s.activeTag;
    final hasActive = tag != null;

    // Sync samples from backend history (read-only, no FFI calls).
    if (hasActive) {
      final hist = s.serviceUptime;
      for (final svc in _services) {
        svc.samples = hist['$tag|${svc.name}'] ?? const [];
      }
    }

    return DraggableScrollableSheet(
      expand: false,
      initialChildSize: 0.68,
      minChildSize: 0.4,
      maxChildSize: 0.92,
      builder: (ctx, scrollCtrl) {
        return Column(
          children: [
            _grabber(t),
            _header(t),
            // Плашка «чистоты» текущего сервера — над всеми сервисами. Сама
            // прячется, когда туннель выключен (verdict == 'off').
            if (hasActive) const ServerCleanlinessCard(),
            Expanded(
              child: hasActive
                  ? _list(t, scrollCtrl)
                  : _emptyState(t, scrollCtrl),
            ),
          ],
        );
      },
    );
  }

  // Нет активного профиля / VPN выключен — пинговать нечего. Показываем
  // спокойное пустое состояние вместо попытки пробить туннель, которого нет.
  Widget _emptyState(NekkoTokens t, ScrollController scrollCtrl) {
    return ListView(
      controller: scrollCtrl,
      padding: const EdgeInsets.fromLTRB(24, 40, 24, 24),
      children: [
        Icon(Icons.cloud_off_outlined, color: t.textMuted, size: 40),
        const SizedBox(height: 14),
        Text(
          'нет активного профиля',
          textAlign: TextAlign.center,
          style: TextStyle(
            color: t.text,
            fontSize: 14,
            fontWeight: FontWeight.w600,
          ),
        ),
        const SizedBox(height: 6),
        Text(
          'выберите профиль и включите VPN,\nчтобы пинговать сервисы через него',
          textAlign: TextAlign.center,
          style: TextStyle(color: t.textMuted, fontSize: 12, height: 1.4),
        ),
      ],
    );
  }

  Widget _list(NekkoTokens t, ScrollController scrollCtrl) {
    return ListView.builder(
      controller: scrollCtrl,
      padding: const EdgeInsets.fromLTRB(16, 6, 16, 16),
      itemCount: _services.length,
      itemBuilder: (_, i) {
        final svc = _services[i];
        return _serviceRow(t, svc, i, key: ValueKey(svc));
      },
    );
  }

  Widget _grabber(NekkoTokens t) => Padding(
        padding: const EdgeInsets.only(top: 6, bottom: 2),
        child: Container(
          width: 36,
          height: 4,
          decoration: BoxDecoration(
            color: t.borderStrong,
            borderRadius: BorderRadius.circular(2),
          ),
        ),
      );

  Widget _header(NekkoTokens t) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(20, 2, 12, 8),
      child: SizedBox(
        height: 34,
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.center,
          children: [
            Row(
              crossAxisAlignment: CrossAxisAlignment.baseline,
              textBaseline: TextBaseline.alphabetic,
              children: [
                Text(
                  'доступность сервисов',
                  style: TextStyle(
                    color: t.text,
                    fontSize: 14,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                const SizedBox(width: 8),
                Text(
                  widget.profileLabel,
                  style: TextStyle(
                    color: t.textMuted,
                    fontSize: 12,
                    fontFamily: t.monoFamily,
                  ),
                ),
              ],
            ),
            const Spacer(),
            _addButton(t),
          ],
        ),
      ),
    );
  }

  Widget _addButton(NekkoTokens t) {
    return Tooltip(
      message: 'добавить свой эндпоинт',
      child: Material(
        color: Colors.transparent,
        borderRadius: BorderRadius.circular(t.radius),
        child: InkWell(
          onTap: _addCustomEndpoint,
          mouseCursor: SystemMouseCursors.click,
          borderRadius: BorderRadius.circular(t.radius),
          hoverColor: t.primary.withValues(alpha: 0.10),
          child: Container(
            width: 34,
            height: 34,
            alignment: Alignment.center,
            decoration: BoxDecoration(
              border: Border.all(color: t.borderStrong),
              borderRadius: BorderRadius.circular(t.radius),
            ),
            child: Icon(Icons.add, color: t.text, size: 18),
          ),
        ),
      ),
    );
  }

  // Три жеста, которые не конфликтуют друг с другом, потому что различаются
  // не направлением, а типом взаимодействия:
  //  · свайп влево         — удалить (Dismissible слушает горизонтальный drag);
  //  · тап                 — редактировать хост (для любого сервиса);
  //  · long-press + drag   — переставить местами (ReorderableDelayedDragStart-
  //    Listener ждёт задержку, поэтому обычный тап/свайп до неё не доходят).
  // Отдельной иконки-хендла для реордера больше нет — не нагромождает
  // список ради опции, которой пользуются от случая к случаю.
  Widget _serviceRow(NekkoTokens t, _ServicePing svc, int index,
      {required Key key}) {
    // recentAvgRtt даёт стабильное "сейчас" (окно последних проб), avgRtt —
    // общий фон. -1 = нет успешных проб. Пустая история = ещё не пинговали.
    final noData = svc.samples.isEmpty;
    final avg = UptimeStats.recentAvgRtt(svc.samples);
    final dead = !noData && (avg < 0 || avg > 1000);
    final rttText = noData ? '…' : (dead ? 'down' : '$avg ms');

    // ClipRRect общий на фон + карточку — иначе при свайпе видно шов между
    // независимо скруглёнными Container'ами (красный фон торчал прямоуголь-
    // ным углом поверх круглой карточки).
    return Padding(
      key: key,
      padding: const EdgeInsets.only(bottom: 8),
      child: ClipRRect(
        borderRadius: BorderRadius.circular(t.radiusLg),
        child: Dismissible(
          key: ValueKey('${key.hashCode}-dismiss'),
          direction: DismissDirection.endToStart,
          confirmDismiss: (_) => _confirmDismiss(svc),
          onDismissed: (_) {
            if (svc.custom) {
              context.read<AppState>().core.removeCustomService(svc.name);
            }
            setState(() => _services.remove(svc));
          },
          background: Container(
            color: t.bad.withValues(alpha: 0.16),
            padding: const EdgeInsets.symmetric(horizontal: 18),
            alignment: Alignment.centerRight,
            child: Icon(Icons.delete_outline, color: t.bad, size: 20),
          ),
          child: ColoredBox(
              color: t.surface2,
              child: Material(
                color: Colors.transparent,
                child: InkWell(
                  onTap: () => _editEndpoint(svc),
                  mouseCursor: SystemMouseCursors.click,
                  // Ховер цветом самого сервиса, а не общим акцентом темы —
                  // у карточек разные цвета (YouTube красный, Telegram синий
                  // и т.д.), единый розовый primary смотрелся чужеродно.
                  hoverColor: svc.color.withValues(alpha: 0.08),
                  child: Padding(
                    padding: const EdgeInsets.fromLTRB(12, 10, 14, 10),
                    child: Row(
                      children: [
                        ServiceIconBadge(
                          icon: svc.icon,
                          color: svc.color,
                          faviconUrl: svc.faviconUrl,
                        ),
                        const SizedBox(width: 10),
                        SizedBox(
                          width: 96,
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            mainAxisSize: MainAxisSize.min,
                            children: [
                              Text(
                                svc.name,
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis,
                                style: TextStyle(
                                  color: t.text,
                                  fontSize: 13,
                                  fontWeight: FontWeight.w600,
                                ),
                              ),
                              Text(
                                svc.host,
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis,
                                style: TextStyle(
                                  color: t.textMuted,
                                  fontSize: 11,
                                  fontFamily: t.monoFamily,
                                ),
                              ),
                            ],
                          ),
                        ),
                        const SizedBox(width: 8),
                        Expanded(
                          child: SizedBox(
                            height: 30,
                            child: Sparkline(
                              samples: svc.samples,
                              color: dead ? t.bad : t.ok,
                            ),
                          ),
                        ),
                        const SizedBox(width: 8),
                        SizedBox(
                          width: 52,
                          child: Text(
                            rttText,
                            textAlign: TextAlign.right,
                            // Обрезаем встроенный ascent/descent шрифта —
                            // иначе "…"/"57 ms" визуально не совпадают по
                            // вертикали с иконкой сервиса и графиком рядом.
                            textHeightBehavior: const TextHeightBehavior(
                              applyHeightToFirstAscent: false,
                              applyHeightToLastDescent: false,
                            ),
                            style: TextStyle(
                              color: dead
                                  ? t.bad
                                  : (noData ? t.textMuted : t.text),
                              fontSize: 12,
                              fontWeight: FontWeight.w600,
                              fontFamily: t.monoFamily,
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
      ),
    );
  }
}
