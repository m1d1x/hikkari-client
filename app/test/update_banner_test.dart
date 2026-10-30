import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:nekkobox/core/models.dart';
import 'package:nekkobox/core/nekkobox_core.dart';
import 'package:nekkobox/state/app_state.dart';
import 'package:nekkobox/theme/tokens.dart';
import 'package:nekkobox/widgets/brand_header.dart';
import 'package:provider/provider.dart';

void main() {
  for (final width in [220.0, 260.0, 300.0, 360.0, 420.0, 520.0, 800.0]) {
    testWidgets('the update card fits a $width px window', (tester) async {
      tester.view.physicalSize = Size(width, 300);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      final state = AppState(_NoCore());
      await tester.pumpWidget(
        ChangeNotifierProvider<AppState>.value(
          value: state,
          child: MaterialApp(
            theme: ThemeData(extensions: const [NekkoTokens.dark]),
            home: Scaffold(body: BrandHeader(onOpenLogs: () {})),
          ),
        ),
      );
      state.debugSetUpdate(
        const UpdateStatus(
          state: 'ready',
          installed: '1.0.3',
          version: '1.0.4',
        ),
      );
      await tester.pumpAndSettle();
      expect(tester.takeException(), isNull);
      expect(find.byIcon(Icons.arrow_forward), findsOneWidget);
      // The title is one of its two whole forms, and is clipped only when
      // even the short one cannot fit beside the arrow (the test font is
      // much wider than the app's, so that floor is reached below 300 px).
      final title = find.textContaining('обновление');
      expect(title, findsOneWidget);
      final text = tester.widget<Text>(title);
      expect(['обновление готово', 'обновление'], contains(text.data));
      final shown = tester.getSize(title).width;
      final painter = TextPainter(
        text: TextSpan(
          text: text.data,
          style: DefaultTextStyle.of(tester.element(title)).style,
        ),
        textDirection: TextDirection.ltr,
      )..layout();
      if (width >= 300) {
        expect(shown, greaterThanOrEqualTo(painter.width - 0.5));
      }
    });
  }
}

/// The card reads only AppState.updateStatus; nothing may reach the engine.
class _NoCore implements NekkoboxCore {
  @override
  dynamic noSuchMethod(Invocation invocation) =>
      throw UnimplementedError('${invocation.memberName}');
}
