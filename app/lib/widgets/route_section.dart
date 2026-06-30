import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../core/models.dart';
import '../state/app_state.dart';
import '../theme/tokens.dart';

/// "Маршрутизация" section — master toggle + mode switch + three category
/// tabs + the items list. Layout is a single card whose body collapses;
/// expand state is local (where the user is in the UI), but everything
/// data-shaped (enabled flag, mode, lists) lives in [AppState.routing].
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

  // Header: title + chevron + mode-flip button + master switch.
  // Tap anywhere on the row toggles collapse — except the mode button and
  // the switch, which absorb their own taps and don't bubble.
  Widget _head(NekkoTokens t, AppState s) {
    return InkWell(
      onTap: () => setState(() => _open = !_open),
      mouseCursor: SystemMouseCursors.click,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
        child: Row(
          children: [
            Text(
              'маршрутизация',
              style: TextStyle(
                color: t.text,
                fontSize: 13,
                fontWeight: FontWeight.w500,
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
            const Spacer(),
            _ModeFlipButton(state: s),
            const SizedBox(width: 12),
            _IosSwitch(
              value: s.routing.enabled,
              onChanged: (v) => s.setRoutingEnabled(v),
            ),
          ],
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
          _ModeCaption(state: s),
          const SizedBox(height: 10),
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


/// Passive caption: just tells the user what the current routing mode means.
/// The actual flip lives in the header [_ModeFlipButton] now, so this row
/// is non-interactive — it reads as documentation that follows the button.
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

// =============================================================================
//  МИНИ-ТУТОРИАЛ ПО FLUTTER НА ПРИМЕРЕ ЭТОЙ КНОПКИ
// =============================================================================
//
//  Flutter рисует UI как ДЕРЕВО ВИДЖЕТОВ. Каждый виджет — это либо:
//   • StatelessWidget — статичный, перерисовывается только когда родитель его
//     перестроит. Пример в файле: _IosSwitch, _TabPill, _AddButton.
//   • StatefulWidget — у него есть внутреннее состояние (поля _xxx), при
//     setState() он перерисовывается. Пример: эта самая _ModeFlipButton — она
//     помнит _hover и сама себя перерисовывает на наведение мыши.
//
//  StatefulWidget = два класса:
//    1) сам виджет (`_ModeFlipButton`) — иммутабельный, хранит входные
//       параметры (тут это `state`).
//    2) его State (`_ModeFlipButtonState`) — мутабельный, тут живёт `_hover`
//       и метод build(), который собирает дерево.
//
//  Виджеты обычно ВЛОЖЕНЫ друг в друга через `child:` или `children:`. Здесь
//  цепочка такая (читай как матрёшку):
//
//    Opacity         — приглушает всё внутри (если dim==true → 50%).
//      Tooltip       — всплывающая подсказка при наведении.
//        MouseRegion — ловит вход/выход мыши → меняет _hover.
//          GestureDetector — ловит клик, вызывает setRoutingMode().
//            AnimatedContainer — фон, размер, скругление + плавная анимация.
//              Row(children:[ AnimatedSwitcher(Text), SizedBox, Icon ])
//                — горизонтальный ряд: текст, отступ 6px, иконка.
//
//  Hot reload (`r` в `flutter run`) перерисовывает дерево БЕЗ перезапуска
//  приложения — твои правки появляются за 100мс. Если поменял поле State
//  или импорт — нужен hot RESTART (`R`).
//
// =============================================================================

/// Header-level mode-flip button. Compact pill that swaps proxy↔bypass on
/// tap. Disabled (and dimmed) when master is off.
class _ModeFlipButton extends StatefulWidget {
  // `final` поле = принятый снаружи параметр. Передаётся через конструктор
  // вызывающей стороной: `_ModeFlipButton(state: s)`.
  final AppState state;
  const _ModeFlipButton({required this.state});

  // Связь между виджетом и его state'ом. Flutter сам это вызовет один раз.
  @override
  State<_ModeFlipButton> createState() => _ModeFlipButtonState();
}

class _ModeFlipButtonState extends State<_ModeFlipButton> {
  // Локальное состояние — только этот виджет о нём знает. Меняется через
  // setState(() => _hover = ...), что триггерит перерисовку build() ниже.
  bool _hover = false;

  // build() Flutter дёргает каждый раз когда:
  //   • поменялся _hover (через setState)
  //   • родитель сказал «перерисуйся» (например AppState уведомил)
  // Возвращай тут дерево виджетов — Flutter сам разберётся что менять
  // в реальном рендере.
  @override
  Widget build(BuildContext context) {
    // ВЫЧИСЛЕНИЯ — здесь готовим всё что зависит от текущего состояния.
    // Эти переменные пересчитываются на каждый build, это нормально.

    // `t` = токены темы (цвета, радиусы, шрифт). Все цвета бери отсюда,
    // а не хардкоди — тогда тёмная/светлая тема будут работать сами.
    // Чтобы посмотреть что внутри: ctrl+click на `tokens` → откроется файл.
    final t = context.tokens;

    // `s` = AppState (наш глобальный стейт). Через него читаем routing-режим
    // и вызываем действия (setRoutingMode переключит и запустит debounce).
    final s = widget.state;

    // dim = true когда master-свитч маршрутизации выключен. В этом случае
    // кнопку показываем тусклой и не реагируем на клики/hover.
    final dim = !s.routing.enabled;

    // Какой режим будет после клика.
    final nextMode = s.routing.mode == RoutingMode.bypass
        ? RoutingMode.proxy
        : RoutingMode.bypass;

    // Текст подсказки (Tooltip) — что произойдёт по клику.
    final tooltipText = s.routing.mode == RoutingMode.bypass
        ? 'через vpn пойдёт ТОЛЬКО список'
        : 'через vpn пойдёт ВСЁ, кроме списка';

    // Лейбл на самой кнопке = текущий режим. Юзер видит "в каком я сейчас",
    // клик свапает.
    final label = s.routing.mode == RoutingMode.bypass ? 'bypass' : 'proxy';

    // ===== ВОТ ОН — ЦВЕТ ФОНА =====
    // Поменял на розовый акцент темы (`t.primary`) с прозрачностью, чтобы
    // ты увидел эффект. Конструкция `cond ? a : b` = тернарный оператор:
    //   "если cond то a, иначе b". Тут вложенная — три ветки:
    //     dim  → прозрачный (выключенная маршрутизация = кнопка-призрак)
    //     hover → плотный розовый (40% непрозрачности)
    //     else → лёгкий розовый (15% непрозрачности, обычное состояние)
    //
    // ЭКСПЕРИМЕНТЫ — закомментируй текущий блок и попробуй ОДИН из:
    //   • `final bg = Colors.transparent;`           // вообще нет фона
    //   • `final bg = t.bg;`                         // как страница
    //   • `final bg = const Color(0xFF00FF00);`      // лютый зелёный
    //   • `final bg = t.surface2;`                   // как было раньше
    final bg = dim
        ? Colors.transparent
        : (_hover
            ? t.primary.withValues(alpha: 0.40)
            : t.primary.withValues(alpha: 0.15));

    // fg = foreground = цвет текста и иконки.
    final fg = dim ? t.textFaint : (_hover ? t.text : t.textMuted);

    // ===== ДЕРЕВО ВИДЖЕТОВ =====
    return Opacity(
      // Если dim → весь виджет показывается на 50% прозрачности. Это
      // ВИЗУАЛЬНЫЙ эффект, клики всё ещё долетают (отключаем их ниже).
      opacity: dim ? 0.5 : 1,
      child: Tooltip(
        // Подсказка появляется при наведении через 500мс.
        message: dim ? '' : tooltipText,
        waitDuration: const Duration(milliseconds: 500),
        child: MouseRegion(
          // Курсор-указатель когда виджет кликабелен.
          cursor: dim
              ? SystemMouseCursors.basic
              : SystemMouseCursors.click,
          // Мышь зашла на виджет — ставим _hover=true → build() побежит
          // снова, bg и fg пересчитаются с учётом нового _hover.
          onEnter: (_) {
            if (!dim) setState(() => _hover = true);
          },
          // Мышь ушла — обратно.
          onExit: (_) => setState(() => _hover = false),
          child: GestureDetector(
            // opaque = ловить клики по всей площади child'а, даже по пустым
            // местам без визуального наполнения.
            behavior: HitTestBehavior.opaque,
            // onTap: null означает "виджет некликабельный". Так делаем когда
            // dim=true — кнопка визуально есть, но не реагирует.
            onTap: dim ? null : () => s.setRoutingMode(nextMode),
            child: AnimatedContainer(
              // ===== РАЗМЕР И АНИМАЦИЯ =====
              // Container с приставкой Animated — плавно интерполирует свои
              // параметры (color, size, padding...) между состояниями. То
              // есть когда меняется bg при hover, переход НЕ скачком, а за
              // 120мс. Попробуй поменять на 1000 — увидишь как плавно.
              duration: const Duration(milliseconds: 120),
              // height = фиксированная высота кнопки. Покрути 24..40.
              height: 28,
              // padding = ВНУТРЕННИЕ отступы (от края кнопки до её
              // содержимого). symmetric(horizontal: X) = X слева и справа.
              padding:
                  const EdgeInsets.symmetric(horizontal: 10, vertical: 0),
              // ===== ОФОРМЛЕНИЕ =====
              decoration: BoxDecoration(
                color: bg,
                // Скругление углов. Покрути 0..999.
                borderRadius: BorderRadius.circular(t.radius),
                // РАСКОММЕНТИРУЙ для бордера:
                // border: Border.all(color: t.primary, width: 1),
              ),
              // ===== СОДЕРЖИМОЕ =====
              child: Row(
                // mainAxisSize.min = Row занимает столько ширины, сколько
                // нужно содержимому. По умолчанию был бы max (растягивается).
                mainAxisSize: MainAxisSize.min,
                children: [
                  AnimatedSwitcher(
                    // Когда `child` меняется (новый key) — старый плавно
                    // исчезает, новый плавно появляется. Тут используется
                    // чтобы текст "bypass" → "proxy" перетекал, а не скакал.
                    duration: const Duration(milliseconds: 140),
                    transitionBuilder: (child, anim) =>
                        FadeTransition(opacity: anim, child: child),
                    child: Text(
                      label,
                      // key обязателен для AnimatedSwitcher — иначе он не
                      // понимает что текст ИЗМЕНИЛСЯ и не запускает анимацию.
                      key: ValueKey(label),
                      style: TextStyle(
                        color: fg,
                        fontSize: 12,          // 11..14 норм диапазон
                        fontWeight: FontWeight.w500, // w400/w500/w600/w700
                        height: 1,             // межстрочный (1.0 = впритык)
                      ),
                    ),
                  ),
                  // SizedBox с width = горизонтальный отступ. Без него
                  // текст и иконка слиплись бы.
                  const SizedBox(width: 6),
                  Icon(Icons.swap_horiz, size: 16, color: fg),
                  // ↑ Все доступные иконки: google "flutter material icons"
                  //   или https://fonts.google.com/icons
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
