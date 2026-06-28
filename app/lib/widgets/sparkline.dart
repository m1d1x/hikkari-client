import 'package:flutter/material.dart';

import '../core/models.dart';

/// Apple-Health-style smooth area chart. Ported from the SVG path generator
/// in `App.svelte#sparkPath`. We draw at most the last [windowSize] samples;
/// a missing sample (`!ok`) shows as a red dot pinned to the baseline so
/// outages stay legible even on a thin row.
class Sparkline extends StatelessWidget {
  final List<PingSample> samples;
  final Color color;
  final Color outageColor;
  final int windowSize;
  final double padding;

  const Sparkline({
    super.key,
    required this.samples,
    required this.color,
    this.outageColor = const Color(0xFFFF453A),
    this.windowSize = 60,
    this.padding = 3,
  });

  @override
  Widget build(BuildContext context) {
    return CustomPaint(
      painter: _SparklinePainter(
        samples: samples,
        color: color,
        outageColor: outageColor,
        windowSize: windowSize,
        padding: padding,
      ),
      size: Size.infinite,
    );
  }
}

class _SparklinePainter extends CustomPainter {
  final List<PingSample> samples;
  final Color color;
  final Color outageColor;
  final int windowSize;
  final double padding;

  _SparklinePainter({
    required this.samples,
    required this.color,
    required this.outageColor,
    required this.windowSize,
    required this.padding,
  });

  @override
  void paint(Canvas canvas, Size size) {
    if (samples.isEmpty) return;

    final slice = samples.length > windowSize
        ? samples.sublist(samples.length - windowSize)
        : samples;
    final rtts = slice.where((s) => s.ok).map((s) => s.rttMs).toList();
    // Floor of 80ms so a fresh chart with no traffic doesn't look spiky.
    final maxRtt = rtts.fold<int>(80, (a, b) => b > a ? b : a);

    final stepW = size.width / (windowSize - 1);
    final off = (windowSize - slice.length) * stepW;
    final usableH = size.height - padding * 2;

    final pts = <_Pt>[];
    for (var i = 0; i < slice.length; i++) {
      final s = slice[i];
      final x = off + i * stepW;
      final y = s.ok
          ? (size.height - padding) - (s.rttMs / maxRtt) * usableH
          : size.height - 2;
      pts.add(_Pt(x, y, s.ok));
    }
    if (pts.isEmpty) return;

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

    // Area = stroke path, then drop to baseline and close.
    final areaPath = Path.from(linePath)
      ..lineTo(pts.last.x, size.height)
      ..lineTo(pts.first.x, size.height)
      ..close();

    final areaPaint = Paint()
      ..shader = LinearGradient(
        begin: Alignment.topCenter,
        end: Alignment.bottomCenter,
        colors: [
          color.withValues(alpha: 0.45),
          color.withValues(alpha: 0.0),
        ],
      ).createShader(Rect.fromLTWH(0, 0, size.width, size.height));
    canvas.drawPath(areaPath, areaPaint);

    final linePaint = Paint()
      ..color = color
      ..style = PaintingStyle.stroke
      ..strokeWidth = 1.4
      ..strokeCap = StrokeCap.round
      ..strokeJoin = StrokeJoin.round;
    canvas.drawPath(linePath, linePaint);

    // Outage dots — red marker on the baseline for each failed probe.
    final dotPaint = Paint()..color = outageColor;
    for (final p in pts) {
      if (!p.ok) {
        canvas.drawCircle(Offset(p.x, size.height - 4), 1.6, dotPaint);
      }
    }
  }

  @override
  bool shouldRepaint(_SparklinePainter old) =>
      old.samples != samples ||
      old.color != color ||
      old.windowSize != windowSize;
}

class _Pt {
  final double x;
  final double y;
  final bool ok;
  _Pt(this.x, this.y, this.ok);
}
