import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:nekkobox/core/nekkobox_core.dart';
import 'package:nekkobox/state/app_state.dart';
import 'package:nekkobox/theme/app_theme.dart';
import 'package:nekkobox/widgets/morphing_word.dart';
import 'package:nekkobox/widgets/power_ring.dart';
import 'package:nekkobox/widgets/power_toggle.dart';
import 'package:provider/provider.dart';

class _QuietCore extends NekkoboxCore {
  @override
  String rawCall(String method, [String argsJson = '']) =>
      '{"ok":true,"data":null}';
}

String? _word(WidgetTester tester) =>
    tester.widget<MorphingWord>(find.byType(MorphingWord)).text;

PowerRingPhase _phase(WidgetTester tester) =>
    tester.widget<PowerRing>(find.byType(PowerRing)).phase;

void main() {
  testWidgets('a press says what it is doing and then gets out of the way', (
    tester,
  ) async {
    final state = AppState(_QuietCore(), dryRun: true);
    addTearDown(state.dispose);
    await tester.runAsync(state.start);

    await tester.pumpWidget(
      ChangeNotifierProvider<AppState>.value(
        value: state,
        child: MaterialApp(
          theme: buildAppTheme(),
          home: const Scaffold(body: Center(child: PowerToggle())),
        ),
      ),
    );
    expect(state.isRunning, isFalse);
    expect(_word(tester), isNull);
    expect(_phase(tester), PowerRingPhase.off);

    // Press: the word and the ring both say the start is in flight.
    await tester.tap(find.byType(PowerRing));
    await tester.pump();
    expect(state.vpnTransition, VpnTransition.connecting);
    expect(_word(tester), 'подключаем');
    expect(_phase(tester), PowerRingPhase.connecting);

    // The dry-run start lands after ~700 ms.
    await tester.pump(const Duration(milliseconds: 800));
    await tester.pump();
    expect(state.isRunning, isTrue);
    expect(state.vpnTransition, VpnTransition.none);
    expect(_word(tester), 'подключено');
    expect(_phase(tester), PowerRingPhase.on);

    // After the hold the word leaves and the profile row comes back.
    await tester.pump(const Duration(milliseconds: 2500));
    expect(_word(tester), isNull);
    await tester.pump(const Duration(seconds: 1));

    // Press again: the stop is announced the same way.
    await tester.tap(find.byType(PowerRing));
    await tester.pump();
    expect(_word(tester), 'отключаем');
    expect(_phase(tester), PowerRingPhase.disconnecting);
    await tester.pump(const Duration(milliseconds: 300));
    await tester.pump();
    expect(state.isRunning, isFalse);
    expect(_word(tester), 'отключено');
    expect(_phase(tester), PowerRingPhase.off);
    await tester.pump(const Duration(milliseconds: 1500));
    expect(_word(tester), isNull);
    await tester.pump(const Duration(seconds: 1));
  });

  Widget word(String? text) => MaterialApp(
    home: Center(
      child: MorphingWord(
        text: text,
        reserve: 'подключение',
        style: const TextStyle(fontSize: 16),
      ),
    ),
  );

  testWidgets('a word changes only its differing ending', (tester) async {
    await tester.pumpWidget(word('подключение'));
    await tester.pumpWidget(word('подключено'));
    await tester.pump(const Duration(milliseconds: 100));
    // Mid-change: "подключен" stays, "ие" leaves while "о" arrives.
    expect(find.text('подключен'), findsOneWidget);
    expect(find.text('ие'), findsOneWidget);
    expect(find.text('о'), findsOneWidget);
    await tester.pumpAndSettle();
    expect(find.text('ие'), findsNothing);
    await tester.pumpWidget(word(null));
    await tester.pumpAndSettle();
    expect(find.byType(Text), findsNothing);
  });

  testWidgets('a word keeps one left edge while it shows, changes and hides', (
    tester,
  ) async {
    double leftOf(String text) => tester.getTopLeft(find.text(text)).dx;

    await tester.pumpWidget(word(null));
    await tester.pumpWidget(word('подключение'));
    await tester.pump(const Duration(milliseconds: 16));
    final edge = leftOf('подключение');
    await tester.pumpAndSettle();
    expect(leftOf('подключение'), edge);

    await tester.pumpWidget(word('подключено'));
    await tester.pump(const Duration(milliseconds: 100));
    expect(leftOf('подключен'), edge);
    await tester.pumpAndSettle();
    expect(leftOf('подключено'), edge);

    await tester.pumpWidget(word(null));
    await tester.pump(const Duration(milliseconds: 100));
    expect(leftOf('подключено'), edge);
  });
}
