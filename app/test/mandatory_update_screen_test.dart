import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:nekkobox/core/models.dart';
import 'package:nekkobox/core/nekkobox_core.dart';
import 'package:nekkobox/screens/mandatory_update_screen.dart';
import 'package:nekkobox/state/app_state.dart';
import 'package:nekkobox/theme/tokens.dart';
import 'package:provider/provider.dart';

void main() {
  const downloading = UpdateStatus(
    state: 'downloading',
    installed: '1.0.3',
    version: '1.1.0',
    mandatory: true,
    bytesDone: 5 << 20,
    bytesTotal: 14 << 20,
  );
  const ready = UpdateStatus(
    state: 'ready',
    installed: '1.0.3',
    version: '1.1.0',
    mandatory: true,
  );

  Future<AppState> pump(
    WidgetTester tester,
    double width,
    UpdateStatus u,
  ) async {
    tester.view.physicalSize = Size(width, 640);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final state = AppState(_NoCore());
    await tester.pumpWidget(
      ChangeNotifierProvider<AppState>.value(
        value: state,
        child: MaterialApp(
          theme: ThemeData(extensions: const [NekkoTokens.dark]),
          home: const MandatoryUpdateScreen(),
        ),
      ),
    );
    state.debugSetUpdate(u);
    // The progress bar animates indefinitely only without a value; this one
    // has one, so the frame settles.
    await tester.pumpAndSettle();
    return state;
  }

  // From 280: below that the window's own titlebar (its three buttons and
  // the name) no longer fits, here as on the home screen.
  for (final width in [280.0, 300.0, 360.0, 420.0, 520.0, 800.0]) {
    testWidgets('downloading fits a $width px window', (tester) async {
      await pump(tester, width, downloading);
      expect(tester.takeException(), isNull);
      final title = tester.widget<Text>(find.textContaining('обновиться'));
      expect(['нужно обновиться', 'обновиться'], contains(title.data));
      expect(find.byType(LinearProgressIndicator), findsOneWidget);
      expect(find.text('скачивается · 5.0 мб из 14.0 мб'), findsOneWidget);
      expect(find.byIcon(Icons.arrow_forward), findsNothing);
      // The tunnel stays reachable from the blocking screen.
      expect(find.text('vpn выключен'), findsOneWidget);
      expect(find.text('включить'), findsOneWidget);
    });

    testWidgets('ready offers one restart in a $width px window', (
      tester,
    ) async {
      await pump(tester, width, ready);
      expect(tester.takeException(), isNull);
      expect(find.byIcon(Icons.arrow_forward), findsOneWidget);
      expect(find.byType(LinearProgressIndicator), findsNothing);
      expect(find.text('1.0.3 → 1.1.0'), findsOneWidget);
      // The word is whole or absent, never clipped.
      final word = find.text('перезапустить');
      // The test font is much wider than the app's: the word drops out
      // below 360 px here, far below that in the app.
      if (width >= 360) expect(word, findsOneWidget);
      if (word.evaluate().isNotEmpty) {
        final text = tester.widget<Text>(word);
        final painter = TextPainter(
          text: TextSpan(
            text: text.data,
            style: DefaultTextStyle.of(
              tester.element(word),
            ).style.merge(text.style),
          ),
          textDirection: TextDirection.ltr,
        )..layout();
        expect(
          tester.getSize(word).width,
          greaterThanOrEqualTo(painter.width - 0.5),
        );
      }
    });
  }

  test('only a mandatory update in flight blocks the app', () {
    expect(downloading.blocks, isTrue);
    expect(ready.blocks, isTrue);
    expect(const UpdateStatus(state: 'error', mandatory: true).blocks, isTrue);
    expect(
      const UpdateStatus(state: 'ready', version: '1.0.4').blocks,
      isFalse,
    );
    expect(const UpdateStatus(state: 'idle', mandatory: true).blocks, isFalse);
  });
}

/// The screen reads only AppState; nothing may reach the engine.
class _NoCore implements NekkoboxCore {
  @override
  dynamic noSuchMethod(Invocation invocation) =>
      throw UnimplementedError('${invocation.memberName}');
}
