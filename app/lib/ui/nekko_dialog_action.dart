import 'package:flutter/material.dart';

import '../theme/tokens.dart';

class NekkoDialogAction extends StatefulWidget {
  final IconData? icon;
  final String? label;
  final Widget? child;
  final bool enabled;
  final bool primary;
  final bool destructive;
  final VoidCallback onPressed;

  const NekkoDialogAction({
    super.key,
    this.icon,
    this.label,
    this.child,
    required this.enabled,
    this.primary = false,
    this.destructive = false,
    required this.onPressed,
  });

  @override
  State<NekkoDialogAction> createState() => _NekkoDialogActionState();
}

class _NekkoDialogActionState extends State<NekkoDialogAction> {
  bool _hovered = false;
  bool _focused = false;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final accent = widget.destructive ? t.bad : t.primary;
    final hovered = widget.enabled && (_hovered || _focused);
    final foreground = widget.primary ? t.bg : t.text;
    final quietForeground = widget.destructive
        ? t.bad
        : (hovered ? t.text : t.textMuted);
    return MouseRegion(
      cursor: widget.enabled
          ? SystemMouseCursors.click
          : SystemMouseCursors.basic,
      onEnter: (_) => setState(() => _hovered = true),
      onExit: (_) => setState(() => _hovered = false),
      child: AnimatedContainer(
        duration: AppDurations.hover,
        curve: AppCurves.hover,
        decoration: BoxDecoration(
          color: widget.primary
              ? (hovered
                    ? Color.alphaBlend(
                        Colors.black.withValues(alpha: 0.15),
                        accent,
                      )
                    : accent)
              : (hovered ? t.surface2 : Colors.transparent),
          border: null,
          borderRadius: BorderRadius.circular(t.radius),
        ),
        child: TextButton(
          onPressed: widget.enabled ? widget.onPressed : null,
          onFocusChange: (value) => setState(() => _focused = value),
          style: TextButton.styleFrom(
            padding: EdgeInsets.zero,
            minimumSize: Size.zero,
            overlayColor: Colors.transparent,
            tapTargetSize: MaterialTapTargetSize.shrinkWrap,
            shape: RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(t.radius),
            ),
          ),
          child: SizedBox(
            height: 34,
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 14),
              child:
                  widget.child ??
                  Row(
                    mainAxisSize: MainAxisSize.min,
                    crossAxisAlignment: CrossAxisAlignment.center,
                    children: [
                      if (widget.icon != null) ...[
                        TweenAnimationBuilder<Color?>(
                          tween: ColorTween(
                            end: widget.primary ? foreground : quietForeground,
                          ),
                          duration: AppDurations.hover,
                          curve: AppCurves.hover,
                          builder: (context, color, _) =>
                              Icon(widget.icon, size: 14, color: color),
                        ),
                        if (widget.label != null) const SizedBox(width: 6),
                      ],
                      if (widget.label != null)
                        TweenAnimationBuilder<Color?>(
                          tween: ColorTween(
                            end: widget.primary ? foreground : quietForeground,
                          ),
                          duration: AppDurations.hover,
                          curve: AppCurves.hover,
                          builder: (context, color, _) => Text(
                            widget.label!,
                            style: TextStyle(
                              color: color,
                              fontSize: 12,
                              height: 1,
                              fontWeight: widget.primary
                                  ? FontWeight.w600
                                  : FontWeight.w400,
                            ),
                          ),
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
