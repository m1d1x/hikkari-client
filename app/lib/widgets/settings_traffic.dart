import 'dart:async';

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../core/models.dart';
import '../state/app_state.dart';
import '../theme/tokens.dart';

/// A local counter reader, mounted only in the settings sheet while connected.
/// No sampling survives closing the sheet or putting the app in the background.
class SettingsTraffic extends StatefulWidget {
  const SettingsTraffic({super.key});

  @override
  State<SettingsTraffic> createState() => _SettingsTrafficState();
}

class _SettingsTrafficState extends State<SettingsTraffic>
    with WidgetsBindingObserver {
  Timer? _timer;
  final _clock = Stopwatch()..start();
  TrafficCounters? _previous;
  int _previousAt = 0;
  String? _speed;
  bool _supported = true;
  bool _reading = false;
  bool _foreground = true;
  int _generation = 0;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    final lifecycle = WidgetsBinding.instance.lifecycleState;
    _foreground = lifecycle == null || lifecycle == AppLifecycleState.resumed;
    if (_foreground) _start();
  }

  void _start() {
    _previous = null;
    _read();
    _timer = Timer.periodic(const Duration(seconds: 1), (_) => _read());
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    final foreground = state == AppLifecycleState.resumed;
    if (foreground == _foreground) return;
    _foreground = foreground;
    _generation++;
    _timer?.cancel();
    _previous = null;
    setState(() => _speed = null);
    if (_foreground) _start();
  }

  Future<void> _read() async {
    if (_reading || !mounted || !_foreground) return;
    _reading = true;
    final generation = _generation;
    try {
      final next = await context.read<AppState>().trafficCounters();
      if (!mounted || generation != _generation) return;
      final at = _clock.elapsedMicroseconds;
      final previous = _previous;
      String? speed;
      if (next != null &&
          previous != null &&
          at > _previousAt &&
          next.uploadBytes >= previous.uploadBytes &&
          next.downloadBytes >= previous.downloadBytes) {
        // bytes / microseconds * 8 = decimal megabits / second.
        final seconds = at - _previousAt;
        final down =
            (next.downloadBytes - previous.downloadBytes) * 8 / seconds;
        final up = (next.uploadBytes - previous.uploadBytes) * 8 / seconds;
        String format(double value) =>
            value.toStringAsFixed(1).replaceAll('.', ',');
        speed = '↓ ${format(down)} · ↑ ${format(up)} Мбит/с';
      }
      _previous = next;
      _previousAt = at;
      setState(() {
        _supported = next != null;
        _speed = speed;
      });
    } catch (_) {
      if (mounted && generation == _generation) {
        _previous = null;
        setState(() => _speed = null);
      }
    } finally {
      _reading = false;
    }
  }

  @override
  void dispose() {
    _generation++;
    _timer?.cancel();
    _clock.stop();
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    if (!_supported) return const SizedBox.shrink();
    final t = context.tokens;
    return Padding(
      padding: const EdgeInsets.only(left: 88, top: 3),
      child: Tooltip(
        message:
            'Текущий трафик через клиент, включая прямые соединения по правилам. '
            'Это не замер пропускной способности.',
        child: Text(
          _speed ?? '↓ — · ↑ — Мбит/с',
          style: TextStyle(
            color: t.textMuted,
            fontFamily: t.monoFamily,
            fontSize: 11,
            fontFeatures: const [FontFeature.tabularFigures()],
          ),
        ),
      ),
    );
  }
}
