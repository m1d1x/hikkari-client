// Measures the routing reveal in a real macOS window, in profile mode:
//   flutter drive --profile -d macos \
//     --driver=test_driver/integration_test.dart \
//     --target=integration_test/route_reveal_motion_test.dart \
//     --dart-define=ROUTE_CATALOG=$PWD/../engine/core/presets/defaults.json
// The core is a fake serving the bundled preset catalog, so no engine or
// service is touched. Every frame records where the routing header is drawn.
import 'dart:convert';
import 'dart:io';
import 'dart:ui';

import 'package:flutter/material.dart';
import 'package:flutter/scheduler.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:nekkobox/core/nekkobox_core.dart';
import 'package:nekkobox/screens/home_screen.dart';
import 'package:nekkobox/state/app_state.dart';
import 'package:nekkobox/theme/app_theme.dart';
import 'package:provider/provider.dart';
import 'package:window_manager/window_manager.dart';

class _CatalogCore extends NekkoboxCore {
  _CatalogCore(this.catalog);
  final Map<String, dynamic> catalog;

  @override
  String rawCall(String method, [String argsJson = '']) {
    if (method == 'ListApps') return jsonEncode({'ok': true, 'data': []});
    return jsonEncode({
      'ok': true,
      'data': {
        'syncEnabled': true,
        'configured': false,
        'connected': false,
        'source': 'bundled',
        'platform': 'darwin',
        'lastChecked': '',
        'error': '',
        'choices': <String, dynamic>{},
        'catalog': catalog,
      },
    });
  }
}

void main() {
  final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  binding.framePolicy = LiveTestWidgetsFlutterBindingFramePolicy.fullyLive;

  testWidgets('routing reveal motion', (tester) async {
    // flutter drive runs the app from the build folder: the catalog path
    // comes in as a define, relative paths are tried as a fallback.
    const defined = String.fromEnvironment('ROUTE_CATALOG');
    final path = defined.isNotEmpty
        ? defined
        : '${Directory.current.path}/../engine/core/presets/defaults.json';
    final catalog =
        jsonDecode(File(path).readAsStringSync()) as Map<String, dynamic>;
    final state = AppState(_CatalogCore(catalog));
    await state.refreshPresetState();
    await windowManager.ensureInitialized();

    await tester.pumpWidget(
      ChangeNotifierProvider.value(
        value: state,
        child: MaterialApp(theme: buildAppTheme(), home: const HomeScreen()),
      ),
    );
    final header = find.text('маршрутизация');
    final display = PlatformDispatcher.instance.displays.first;
    final screenHeight = display.size.height / display.devicePixelRatio;

    var recording = false;
    var frameStart = Duration.zero;
    final samples = <(Duration, double)>[];
    final timings = <FrameTiming>[];
    SchedulerBinding.instance.addPersistentFrameCallback((stamp) {
      if (!recording) return;
      final box = tester.renderObject(header) as RenderBox;
      samples.add((stamp - frameStart, box.localToGlobal(Offset.zero).dy));
    });
    SchedulerBinding.instance.addTimingsCallback((list) {
      if (recording) timings.addAll(list);
    });

    Future<void> wait(int ms) =>
        Future<void>.delayed(Duration(milliseconds: ms));

    final mouse = await tester.createGesture(kind: PointerDeviceKind.mouse);
    addTearDown(mouse.removePointer);

    final report = StringBuffer();
    for (final height in [700.0, screenHeight - 40]) {
      await windowManager.setSize(Size(480, height));
      await wait(800);
      for (final step in ['open', 'close', 'open', 'close']) {
        final at = tester.getCenter(header);
        await mouse.moveTo(at);
        await wait(300);
        samples.clear();
        timings.clear();
        frameStart = SchedulerBinding.instance.currentSystemFrameTimeStamp;
        recording = true;
        final tapAt = DateTime.now();
        await mouse.down(at);
        await mouse.up();
        await wait(1200);
        recording = false;
        await wait(200);
        final tapUs = tapAt.microsecondsSinceEpoch;
        final moves = <String>[];
        double? previous;
        Duration? previousAt;
        var maxStep = 0.0;
        var maxGapMs = 0.0;
        Duration? firstMove, lastMove;
        for (final (at, y) in samples) {
          if (previous != null) {
            final step = (y - previous).abs();
            if (step > 0.05) {
              firstMove ??= previousAt;
              lastMove = at;
              final gap = (at - previousAt!).inMicroseconds / 1000;
              if (gap > maxGapMs) maxGapMs = gap;
              if (step > maxStep) maxStep = step;
              moves.add(
                '${(y - previous).toStringAsFixed(0)}@${gap.toStringAsFixed(0)}',
              );
            }
          }
          previous = y;
          previousAt = at;
        }
        double ms(Duration d) => d.inMicroseconds / 1000;
        final build = timings.isEmpty
            ? 0
            : timings
                  .map((t) => ms(t.buildDuration))
                  .reduce((a, b) => a > b ? a : b);
        final raster = timings.isEmpty
            ? 0
            : timings
                  .map((t) => ms(t.rasterDuration))
                  .reduce((a, b) => a > b ? a : b);
        report.writeln(
          'H=${height.round()} $step: frames=${samples.length} '
          'moving=${firstMove == null ? 0 : ms(lastMove! - firstMove).round()}ms '
          'maxStep=${maxStep.toStringAsFixed(0)}px maxGap=${maxGapMs.toStringAsFixed(0)}ms '
          'worstBuild=${build.toStringAsFixed(1)}ms worstRaster=${raster.toStringAsFixed(1)}ms '
          'tap=$tapUs',
        );
        report.writeln('  ${moves.join(' ')}');
        await wait(400);
      }
    }
    // ignore: avoid_print
    print('ROUTE_MOTION_REPORT\n$report');
    binding.reportData = {'report': report.toString()};
  });
}
