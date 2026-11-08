import 'dart:math' as math;

import 'package:flutter/material.dart';

import '../core/models.dart';

/// Shared smooth line for latency and throughput. Missing and failed
/// measurements leave gaps; a single real measurement is drawn as one dot.
///
/// With [placeholderSeed] set, a profile with fewer than two measurements
/// shows a faint placeholder wave instead of an empty slot: not data, only a
/// shape, the same for the same seed. Real measurements replace it as soon
/// as there are two.
class Sparkline extends StatelessWidget {
  final List<PingSample> samples;
  final List<double?>? values;
  final Color color;
  final int windowSize;
  final double padding;
  final String? placeholderSeed;

  const Sparkline({
    super.key,
    this.samples = const [],
    this.values,
    required this.color,
    this.windowSize = 60,
    this.padding = 3,
    this.placeholderSeed,
  }) : assert(windowSize > 1);

  @override
  Widget build(BuildContext context) {
    return CustomPaint(
      painter: _SparklinePainter(
        samples: samples,
        values: values,
        color: color,
        windowSize: windowSize,
        padding: padding,
        placeholderSeed: placeholderSeed,
      ),
      size: Size.infinite,
    );
  }
}

class _SparklinePainter extends CustomPainter {
  final List<PingSample> samples;
  final List<double?>? values;
  final Color color;
  final int windowSize;
  final double padding;
  final String? placeholderSeed;

  _SparklinePainter({
    required this.samples,
    required this.values,
    required this.color,
    required this.windowSize,
    required this.padding,
    required this.placeholderSeed,
  });

  static const _placeholderAlpha = 0.35;

  // A calm wave between 35% and 75% of the height, its phases taken from
  // the seed so each profile keeps its own shape.
  static List<double?> _placeholder(String seed, int n) {
    final rnd = math.Random(seed.codeUnits.fold<int>(17, (h, c) => h * 31 + c));
    final p1 = rnd.nextDouble() * math.pi * 2;
    final p2 = rnd.nextDouble() * math.pi * 2;
    final p3 = rnd.nextDouble() * math.pi * 2;
    return [
      for (var i = 0; i < n; i++)
        0.55 +
            0.11 * math.sin(i * 0.31 + p1) +
            0.06 * math.sin(i * 0.83 + p2) +
            0.03 * math.sin(i * 1.9 + p3),
    ];
  }

  @override
  void paint(Canvas canvas, Size size) {
    var data =
        values ?? samples.map((s) => s.ok ? s.rttMs.toDouble() : null).toList();
    var normalized = values != null;
    var color = this.color;
    final seed = placeholderSeed;
    if (seed != null &&
        data.whereType<double>().where((v) => v.isFinite && v > 0).length < 2) {
      data = _placeholder(seed, windowSize);
      normalized = true;
      color = color.withValues(alpha: color.a * _placeholderAlpha);
    }
    if (data.isEmpty || size.width < 2 || size.height <= padding * 2) return;
    final slice = data.length > windowSize
        ? data.sublist(data.length - windowSize)
        : data;
    final valid = slice
        .whereType<double>()
        .where((v) => v.isFinite && v > 0)
        .toList();
    if (valid.isEmpty) return;
    final maxValue = valid.fold<double>(
      normalized ? 1 : 80,
      (a, b) => a > b ? a : b,
    );
    const strokeInset = 1.0;
    final stepW = (size.width - strokeInset * 2) / (windowSize - 1);
    final off = strokeInset + (windowSize - slice.length) * stepW;
    final usableH = size.height - padding * 2;
    final pts = <_Pt>[];
    for (var i = 0; i < slice.length; i++) {
      final value = slice[i];
      if (value == null || !value.isFinite || value <= 0) {
        _draw(canvas, pts, color);
        pts.clear();
        continue;
      }
      pts.add(
        _Pt(
          off + i * stepW,
          (size.height - padding) - (value / maxValue) * usableH,
        ),
      );
    }
    _draw(canvas, pts, color);
  }

  void _draw(Canvas canvas, List<_Pt> pts, Color color) {
    if (pts.isEmpty) return;
    if (pts.length == 1) {
      canvas.drawCircle(
        Offset(pts.first.x, pts.first.y),
        1.4,
        Paint()..color = color,
      );
      return;
    }
    // Smooth path via quadratic midpoint curves — same trick as the
    // Svelte version (`Q p0 -> mid -> T p1`).
    final linePath = Path()..moveTo(pts.first.x, pts.first.y);
    for (var i = 1; i < pts.length; i++) {
      final p0 = pts[i - 1];
      final p1 = pts[i];
      final mx = (p0.x + p1.x) / 2;
      final my = (p0.y + p1.y) / 2;
      linePath.quadraticBezierTo(p0.x, p0.y, mx, my);
      // The "T" smooth-shorthand reflects the previous control point;
      // emulated here with a second quadratic that runs through p1.
      linePath.quadraticBezierTo(p1.x, p1.y, p1.x, p1.y);
    }

    final linePaint = Paint()
      ..color = color
      ..style = PaintingStyle.stroke
      ..strokeWidth = 1.4
      ..strokeCap = StrokeCap.round
      ..strokeJoin = StrokeJoin.round;
    canvas.drawPath(linePath, linePaint);
  }

  @override
  bool shouldRepaint(_SparklinePainter old) =>
      old.samples != samples ||
      old.values != values ||
      old.color != color ||
      old.windowSize != windowSize ||
      old.placeholderSeed != placeholderSeed;
}

class _Pt {
  final double x;
  final double y;
  _Pt(this.x, this.y);
}
