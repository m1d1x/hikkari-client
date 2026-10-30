import 'package:flutter/material.dart';

import '../theme/tokens.dart';

/// Один сегмент [NekkoSegmented]: значение (id) + подпись.
class NekkoSegment {
  final String value;
  final String label;
  const NekkoSegment(this.value, this.label);
}

/// Мультитогл-переключатель (segmented control) — тёмный трек с равными
/// «пилюлями» внутри. Активная подсвечена `surface2`, неактивные — прозрачные
/// с ховером. Вынесен из маршрутизации (домены/приложения/ip).
class NekkoSegmented extends StatelessWidget {
  final List<NekkoSegment> segments;
  final String selected;
  final ValueChanged<String> onChanged;

  const NekkoSegmented({
    super.key,
    required this.segments,
    required this.selected,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
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
          for (var i = 0; i < segments.length; i++) ...[
            if (i > 0) const SizedBox(width: 4),
            Expanded(
              child: _Pill(
                label: segments[i].label,
                active: selected == segments[i].value,
                onTap: () => onChanged(segments[i].value),
              ),
            ),
          ],
        ],
      ),
    );
  }
}

class _Pill extends StatefulWidget {
  final String label;
  final bool active;
  final VoidCallback onTap;
  const _Pill({
    required this.label,
    required this.active,
    required this.onTap,
  });

  @override
  State<_Pill> createState() => _PillState();
}

class _PillState extends State<_Pill> {
  bool _hover = false;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final active = widget.active;
    final hoverInactive = _hover && !active;
    return MouseRegion(
      cursor: SystemMouseCursors.click,
      onEnter: (_) => setState(() => _hover = true),
      onExit: (_) => setState(() => _hover = false),
      child: GestureDetector(
        onTap: widget.onTap,
        behavior: HitTestBehavior.opaque,
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 150),
          decoration: BoxDecoration(
            color: active
                ? t.surface2
                : (hoverInactive
                    ? t.surface2.withValues(alpha: 0.4)
                    : Colors.transparent),
            borderRadius: BorderRadius.circular(10),
          ),
          child: Center(
            child: TweenAnimationBuilder<Color?>(
              tween: ColorTween(
                end: active ? t.text : (hoverInactive ? t.text : t.textMuted),
              ),
              duration: const Duration(milliseconds: 150),
              builder: (_, color, __) => Text(
                widget.label,
                style: TextStyle(
                  color: color,
                  fontSize: 12,
                  fontWeight: FontWeight.w500,
                  height: 1,
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}
