import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:provider/provider.dart';
import 'package:nekkobox/core/models.dart';
import 'package:nekkobox/core/nekkobox_core.dart';
import 'package:nekkobox/state/app_state.dart';
import 'package:nekkobox/theme/app_theme.dart';
import 'package:nekkobox/widgets/settings_traffic.dart';
import 'package:nekkobox/widgets/brand_header.dart';

class _Core extends NekkoboxCore {
  @override
  String rawCall(String method, [String argsJson = '']) =>
      throw StateError('unexpected engine call: $method');
}

class _State extends AppState {
  _State() : super(_Core());
  int reads = 0;
  bool fail = false;
  TrafficCounters? counters = const TrafficCounters(
    uploadBytes: 1000,
    downloadBytes: 2000,
  );

  bool connected = true;
  @override
  bool get isRunning => connected;
  @override
  HealthInfo get health => HealthInfo(
    running: connected,
    tunnelOK: connected,
    ip: '192.0.2.1',
    status: connected ? 'ok' : 'off',
    message: '',
  );

  void disconnect() {
    connected = false;
    notifyListeners();
  }

  @override
  Future<TrafficCounters?> trafficCounters() async {
    reads++;
    if (fail) throw StateError('core stopped');
    if (!connected) return null;
    return counters;
  }
}

void main() {
  testWidgets('traffic row stays mounted and shows a dash when disconnected', (
    tester,
  ) async {
    final font = FontLoader('IBM Plex Mono')
      ..addFont(rootBundle.load('assets/fonts/IBMPlexMono-Medium.ttf'));
    await font.load();
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.binding.setSurfaceSize(const Size(409, 800));
    addTearDown(() => tester.binding.setSurfaceSize(null));
    final state = _State();
    addTearDown(state.dispose);
    await tester.pumpWidget(
      ChangeNotifierProvider<AppState>.value(
        value: state,
        child: MaterialApp(
          theme: buildAppTheme(),
          home: Scaffold(body: BrandHeader(onOpenLogs: () {})),
        ),
      ),
    );
    expect(find.byType(SettingsTraffic), findsNothing);
    await tester.tap(find.byIcon(Icons.settings_outlined));
    await tester.pumpAndSettle();
    expect(find.byType(SettingsTraffic), findsOneWidget);
    expect(tester.getSize(find.text('потребление')).height, lessThan(20));
    expect(
      tester.getTopLeft(find.text('потребление')).dx,
      tester.getTopLeft(find.text('статус')).dx,
    );
    expect(
      tester.getTopLeft(find.byType(SettingsTraffic)).dy,
      greaterThan(tester.getTopLeft(find.text('подключено')).dy),
    );
    expect(tester.takeException(), isNull);
    state.disconnect();
    await tester.pump(const Duration(seconds: 1));
    // The row stays; with nothing to measure the dash placeholder carries it.
    expect(find.byType(SettingsTraffic), findsOneWidget);
    expect(find.text('↓ - · ↑ - Мбит/с'), findsOneWidget);
    // Sampling continues while the sheet is open…
    final reads = state.reads;
    await tester.pump(const Duration(seconds: 3));
    expect(state.reads, greaterThan(reads));
    // …and stops once the sheet is gone.
    await tester.pumpWidget(const SizedBox());
    final closedReads = state.reads;
    await tester.pump(const Duration(seconds: 3));
    expect(state.reads, closedReads);
  });

  testWidgets('settings counters stop when hidden and reset after errors', (
    tester,
  ) async {
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    final state = _State();
    addTearDown(state.dispose);
    await tester.pumpWidget(
      ChangeNotifierProvider<AppState>.value(
        value: state,
        child: MaterialApp(
          theme: buildAppTheme(),
          home: const Scaffold(body: SettingsTraffic()),
        ),
      ),
    );
    await tester.pump();
    expect(state.reads, 1);
    expect(find.text('↓ - · ↑ - Мбит/с'), findsOneWidget);
    await tester.pump(const Duration(seconds: 1));
    expect(find.text('↓ 0,0 · ↑ 0,0 Мбит/с'), findsOneWidget);
    state.fail = true;
    await tester.pump(const Duration(seconds: 1));
    expect(find.text('↓ - · ↑ - Мбит/с'), findsOneWidget);
    state.fail = false;
    await tester.pump(const Duration(seconds: 1));
    expect(find.text('↓ - · ↑ - Мбит/с'), findsOneWidget);
    await tester.pump(const Duration(seconds: 1));
    expect(find.text('↓ 0,0 · ↑ 0,0 Мбит/с'), findsOneWidget);

    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    final pausedReads = state.reads;
    await tester.pump(const Duration(seconds: 5));
    expect(state.reads, pausedReads);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pump();
    expect(find.text('↓ - · ↑ - Мбит/с'), findsOneWidget);

    await tester.pumpWidget(const SizedBox());
    final closedReads = state.reads;
    await tester.pump(const Duration(seconds: 5));
    expect(state.reads, closedReads);
    expect(tester.takeException(), isNull);
  });
}
