import 'package:flutter/material.dart';

import '../theme/tokens.dart';

/// Поле с редактированием по двойному клику. В покое выглядит как обычный
/// текст; на ховере подсвечивается светлым фоном (сигнал «кликабельно»), по
/// двойному клику превращается в тёмный инпут. Выход — Enter или клик мимо.
///
/// Тонкости, ради которых это отдельный компонент (иначе на них тратятся часы):
/// • Один и тот же [TextField] в обоих режимах (меняем только `readOnly`) —
///   так строчные метрики совпадают и текст не «прыгает» вниз при входе.
/// • Глобальная `inputDecorationTheme` погашена явно, иначе рисует «инпут в
///   инпуте»; весь визуал даёт внешний бокс.
/// • Фокус/выделение ставятся в post-frame (после снятия `readOnly`), иначе
///   печатать нельзя, пока не сдвинешь курсор.
/// • Наш [GestureDetector] висит только в режиме просмотра — в редактировании
///   жесты целиком у [TextField], чтобы не конфликтовали распознаватели.
/// • При выходе сбрасываются фокус, выделение и ховер (в редактировании
///   `MouseRegion` снят и `onExit` не приходит — флаг «залипает»).
class NekkoInlineEditField extends StatefulWidget {
  /// Текущее значение (например, имя профиля). Синхронизируется извне.
  final String value;

  /// Вызывается с обрезанным новым значением, только если оно непустое и
  /// отличается от [value].
  final ValueChanged<String> onSubmitted;

  final String tooltip;
  final TextStyle? textStyle;

  /// Высота бокса поля. По умолчанию 30; в плотных строках (напр. строка
  /// «профиль» в развороте) уменьшается, чтобы вертикальный ритм совпал с
  /// соседними key-value строками.
  final double height;

  const NekkoInlineEditField({
    super.key,
    required this.value,
    required this.onSubmitted,
    this.tooltip = 'двойной клик = переименовать',
    this.textStyle,
    this.height = 30,
  });

  @override
  State<NekkoInlineEditField> createState() => _NekkoInlineEditFieldState();
}

class _NekkoInlineEditFieldState extends State<NekkoInlineEditField> {
  static const _padding = EdgeInsets.symmetric(horizontal: 8, vertical: 6);

  bool _editing = false;
  bool _hovered = false;
  late final TextEditingController _ctrl;
  late final FocusNode _focus;

  @override
  void initState() {
    super.initState();
    _ctrl = TextEditingController(text: widget.value);
    _focus = FocusNode();
  }

  @override
  void didUpdateWidget(NekkoInlineEditField old) {
    super.didUpdateWidget(old);
    if (old.value != widget.value && !_editing) {
      _ctrl.text = widget.value;
    }
  }

  @override
  void dispose() {
    _ctrl.dispose();
    _focus.dispose();
    super.dispose();
  }

  void _startEditing() {
    if (_editing) return;
    setState(() => _editing = true);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted || !_editing) return;
      _focus.requestFocus();
      _ctrl.selection = TextSelection(
        baseOffset: 0,
        extentOffset: _ctrl.text.length,
      );
    });
  }

  void _finishEditing() {
    final v = _ctrl.text.trim();
    setState(() {
      _editing = false;
      _hovered = false;
    });
    _focus.unfocus();
    _ctrl.selection = const TextSelection.collapsed(offset: 0);
    if (v.isEmpty || v == widget.value) return;
    widget.onSubmitted(v);
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final style = widget.textStyle ?? TextStyle(color: t.text, fontSize: 12);

    final box = AnimatedContainer(
      duration: const Duration(milliseconds: 100),
      height: widget.height,
      alignment: Alignment.centerLeft,
      decoration: BoxDecoration(
        color: _editing
            ? t.bg
            : (_hovered ? t.surface2 : Colors.transparent),
        borderRadius: BorderRadius.circular(t.radius),
        border: Border.all(
          color: _editing
              ? t.borderStrong
              : (_hovered ? t.border : Colors.transparent),
        ),
      ),
      child: Material(
        type: MaterialType.transparency,
        child: TextField(
          controller: _ctrl,
          focusNode: _focus,
          readOnly: !_editing,
          enableInteractiveSelection: _editing,
          showCursor: _editing,
          style: style,
          cursorColor: t.primary,
          cursorWidth: 1.5,
          maxLines: 1,
          textAlignVertical: TextAlignVertical.center,
          decoration: const InputDecoration(
            isDense: true,
            isCollapsed: true,
            filled: false,
            fillColor: Colors.transparent,
            hoverColor: Colors.transparent,
            focusColor: Colors.transparent,
            contentPadding: _padding,
            border: InputBorder.none,
            enabledBorder: InputBorder.none,
            focusedBorder: InputBorder.none,
          ),
          onSubmitted: (_) => _finishEditing(),
          onTapOutside: (_) {
            if (_editing) _finishEditing();
          },
        ),
      ),
    );

    if (_editing) return box;

    return MouseRegion(
      cursor: SystemMouseCursors.click,
      onEnter: (_) => setState(() => _hovered = true),
      onExit: (_) => setState(() => _hovered = false),
      child: Tooltip(
        message: widget.tooltip,
        child: GestureDetector(
          behavior: HitTestBehavior.opaque,
          onDoubleTap: _startEditing,
          // readOnly-поле иначе перехватывало бы тапы — гасим ему события,
          // чтобы двойной клик ловил GestureDetector.
          child: IgnorePointer(child: box),
        ),
      ),
    );
  }
}
