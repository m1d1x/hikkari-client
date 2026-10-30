import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:nekkobox/core/models.dart';
import 'package:nekkobox/core/nekkobox_core.dart';
import 'package:nekkobox/state/app_state.dart';
import 'package:nekkobox/widgets/import_profile_dialog.dart';
import 'package:provider/provider.dart';
import 'package:nekkobox/theme/app_theme.dart';
import 'package:nekkobox/ui/nekko_toast.dart';

class _Core extends NekkoboxCore {
  @override
  String rawCall(String method, [String argsJson = '']) =>
      '{"ok":true,"data":null}';
}

class _ImportState extends AppState {
  _ImportState() : super(_Core());
  bool added = false;
  @override
  List<ProfileInfo> get profiles => added
      ? [
          ProfileInfo.fromJson({'name': 'test'}),
        ]
      : [];
  @override
  Future<String?> addProfileFromUri(String uri) async {
    added = true;
    return 'test';
  }
}

void main() {
  testWidgets(
    'import folds into the Nekko outcome and closes without a second notice',
    (tester) async {
      final state = _ImportState();
      addTearDown(state.dispose);
      await tester.pumpWidget(
        ChangeNotifierProvider<AppState>.value(
          value: state,
          child: MaterialApp(
            theme: buildAppTheme(),
            builder: (context, child) => MediaQuery(
              data: MediaQuery.of(
                context,
              ).copyWith(textScaler: const TextScaler.linear(0.8)),
              child: child!,
            ),
            home: Scaffold(
              body: Builder(
                builder: (context) => TextButton(
                  onPressed: () => ImportProfileDialog.show(context),
                  child: const Text('open'),
                ),
              ),
            ),
          ),
        ),
      );
      await tester.tap(find.text('open'));
      await tester.pumpAndSettle();
      final expandedHeight = tester
          .getSize(find.byType(AnimatedCrossFade))
          .height;
      await tester.enterText(find.byType(TextField), 'vless://test');
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 16));
      await tester.pump(const Duration(milliseconds: 80));
      expect(
        tester.getSize(find.byType(AnimatedCrossFade)).height,
        lessThan(expandedHeight),
      );
      expect(find.text('профиль добавлен'), findsOneWidget);
      expect(
        tester.getCenter(find.text('профиль добавлен')).dx,
        closeTo(tester.getCenter(find.byType(AnimatedCrossFade)).dx, 0.5),
      );
      await tester.pump(const Duration(milliseconds: 450));
      final formFade = tester.widget<FadeTransition>(
        find
            .ancestor(
              of: find.text('импорт по ссылке'),
              matching: find.byType(FadeTransition),
            )
            .first,
      );
      expect(formFade.opacity.value, 0);
      expect(find.text('профиль добавлен'), findsOneWidget);
      expect(find.text('добавлено серверов: 1'), findsOneWidget);
      final cardCenter = tester.getCenter(find.byType(AnimatedCrossFade)).dx;
      for (final element in [
        find.byIcon(Icons.check_rounded),
        find.text('профиль добавлен'),
        find.text('добавлено серверов: 1'),
      ]) {
        expect(tester.getCenter(element).dx, closeTo(cardCenter, 0.5));
      }

      await tester.pump(const Duration(milliseconds: 600));
      expect(find.byType(ImportProfileDialog), findsOneWidget);
      await tester.pump(const Duration(milliseconds: 800));
      await tester.pumpAndSettle();
      expect(find.byType(ImportProfileDialog), findsNothing);
      expect(find.text('Добавлено серверов: 1'), findsNothing);
      expect(find.byType(SnackBar), findsNothing);
      expect(tester.takeException(), isNull);
    },
  );
  testWidgets('compact notice expires and does not block clicks', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(420, 650));
    addTearDown(() => tester.binding.setSurfaceSize(null));
    late BuildContext source;
    var taps = 0;
    await tester.pumpWidget(
      MaterialApp(
        theme: buildAppTheme(),
        home: Scaffold(
          body: Builder(
            builder: (context) {
              source = context;
              return GestureDetector(
                behavior: HitTestBehavior.opaque,
                onTap: () => taps++,
                child: const SizedBox.expand(),
              );
            },
          ),
        ),
      ),
    );
    NekkoToast.show(source, 'Добавлено серверов: 1');
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 180));
    final text = find.text('Добавлено серверов: 1');
    final pill = find
        .ancestor(of: text, matching: find.byType(Container))
        .first;
    final rect = tester.getRect(pill);
    expect(rect.center.dx, closeTo(210, 0.1));
    expect(rect.bottom, closeTo(554, 0.1));
    expect(rect.width, lessThan(372));
    expect(find.byType(SnackBar), findsNothing);
    await tester.tapAt(rect.center);
    expect(taps, 1);
    await tester.pump(NekkoToast.readHold);
    await tester.pump(const Duration(milliseconds: 180));
    await tester.pumpAndSettle();
    expect(text, findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('anchor survives dialog closing and replaces previous notice', (
    tester,
  ) async {
    late BuildContext source;
    late ToastAnchor anchor;
    await tester.pumpWidget(
      MaterialApp(
        theme: buildAppTheme(),
        home: Scaffold(
          body: Builder(
            builder: (context) {
              source = context;
              return const SizedBox.expand();
            },
          ),
        ),
      ),
    );
    final closed = showDialog<void>(
      context: source,
      builder: (context) {
        anchor = NekkoToast.anchor(context);
        return const Dialog(child: Text('dialog'));
      },
    );
    await tester.pumpAndSettle();
    Navigator.of(tester.element(find.text('dialog'))).pop();
    await closed;
    await tester.pumpAndSettle();
    anchor.show('first');
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 180));
    anchor.show('second', hold: NekkoToast.glanceHold);
    await tester.pump();
    expect(find.text('first'), findsNothing);
    expect(find.text('second'), findsOneWidget);
    await tester.pump(NekkoToast.glanceHold);
    await tester.pump(const Duration(milliseconds: 180));
    expect(find.text('second'), findsNothing);
    await tester.pumpWidget(const SizedBox());
    anchor.show('after disposal');
    await tester.pump();
    expect(tester.takeException(), isNull);
  });

  testWidgets('long notice wraps and cleans up when window is disposed', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(280, 400));
    addTearDown(() => tester.binding.setSurfaceSize(null));
    late BuildContext source;
    await tester.pumpWidget(
      MaterialApp(
        theme: buildAppTheme(),
        home: Scaffold(
          body: Builder(
            builder: (context) {
              source = context;
              return const SizedBox.expand();
            },
          ),
        ),
      ),
    );
    const message = 'Не удалось переключить профиль: сервер недоступен';
    NekkoToast.show(source, message);
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 180));
    expect(tester.takeException(), isNull);
    expect(tester.getSize(find.text(message)).height, greaterThan(20));
    await tester.pumpWidget(const SizedBox());
    await tester.pump(const Duration(seconds: 3));
    expect(tester.takeException(), isNull);
  });
}
