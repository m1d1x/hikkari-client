import 'dart:math' as math;
import 'dart:ui' show lerpDouble;

import 'package:flutter/material.dart';

import '../theme/tokens.dart';
import 'power_mark_geometry.dart';

/// What the power button is showing. Derived from the tunnel state by the
/// owner; the button only animates between these.
enum PowerRingPhase { off, connecting, on, disconnecting }

/// The power button's ring and mark.
///
/// The mark is the power symbol while off and the Hikkari eye while on, and
/// one turns into the other:
///
/// - press: the power arc opens into the two eyelids and the bar drops into
///   the pupil, which looks from side to side while the start is in flight;
/// - connected: the pupil settles, the four rays of the X grow out from the
///   eye and the mark and ring take the accent;
/// - disconnect: the rays draw back in, then the eye folds back into the
///   power symbol.
///
/// The eye is the logo's own outlines (`power_mark_geometry.dart`, generated
/// from `brand/logo.svg`): the main blade (upper lid with the lower-left and
/// upper-right rays), the lower lid, the pupil and the two other rays.
class PowerRing extends StatefulWidget {
  const PowerRing({
    super.key,
    required this.phase,
    required this.size,
    required this.accent,
    required this.idleBorder,
    required this.idleIcon,
    required this.hover,
    required this.child,
  });

  final PowerRingPhase phase;
  final double size;
  final Color accent;
  final Color idleBorder;
  final Color idleIcon;
  final bool hover;

  /// The tappable button, laid over the ring and mark.
  final Widget child;

  @override
  State<PowerRing> createState() => _PowerRingState();
}

class _PowerRingState extends State<PowerRing> with TickerProviderStateMixin {
  // 0 = power symbol, 1 = eye.
  late final AnimationController _morph = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 620),
  );
  // 0 = no rays, 1 = full X.
  late final AnimationController _rays = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 720),
  );
  // Pupil sweep while a press is in flight, and how far it sweeps.
  late final AnimationController _look = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 820),
  );
  late final AnimationController _lookAmount = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 360),
  );
  // Pointer over the button, on the shared hover timing.
  late final AnimationController _hover = AnimationController(
    vsync: this,
    duration: AppDurations.hover,
  );
  // 0 = off colours, 1 = accent.
  late final AnimationController _level = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 420),
  );

  @override
  void initState() {
    super.initState();
    _hover.value = widget.hover ? 1 : 0;
    _apply(null, widget.phase);
  }

  @override
  void didUpdateWidget(PowerRing old) {
    super.didUpdateWidget(old);
    if (old.phase != widget.phase) _apply(old.phase, widget.phase);
    if (old.hover != widget.hover) {
      _hover.animateTo(widget.hover ? 1 : 0, curve: AppCurves.hover);
    }
  }

  void _searching(bool on) {
    if (on) {
      if (!_look.isAnimating) _look.repeat(reverse: true);
      _lookAmount.forward();
    } else {
      _lookAmount.reverse().whenComplete(() {
        if (mounted && _lookAmount.isDismissed) _look.stop();
      });
    }
  }

  void _apply(PowerRingPhase? from, PowerRingPhase to) {
    if (from == null) {
      final on = to == PowerRingPhase.on;
      _morph.value = on || to == PowerRingPhase.connecting ? 1 : 0;
      _rays.value = on ? 1 : 0;
      _level.value = on ? 1 : 0;
      _searching(to == PowerRingPhase.connecting);
      return;
    }
    switch (to) {
      case PowerRingPhase.connecting:
        _morph.animateTo(1, curve: Curves.easeInOutCubic);
        _rays.animateTo(0, curve: Curves.easeInCubic);
        _searching(true);
      case PowerRingPhase.on:
        _searching(false);
        _morph.animateTo(1, curve: Curves.easeInOutCubic);
        _rays.animateTo(1); // eased in the painter
        _level.animateTo(1, curve: Curves.easeOutCubic);
      case PowerRingPhase.disconnecting:
        _rays.animateTo(0, curve: Curves.easeInCubic);
        _level.animateTo(0.4, curve: Curves.easeOutCubic);
        _searching(true);
      case PowerRingPhase.off:
        _searching(false);
        _level.animateTo(0, curve: Curves.easeOutCubic);
        // The rays go first, then the eye folds back into the symbol.
        _rays.animateTo(0, curve: Curves.easeInCubic).whenComplete(() {
          if (mounted && widget.phase == PowerRingPhase.off) {
            _morph.animateTo(0, curve: Curves.easeInOutCubic);
          }
        });
    }
  }

  @override
  void dispose() {
    _morph.dispose();
    _rays.dispose();
    _look.dispose();
    _lookAmount.dispose();
    _level.dispose();
    _hover.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: widget.size,
      height: widget.size,
      child: AnimatedBuilder(
        animation: Listenable.merge([
          _morph,
          _rays,
          _look,
          _lookAmount,
          _level,
          _hover,
        ]),
        builder: (context, child) => CustomPaint(
          painter: _MarkPainter(
            morph: _morph.value,
            rays: _rays.value,
            look:
                (Curves.easeInOut.transform(_look.value) * 2 - 1) *
                Curves.easeOut.transform(_lookAmount.value),
            level: _level.value,
            hover: _hover.value,
            accent: widget.accent,
            idleBorder: widget.idleBorder,
            idleIcon: widget.idleIcon,
          ),
          child: child,
        ),
        child: widget.child,
      ),
    );
  }
}

