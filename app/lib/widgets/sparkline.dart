import 'package:flutter/material.dart';

import '../core/models.dart';

/// Shared smooth line for latency and throughput. Missing and failed
/// measurements leave gaps; a single real measurement is drawn as one dot.
class Sparkline extends StatelessWidget {
  final List<PingSample> samples;
  final List<double?>? values;
  final Color color;
  final int windowSize;
  final double padding;

  const Sparkline({
    super.key,
    this.samples = const [],
    this.values,
    required this.color,
    this.windowSize = 60,
    this.padding = 3,
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

  _SparklinePainter({
    required this.samples,
    required this.values,
    required this.color,
    required this.windowSize,
    required this.padding,
  });

  @override
  void paint(Canvas canvas, Size size) {
    final data =
        values ?? samples.map((s) => s.ok ? s.rttMs.toDouble() : null).toList();
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
      values == null ? 80 : 1,
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
        _draw(canvas, pts);
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
    _draw(canvas, pts);
  }

  void _draw(Canvas canvas, List<_Pt> pts) {
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
      old.windowSize != windowSize;
}

class _Pt {
  final double x;
  final double y;
  _Pt(this.x, this.y);
}
