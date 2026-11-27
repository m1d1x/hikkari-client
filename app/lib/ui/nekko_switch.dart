import 'package:flutter/material.dart';

import '../theme/tokens.dart';

/// iOS-style toggle. 46×28 track, 24×24 knob; [dense] is the 30×18 one with
/// a 14×14 knob that sits inside a list row without growing it.
class NekkoSwitch extends StatelessWidget {
  final bool value;
  final ValueChanged<bool>? onChanged;
  final bool dense;
  const NekkoSwitch({
    super.key,
    required this.value,
    required this.onChanged,
    this.dense = false,
  });

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return MouseRegion(
      cursor: onChanged == null
          ? SystemMouseCursors.basic
          : SystemMouseCursors.click,
      child: GestureDetector(
        onTap: onChanged == null ? null : () => onChanged!(!value),
        behavior: HitTestBehavior.opaque,
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 250),
          curve: AppCurves.fastInSlowOut,
          width: dense ? 30 : 46,
          height: dense ? 18 : 28,
          padding: const EdgeInsets.all(2),
          decoration: BoxDecoration(
            color: value ? t.primary : t.borderStrong,
            borderRadius: BorderRadius.circular(999),
          ),
          child: AnimatedAlign(
            duration: const Duration(milliseconds: 250),
            curve: AppCurves.fastInSlowOut,
            alignment: value ? Alignment.centerRight : Alignment.centerLeft,
            child: Container(
              width: dense ? 14 : 24,
              height: dense ? 14 : 24,
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