/// The power symbol and the eye, in mark units (0.3 of the ring's radius)
/// with the pupil at the origin.
abstract final class _Mark {
  static const samples = kMarkSamples;

  /// Power symbol: Material's power_settings_new on its 24-unit grid, drawn
  /// at 68 px (the size the button's icon had) — arc of radius 8 and width 2
  /// round (12, 12), open ±40.5° about the top, the bar from y 3 to 13, all
  /// ends cut flat. In mark units one grid unit is 68 / 24 px over the
  /// painter's unit of 0.3 · 88 px.
  static const _grid = (68 / 24) / (0.3 * 88);
  static const arcRadius = 8 * _grid;
  static const arcGapDeg = 40.5;
  static const barTop = Offset(0, -9 * _grid);
  static const barBottom = Offset(0, 1 * _grid);
  static const stroke = 2 * _grid;

  /// Right half of the power arc, from the gap down to the bottom: becomes
  /// the lower lid from its right end round to its left end.
  static final List<Offset> arcRight = _arc(-90 + arcGapDeg, 90);

  /// Left half, from the bottom up to the gap: becomes the upper lid from its
  /// left end over to its right end.
  static final List<Offset> arcLeft = _arc(90, 270 - arcGapDeg);

  /// The logo's outlines run from tip to tip; these run the other way, the
  /// way the arc halves do. Reversing a run swaps its sides.
  static final _Blade lowerLid = _Blade(
    kMarkLowerLidRight,
    kMarkLowerLidLeft,
  ).reversed;
  static final _Blade blade = _Blade(kMarkBladeLeft, kMarkBladeRight).reversed;
  static const lidFrom = 1 - kMarkLidTo;
  static const lidTo = 1 - kMarkLidFrom;
  static final _Blade rayUpperLeft = _Blade(
    kMarkRayUpperLeftLeft,
    kMarkRayUpperLeftRight,
  );
  static final _Blade rayLowerRight = _Blade(
    kMarkRayLowerRightLeft,
    kMarkRayLowerRightRight,
  );

  /// The direction the eye points, for the pupil's sweep.
  static final Offset axis = () {
    final d = blade.centre(lidTo) - blade.centre(lidFrom);
    return d / d.distance;
  }();
  static const lookReach = 0.28;

  static List<Offset> _arc(double fromDeg, double toDeg) => [
    for (var i = 0; i < samples; i++)
      () {
        final a =
            (fromDeg + (toDeg - fromDeg) * i / (samples - 1)) * math.pi / 180;
        return Offset(math.cos(a), math.sin(a)) * arcRadius;
      }(),
  ];
}

