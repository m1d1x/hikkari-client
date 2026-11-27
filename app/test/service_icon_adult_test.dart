import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:nekkobox/theme/app_theme.dart';
import 'package:nekkobox/widgets/service_icon.dart';

void main() {
  testWidgets('the adult group wears an 18+ plate, not a brand mark', (
    tester,
  ) async {
    await tester.pumpWidget(
      MaterialApp(
        theme: buildAppTheme(),
        home: const Scaffold(
          body: Center(
            child: ServiceIconBadge(
              iconId: adultIconId,
              color: Color(0xFFC7D0D8),
              faviconUrl: 'https://example.invalid/favicon.ico',
            ),
          ),
        ),
      ),
    );

    expect(find.text('18+'), findsOneWidget);
    expect(
      find.byType(Image),
      findsNothing,
      reason: 'no logo is bundled and nothing is fetched from those sites',
    );
    expect(tester.getSize(find.byType(ServiceIconBadge)), const Size(30, 30));
    expect(tester.takeException(), isNull);
  });
}
