import 'dart:ui' show PointerDeviceKind;
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:nekkobox/core/models.dart';
import 'package:nekkobox/theme/app_theme.dart';
import 'package:nekkobox/widgets/profile_summary_card.dart';
import 'package:nekkobox/widgets/sparkline.dart';

void main() {
  RelayCapacity metric({bool stale = false}) {
    final now = DateTime.now().millisecondsSinceEpoch ~/ 1000;
    return RelayCapacity(
      mbps: 123,
      limited: true,
      rttMs: 87,
      points: 2,
      measuredAt: now - 60,
      expiresAt: stale ? now - 1 : now + 5300,
      history: [RelayMetricPoint(ts: now ~/ 1800 * 1800, rttMs: 87, mbps: 123)],
    );
  }

  Future<void> mount(
    WidgetTester tester, {
    bool stale = false,
    double width = 340,
  }) async {
    await tester.pumpWidget(
      MaterialApp(
        theme: buildAppTheme(),
        home: Scaffold(
          body: Center(
            child: SizedBox(
              width: width,
              child: ProfileSummaryCard(
                title: 'длинный профиль подключения',
                samples: const [],
                selected: true,
                capacity: metric(stale: stale),
                useProbeMetrics: true,
                onTap: () {},
              ),
            ),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
  }

  testWidgets('hover swaps the number and chart in the same row', (
    tester,
  ) async {
    await mount(tester);
    expect(find.text('87 ms'), findsOneWidget);
    expect(find.byType(Tooltip), findsNothing);
    final before = tester.getRect(find.byType(ProfileSummaryCard));
    final mouse = await tester.createGesture(kind: PointerDeviceKind.mouse);
    await mouse.addPointer(location: Offset.zero);
    await mouse.moveTo(tester.getCenter(find.byType(ProfileSummaryCard)));
    await tester.pump();
    // Mid-morph the chart is still one line, never two stacked charts.
    await tester.pump(const Duration(milliseconds: 140));
    expect(find.byType(Sparkline), findsOneWidget);
    await tester.pumpAndSettle();
    expect(find.text('123 mbps'), findsOneWidget);
    expect(find.text('87 ms'), findsNothing);
    expect(tester.getRect(find.byType(ProfileSummaryCard)), before);
    final chart = tester.widget<Sparkline>(find.byType(Sparkline));
    // Each series is scaled to its own maximum for the morph.
    expect(chart.values!.whereType<double>().toList(), [1.0]);
    await mouse.moveTo(Offset.zero);
    await tester.pumpAndSettle();
    expect(find.text('87 ms'), findsOneWidget);
    expect(tester.takeException(), isNull);
    await mouse.removePointer();
  });

  testWidgets('rapid hover reversal does not duplicate outgoing children', (
    tester,
  ) async {
    await mount(tester);
    final mouse = await tester.createGesture(kind: PointerDeviceKind.mouse);
    await mouse.addPointer(location: Offset.zero);
    for (var i = 0; i < 8; i++) {
      await mouse.moveTo(tester.getCenter(find.byType(ProfileSummaryCard)));
      await tester.pump(const Duration(milliseconds: 12));
      await mouse.moveTo(Offset.zero);
      await tester.pump(const Duration(milliseconds: 12));
    }
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    expect(find.text('87 ms'), findsOneWidget);
    await mouse.removePointer();
  });

  testWidgets('stale measurements never expose old values or a false outage', (
    tester,
  ) async {
    await mount(tester, stale: true, width: 280);
    expect(find.text('87 ms'), findsNothing);
    expect(find.text('down'), findsNothing);
    expect(find.text('-'), findsOneWidget);
    final mouse = await tester.createGesture(kind: PointerDeviceKind.mouse);
    await mouse.addPointer(location: Offset.zero);
    await mouse.moveTo(tester.getCenter(find.byType(ProfileSummaryCard)));
    await tester.pumpAndSettle();
    expect(find.text('123 mbps'), findsNothing);
    expect(
      tester
          .widget<Sparkline>(find.byType(Sparkline))
          .values!
          .whereType<double>(),
      isEmpty,
    );
    expect(tester.takeException(), isNull);
    await mouse.removePointer();
  });
}