/// An elongated outline as two sides running from one tip to the other.
/// `left` lies along +normal of the run, normal = (-dy, dx).
class _Blade {
  _Blade(List<double> left, List<double> right)
    : left = _points(left),
      right = _points(right);

  _Blade._(this.left, this.right);

  final List<Offset> left;
  final List<Offset> right;

  _Blade get reversed =>
      _Blade._(right.reversed.toList(), left.reversed.toList());

  static List<Offset> _points(List<double> flat) => [
    for (var i = 0; i < flat.length; i += 2) Offset(flat[i], flat[i + 1]),
  ];

  static Offset _at(List<Offset> side, double u) {
    final f = u.clamp(0.0, 1.0) * (side.length - 1);
    final i = f.floor().clamp(0, side.length - 2);
    return Offset.lerp(side[i], side[i + 1], f - i)!;
  }

  static const _pointShare = 0.2;
  static double _point(double k) =>
      math.sin(math.min(1.0, k / _pointShare) * math.pi / 2);

  Offset centre(double u) => (_at(left, u) + _at(right, u)) / 2;

  /// [n] facing pairs over the share [from]..[to] of the length. Ends cut
  /// short of a tip are narrowed by [cutTaper] (0 = to a point, 1 = blunt).
  (List<Offset>, List<Offset>) span(
    double from,
    double to,
    int n, {
    double cutTaper = 1,
    bool cutStart = false,
    bool cutEnd = false,
  }) {
    final l = <Offset>[];
    final r = <Offset>[];
    for (var i = 0; i < n; i++) {
      final k = i / (n - 1);
      final u = from + (to - from) * k;
      final a = _at(left, u);
      final b = _at(right, u);
      // A cut end closes to a point along a quarter-sine, the way the lids'
      // own tips do.
      var narrow = 1.0;
      if (cutStart) narrow = math.min(narrow, _point(k));
      if (cutEnd) narrow = math.min(narrow, _point(1 - k));
      narrow = lerpDouble(narrow, 1, cutTaper)!;
      final c = (a + b) / 2;
      l.add(c + (a - c) * narrow);
      r.add(c + (b - c) * narrow);
    }
    return (l, r);
  }
}

class _MarkPainter extends CustomPainter {
  _MarkPainter({
    required this.morph,
    required this.rays,
    required this.look,
    required this.level,
    required this.hover,
    required this.accent,
    required this.idleBorder,
    required this.idleIcon,
  });

  final double morph;
  final double rays;
  final double look;
  final double level;
  final double hover;
  final Color accent;
  final Color idleBorder;
  final Color idleIcon;

