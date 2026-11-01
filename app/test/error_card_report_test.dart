import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:nekkobox/core/nekkobox_core.dart';
import 'package:nekkobox/state/app_state.dart';
import 'package:nekkobox/theme/app_theme.dart';
import 'package:nekkobox/ui/nekko_error_card.dart';
import 'package:provider/provider.dart';

/// Records the engine calls; SubmitSupportReport answers as the hub would.
class _Core extends NekkoboxCore {
  _Core({this.refuse = false});
  final bool refuse;
  final calls = <String>[];

  @override
  String rawCall(String method, [String argsJson = '']) {
    calls.add(method);
    if (method == 'SubmitSupportReport' && refuse) {
      return '{"ok":false,"error":"сохранение отчёта: disk full"}';
    }
    return '{"ok":true,"data":null}';
  }
}

Future<void> _pumpCard(
  WidgetTester tester,
  AppState state, {
  String summary = 'ядро не запущено',
}) async {
  await tester.pumpWidget(
    ChangeNotifierProvider<AppState>.value(
      value: state,
      child: MaterialApp(
        theme: buildAppTheme(),
        home: Scaffold(
          body: Center(
            child: NekkoErrorCard(
              title: 'не удалось подключиться',
              summary: summary,
            ),
          ),
        ),
      ),
    ),
  );
}

void main() {
  // The card used to read the state as AppState? — a type no provider is
  // registered under — and swallowed the lookup failure, so «отправить
  // поддержке» never sent anything.
  testWidgets('tapping the error card sends a support report', (tester) async {
    final core = _Core();
    final state = AppState(core, dryRun: false);
    addTearDown(state.dispose);
    await _pumpCard(tester, state);

    await tester.tap(find.byType(NekkoErrorCard));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 100));

    expect(core.calls, contains('SubmitSupportReport'));
    expect(find.text('передано поддержке'), findsOneWidget);
    await tester.pumpAndSettle(const Duration(seconds: 5));
  });

  testWidgets('a report that could not be saved says so and is logged', (
    tester,
  ) async {
    final core = _Core(refuse: true);
    final state = AppState(core, dryRun: false);
    addTearDown(state.dispose);
    // Another error: the same one is reported once per 5 minutes app-wide.
    await _pumpCard(tester, state, summary: 'туннель упал');

    await tester.tap(find.byType(NekkoErrorCard));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 100));

    expect(core.calls, containsAll(['SubmitSupportReport', 'LogUI']));
    expect(find.text('не удалось сохранить отчёт'), findsOneWidget);
    await tester.pumpAndSettle(const Duration(seconds: 5));
  });
}
