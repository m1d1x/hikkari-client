import 'package:flutter/material.dart';

import '../theme/tokens.dart';

/// Единое текстовое поле проекта — тёмная оболочка (`bg` + рамка `border`,
/// высота 40, скруглённая) с голым [TextField] внутри. Заменяет разрозненные
/// копии в маршрутизации, поиске по профилям и в пикере приложений.
///
/// Почему именно так, а не просто `TextField` с темой: глобальная
/// `inputDecorationTheme` подмешивается даже в «пустую» декорацию и рисует
/// свой фон/рамку/паддинги. Поэтому здесь всё явно погашено
/// (`filled:false`-стиль через `Colors.transparent`, `InputBorder.none`),
/// а весь визуал даёт внешний [Container]. Цвета берутся из [NekkoTokens] —
/// поле корректно перекрашивается при смене темы.
class NekkoTextField extends StatelessWidget {
  final TextEditingController? controller;
  final FocusNode? focusNode;

  /// Плейсхолдер (`hintText`).
  final String hint;

  /// Текст тултипа при наведении. Пусто — тултип не показывается.
  final String tooltip;

  final ValueChanged<String>? onChanged;
  final ValueChanged<String>? onSubmitted;

  final bool autofocus;
  final TextInputAction? textInputAction;
  final int maxLines;

  /// Ведущая иконка (лупа поиска и т.п.). Меняет внутренние отступы под себя.
  final Widget? prefixIcon;

  const NekkoTextField({
    super.key,
    this.controller,
    this.focusNode,
    this.hint = '',
    this.tooltip = '',
    this.onChanged,
    this.onSubmitted,
    this.autofocus = false,
    this.textInputAction,
    this.maxLines = 1,
    this.prefixIcon,
  });

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

    final field = Container(
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
          controller: controller,
          focusNode: focusNode,
          autofocus: autofocus,
          style: textStyle,
          cursorColor: t.text,
          cursorWidth: 1.5,
          cursorRadius: const Radius.circular(1),
          maxLines: maxLines,
          textAlignVertical: TextAlignVertical.center,
          textInputAction: textInputAction,
          onChanged: onChanged,
          onSubmitted: onSubmitted,
          decoration: InputDecoration(
            isDense: true,
            filled: true,
            fillColor: Colors.transparent,
            hoverColor: Colors.transparent,
            focusColor: Colors.transparent,
            // С иконкой — отступ слева отдаём префиксу; без — обычные поля.
            contentPadding: prefixIcon != null
                ? const EdgeInsets.fromLTRB(0, 9, 0, 11)
                : const EdgeInsets.fromLTRB(14, 13, 14, 9),
            border: InputBorder.none,
            enabledBorder: InputBorder.none,
            focusedBorder: InputBorder.none,
            hintText: hint,
            hintStyle: textStyle.copyWith(color: t.textFaint),
            prefixIcon: prefixIcon,
            prefixIconConstraints: prefixIcon != null
                ? const BoxConstraints(minWidth: 40, minHeight: 40)
                : null,
          ),
        ),
      ),
    );

    if (tooltip.isEmpty) return field;
    return Tooltip(message: tooltip, child: field);
  }
}
