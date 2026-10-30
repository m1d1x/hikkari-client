/// The shared «скопировано» copy confirmation, ported from Nekko's CopyFlash.
/// Wrap a copyable body in it and fire [CopyFlashState.flash] on copy: the
/// body recedes and a bouncy check + label take its place, then it settles
/// back to the body.
library;

import 'dart:async';

import 'package:flutter/material.dart';

import '../theme/tokens.dart';

class CopyFlash extends StatefulWidget {
  const CopyFlash({
    super.key,
    required this.child,
    this.color,
    this.iconSize = 15,
    this.fontSize = 13,
    this.bodyOpacity = 0.45,
  });

  /// The body the confirmation replaces while playing. It is dimmed, not
  /// removed, so the host never changes size.
  final Widget child;

  /// Check and label colour; the theme primary when null.
  final Color? color;

  final double iconSize;
  final double fontSize;

  /// How far the body fades while the confirmation is up. A host that already
  /// dims its own body under an overlay passes 1.
  final double bodyOpacity;

  @override
  State<CopyFlash> createState() => CopyFlashState();
}

class CopyFlashState extends State<CopyFlash>
    with SingleTickerProviderStateMixin {
  // Snappy staggered check → label entrance; the exit is quicker still so the
  // body snaps back to work.
  late final AnimationController _ctl = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 225),
    reverseDuration: const Duration(milliseconds: 190),
  );
  Timer? _timer;

  /// Runs while the confirmation is on screen; hosts listen to it to hide
  /// their own hover affordances for the duration.
  Animation<double> get animation => _ctl;

  /// Reveal the confirmation, hold briefly, then settle back.
  void flash() {
    _timer?.cancel();
    unawaited(_ctl.forward(from: 0));
    _timer = Timer(const Duration(milliseconds: 450), () {
      if (mounted) unawaited(_ctl.reverse());
    });
  }

  @override
  void dispose() {
    _timer?.cancel();
    _ctl.dispose();
    super.dispose();
  }

  /// Remap [v] onto [start]..[end] → 0..1 to stagger the check and the label.
  static double _seg(double v, double start, double end) =>
      ((v - start) / (end - start)).clamp(0.0, 1.0);

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: _ctl,
    child: widget.child,
    builder: (context, child) {
      final v = _ctl.value;
      if (v == 0) return child!;

      final color = widget.color ?? context.tokens.primary;
      final leaving = _ctl.status == AnimationStatus.reverse;
      final reveal = Curves.easeOut.transform(v);

      // The check stamps in: elastic scale, un-twisting from a tilt.
      final checkRaw = _seg(v, 0.0, 0.5);
      final checkScaleT = leaving
          ? Curves.easeInBack.transform(checkRaw)
          : Curves.elasticOut.transform(checkRaw);
      final checkEase = leaving
          ? Curves.easeIn.transform(checkRaw)
          : Curves.easeOut.transform(checkRaw);

      // The label slings in from behind the check a beat later, scaling up
      // from 0.8 with a small overshoot.
      final labelRaw = _seg(v, 0.2, 0.9);
      final labelBack = leaving
          ? Curves.easeInCubic.transform(labelRaw)
          : Curves.easeOutBack.transform(labelRaw);
      final labelT = leaving
          ? Curves.easeInCubic.transform(labelRaw)
          : Curves.easeOut.transform(labelRaw);

      // The body recedes a touch from its centre, with a little bounce.
      final contentT = leaving
          ? Curves.easeInCubic.transform(v)
          : Curves.easeOutBack.transform(v);

      final check = Opacity(
        opacity: _seg(v, 0.0, 0.22),
        child: Transform.rotate(
          angle: (1 - checkEase) * -0.8,
          child: Transform.scale(
            scale: 0.3 + 0.7 * checkScaleT,
            child: Icon(
              Icons.check_rounded,
              size: widget.iconSize,
              color: color,
            ),
          ),
        ),
      );

      final label = Opacity(
        opacity: labelT.clamp(0.0, 1.0),
        child: Transform.translate(
          // -1.5: lift the lowercase label so it centres on the check.
          offset: Offset((1 - labelBack) * -18.0, -1.5),
          child: Transform.scale(
            scale: 0.8 + 0.2 * labelBack,
            alignment: Alignment.centerLeft,
            child: Text(
              'скопировано',
              textHeightBehavior: const TextHeightBehavior(
                applyHeightToFirstAscent: false,
                applyHeightToLastDescent: false,
                leadingDistribution: TextLeadingDistribution.even,
              ),
              style: TextStyle(
                fontSize: widget.fontSize,
                height: 1,
                fontWeight: FontWeight.w600,
                color: color,
              ),
            ),
          ),
        ),
      );

      return Stack(
        fit: StackFit.passthrough,
        clipBehavior: Clip.none,
        children: [
          Opacity(
            opacity: 1 - (1 - widget.bodyOpacity) * reveal,
            child: Transform.scale(
              scale: 1 - 0.035 * contentT,
              alignment: Alignment.center,
              child: child,
            ),
          ),
          Positioned.fill(
            child: IgnorePointer(
              child: ExcludeSemantics(
                child: OverflowBox(
                  minWidth: 0,
                  maxWidth: double.infinity,
                  child: Transform.translate(
                    offset: Offset(0, (1 - reveal) * 7.0),
                    child: Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [check, const SizedBox(width: 7), label],
                    ),
                  ),
                ),
              ),
            ),
          ),
        ],
      );
    },
  );
}