  @override
  void paint(Canvas canvas, Size size) {
    final c = size.center(Offset.zero);
    final r = size.shortestSide / 2;

    // Ring.
    final idleRing = Color.lerp(
      idleBorder,
      accent.withValues(alpha: 0.4),
      hover,
    )!;
    final litRing = accent.withValues(alpha: lerpDouble(0.6, 0.85, hover)!);
    final ringW = 1.5 + 1.5 * level;
    canvas.drawCircle(
      c,
      r - ringW / 2,
      Paint()
        ..style = PaintingStyle.stroke
        ..strokeWidth = ringW
        ..color = Color.lerp(idleRing, litRing, level)!,
    );

    // Mark. One unit is sized so the rays' tips stay inside the ring.
    final unit = r * 0.3;
    final idleMark = Color.lerp(
      idleIcon,
      accent.withValues(alpha: 0.7),
      hover,
    )!;
    final colour = Color.lerp(idleMark, accent, level)!;
    // The mark's parts overlap (arc halves, round ends, the bar's caps). They
    // are drawn opaque into one layer and the layer takes the colour's alpha,
    // so overlaps don't show as darker spots.
    final fill = Paint()..color = colour.withValues(alpha: 1);
    canvas.save();
    canvas.translate(c.dx, c.dy);
    canvas.scale(unit);
    canvas.saveLayer(
      Rect.fromCircle(center: Offset.zero, radius: 3.2),
      Paint()..color = Color.fromRGBO(0, 0, 0, colour.a),
    );

    // All four rays open together: the two separate rays and the blade
    // growing out to its two corners.
    final grow = _rayCurve.transform(rays);

    final n = _Mark.samples;
    _morphStroke(canvas, fill, _Mark.arcRight, _Mark.lowerLid.span(0, 1, n));
    _morphStroke(
      canvas,
      fill,
      _Mark.arcLeft,
      _Mark.blade.span(
        lerpDouble(_Mark.lidFrom, 0, grow)!,
        lerpDouble(_Mark.lidTo, 1, grow)!,
        n,
        cutTaper: grow,
        cutStart: true,
        cutEnd: true,
      ),
    );
    for (final ray in [_Mark.rayUpperLeft, _Mark.rayLowerRight]) {
      if (grow <= 0) break;
      final (l, rr) = ray.span(0, grow, n, cutTaper: grow, cutEnd: true);
      _fill(canvas, fill, l, rr);
    }
    _pupil(canvas, fill);

    canvas.restore(); // layer
    canvas.restore();
  }

  // A soft start and a long settle.
  static const _rayCurve = Cubic(0.3, 0, 0.15, 1);

  // A stroke that is half of the power arc at morph 0 and a part of the eye
  // at 1: even width with flat ends turning into the logo's outline.
  void _morphStroke(
    Canvas canvas,
    Paint fill,
    List<Offset> arc,
    (List<Offset>, List<Offset>) target,
  ) {
    final m = morph;
    final n = arc.length;
    final (tl, tr) = target;
    final left = <Offset>[];
    final right = <Offset>[];
    for (var i = 0; i < n; i++) {
      final prev = arc[i == 0 ? 0 : i - 1];
      final next = arc[i == n - 1 ? n - 1 : i + 1];
      final d = next - prev;
      final normal = Offset(-d.dy, d.dx) / d.distance;
      final w = _Mark.stroke / 2;
      left.add(Offset.lerp(arc[i] + normal * w, tl[i], m)!);
      right.add(Offset.lerp(arc[i] - normal * w, tr[i], m)!);
    }
    _fill(canvas, fill, left, right);
  }

  void _fill(Canvas canvas, Paint fill, List<Offset> left, List<Offset> right) {
    final path = Path()..moveTo(left.first.dx, left.first.dy);
    for (final p in left.skip(1)) {
      path.lineTo(p.dx, p.dy);
    }
    for (final p in right.reversed) {
      path.lineTo(p.dx, p.dy);
    }
    path.close();
    canvas.drawPath(path, fill);
  }

  // The bar drops into the pupil: its ends meet while it thickens into a dot,
  // and its flat ends round off on the way.
  void _pupil(Canvas canvas, Paint fill) {
    final m = Curves.easeInOut.transform(morph);
    final centre = _Mark.axis * (_Mark.lookReach * look);
    final top = Offset.lerp(_Mark.barTop, centre, m)!;
    final bottom = Offset.lerp(_Mark.barBottom, centre, m)!;
    final w = lerpDouble(_Mark.stroke, kMarkPupilRadius * 2, m)!;
    canvas.drawLine(
      top,
      bottom,
      Paint()
        ..color = fill.color
        ..strokeWidth = w,
    );
    final cap = w / 2 * m;
    if (cap > 0) {
      canvas.drawCircle(top, cap, fill);
      canvas.drawCircle(bottom, cap, fill);
    }
  }

  @override
  bool shouldRepaint(_MarkPainter o) =>
      o.morph != morph ||
      o.rays != rays ||
      o.look != look ||
      o.level != level ||
      o.hover != hover ||
      o.accent != accent ||
      o.idleBorder != idleBorder ||
      o.idleIcon != idleIcon;
}
