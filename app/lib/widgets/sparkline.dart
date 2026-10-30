import 'package:flutter/material.dart';

import '../core/models.dart';

/// Apple-Health-style smooth area chart. Ported from the SVG path generator
/// in `App.svelte#sparkPath`. Failed probes keep the last successful RTT on
/// the time axis, so the sparkline keeps moving without noisy outage markers.
class Sparkline extends StatelessWidget {
  final List<PingSample> samples;
  final Color color;
  final int windowSize;
  final double padding;

  const Sparkline({
    super.key,
    required this.samples,
    required this.color,
    this.windowSize = 60,
    this.padding = 3,
  });

  @override
  Widget build(BuildContext context) {
    return CustomPaint(
      painter: _SparklinePainter(
        samples: samples,
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
  final Color color;
  final int windowSize;
  final double padding;

  _SparklinePainter({
    required this.samples,
    required this.color,
    required this.windowSize,
    required this.padding,
  });

  @override
  void paint(Canvas canvas, Size size) {
    if (samples.isEmpty) return;

    final slice = samples.length > windowSize
        ? samples.sublist(samples.length - windowSize)
        : samples;
    var hasRtt = false;
    var lastRtt = 0;
    final before = samples.length - slice.length;
    for (var i = before - 1; i >= 0; i--) {
      final s = samples[i];
      if (s.ok) {
        hasRtt = true;
        lastRtt = s.rttMs;
        break;
      }
    }
    final rtts = slice.where((s) => s.ok).map((s) => s.rttMs).toList();
    if (!hasRtt && rtts.isEmpty) return;
    // Floor of 80ms so a fresh chart with no traffic doesn't look spiky.
    var maxRtt = rtts.fold<int>(80, (a, rtt) => rtt > a ? rtt : a);
    if (hasRtt && lastRtt > maxRtt) maxRtt = lastRtt;

    const strokeInset = 1.0;
    final chartW = size.width - strokeInset * 2;
    final stepW = chartW / (windowSize - 1);
    final off = strokeInset + (windowSize - slice.length) * stepW;
    final usableH = size.height - padding * 2;

    final pts = <_Pt>[];
    for (var i = 0; i < slice.length; i++) {
      final s = slice[i];
      if (s.ok) {
        hasRtt = true;
        lastRtt = s.rttMs;
      }
      if (!hasRtt) continue;
      final x = off + i * stepW;
      final y = (size.height - padding) - (lastRtt / maxRtt) * usableH;
      pts.add(_Pt(x, y));
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
      old.color != color ||
      old.windowSize != windowSize;
}

class _Pt {
  final double x;
  final double y;
  _Pt(this.x, this.y);
}
