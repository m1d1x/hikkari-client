import 'package:flutter/material.dart';

import '../theme/tokens.dart';

/// iOS-style toggle. 46×28 track, 24×24 knob.
class NekkoSwitch extends StatelessWidget {
  final bool value;
  final ValueChanged<bool>? onChanged;
  const NekkoSwitch({super.key, required this.value, required this.onChanged});

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
          width: 46,
          height: 28,
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
              width: 24,
              height: 24,
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
