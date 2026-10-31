import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:nekkobox/theme/app_theme.dart';
import 'package:nekkobox/ui/nekko_dialog_action.dart';

void main() {
  Widget host(bool busy, VoidCallback onPressed) => MaterialApp(
    theme: buildAppTheme(),
    home: Scaffold(
      body: Center(
        child: NekkoDialogAction(
          label: 'добавить',
          enabled: true,
          busy: busy,
          primary: true,
          onPressed: onPressed,
        ),
      ),
    ),
  );

  testWidgets('a busy action keeps its size and shows a round spinner', (
    tester,
  ) async {
    var presses = 0;
    await tester.pumpWidget(host(false, () => presses++));
    final idle = tester.getSize(find.byType(NekkoDialogAction));

    await tester.pumpWidget(host(true, () => presses++));
    await tester.pump(const Duration(milliseconds: 100));
    expect(tester.getSize(find.byType(NekkoDialogAction)), idle);

    final spinner = tester.getSize(find.byType(CircularProgressIndicator));
    expect(spinner.width, spinner.height, reason: 'spinner is stretched');
    expect(spinner.width, 12);

    await tester.tap(find.byType(NekkoDialogAction));
    expect(presses, 0, reason: 'a busy action must not accept presses');
  });
}
