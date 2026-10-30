import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:nekkobox/core/nekkobox_core.dart';
import 'package:nekkobox/core/models.dart';
import 'package:nekkobox/state/app_state.dart';
import 'package:nekkobox/theme/app_theme.dart';
import 'package:nekkobox/theme/tokens.dart';
import 'package:nekkobox/ui/nekko_dialog.dart';
import 'package:nekkobox/ui/nekko_dialog_action.dart';
import 'package:nekkobox/ui/nekko_dialog_field.dart';
import 'package:nekkobox/widgets/import_profile_dialog.dart';
import 'package:nekkobox/widgets/service_ping_sheet.dart';
import 'package:provider/provider.dart';

class _Core extends NekkoboxCore {
  @override
  String rawCall(String method, [String argsJson = ""]) =>
      '{"ok":true,"data":null}';
  @override
  Future<List<CustomService>> listCustomServices() async => [];
}

class _ActiveState extends AppState {
  _ActiveState() : super(_Core());
  @override
  String? get activeProfileId => 'test-profile';
}

void main() {
  testWidgets(
    'service rows animate hover, edit and preserve service on cancelled removal',
    (tester) async {
      await tester.binding.setSurfaceSize(const Size(420, 650));
      addTearDown(() => tester.binding.setSurfaceSize(null));
      final state = _ActiveState();
      addTearDown(state.dispose);
      await tester.pumpWidget(
        ChangeNotifierProvider<AppState>.value(
          value: state,
          child: MaterialApp(
            theme: buildAppTheme(),
            home: const Scaffold(
              body: ServicePingSheet(profileLabel: 'Profile'),
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.text('YouTube'), findsOneWidget);
      final mouse = await tester.createGesture(kind: PointerDeviceKind.mouse);
      await mouse.addPointer(location: Offset.zero);
      await mouse.moveTo(tester.getCenter(find.text('YouTube')));
      await tester.pump();
      await tester.pump(AppDurations.hover);
      final surfaces = tester.widgetList<AnimatedContainer>(
        find.byType(AnimatedContainer),
      );
      expect(
        surfaces.any(
          (surface) =>
              (surface.decoration as BoxDecoration).color ==
              Color.alphaBlend(
                NekkoTokens.dark.primary.withValues(alpha: 0.08),
                NekkoTokens.dark.surface2,
              ),
        ),
        isTrue,
      );
      await tester.tap(find.text('YouTube'));
      await tester.pumpAndSettle();
      expect(find.text('редактировать сайт'), findsOneWidget);
      await tester.enterText(find.byType(TextField), 'video.example.com');
      await tester.testTextInput.receiveAction(TextInputAction.done);
      await tester.pumpAndSettle();
      expect(find.text('video.example.com'), findsOneWidget);
      await tester.drag(find.text('YouTube'), const Offset(-350, 0));
      await tester.pumpAndSettle();
      expect(find.text('убрать сервис?'), findsOneWidget);
      await tester.tap(find.text('отмена'));
      await tester.pumpAndSettle();
      expect(find.text('YouTube'), findsOneWidget);
      expect(tester.takeException(), isNull);
      await mouse.removePointer();
    },
  );

  testWidgets('service input reuses import controls, validates and cancels', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(420, 650));
    addTearDown(() => tester.binding.setSurfaceSize(null));
    final state = AppState(_Core());
    addTearDown(state.dispose);
    await tester.pumpWidget(
      ChangeNotifierProvider.value(
        value: state,
        child: MaterialApp(
          theme: buildAppTheme(),
          home: const Scaffold(
            body: ServicePingSheet(
              profileLabel: 'very long profile label which must fit',
            ),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    await tester.tap(find.byTooltip('добавить свой сайт'));
    await tester.pumpAndSettle();
    expect(find.text('свой сайт'), findsOneWidget);
    expect(find.byType(NekkoDialog), findsOneWidget);
    expect(find.byType(NekkoDialogField), findsOneWidget);
    await tester.tap(find.text('добавить'));
    await tester.pumpAndSettle();
    expect(find.text('введите адрес сайта'), findsOneWidget);
    await tester.enterText(find.byType(TextField), 'example.com');
    await tester.pumpAndSettle();
    expect(find.text('введите адрес сайта'), findsNothing);
    await tester.tap(find.text('отмена'));
    await tester.pumpAndSettle();
    expect(find.byType(NekkoDialog), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('import fits narrow window and retains inline error', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(360, 500));
    addTearDown(() => tester.binding.setSurfaceSize(null));
    final state = AppState(_Core());
    addTearDown(state.dispose);
    await tester.pumpWidget(
      ChangeNotifierProvider.value(
        value: state,
        child: MaterialApp(
          theme: buildAppTheme(),
          home: const Scaffold(body: ImportProfileDialog()),
        ),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.text('добавить'));
    await tester.pumpAndSettle();
    expect(find.text('вставьте ссылку для импорта.'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets(
    'confirmation cancellation and explicit deletion return distinct results',
    (tester) async {
      bool? result;
      await tester.pumpWidget(
        MaterialApp(
          theme: buildAppTheme(),
          home: Builder(
            builder: (context) => Scaffold(
              body: TextButton(
                onPressed: () async {
                  result = await NekkoDialog.confirm(
                    context,
                    title: 'удалить профиль?',
                    message: 'Profile',
                    confirmLabel: 'удалить',
                  );
                },
                child: const Text('open'),
              ),
            ),
          ),
        ),
      );
      await tester.tap(find.text('open'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('отмена'));
      await tester.pumpAndSettle();
      expect(result, isFalse);
      await tester.tap(find.text('open'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('удалить'));
      await tester.pumpAndSettle();
      expect(result, isTrue);
    },
  );

  testWidgets('hover animates and disabled action ignores clicks', (
    tester,
  ) async {
    var calls = 0;
    await tester.pumpWidget(
      MaterialApp(
        theme: buildAppTheme(),
        home: Scaffold(
          body: NekkoDialogAction(
            label: 'action',
            enabled: true,
            onPressed: () => calls++,
          ),
        ),
      ),
    );
    final mouse = await tester.createGesture(kind: PointerDeviceKind.mouse);
    await mouse.addPointer(location: Offset.zero);
    await mouse.moveTo(tester.getCenter(find.text('action')));
    await tester.pump();
    await tester.pump(AppDurations.hover);
    final surface = tester.widget<AnimatedContainer>(
      find
          .descendant(
            of: find.byType(NekkoDialogAction),
            matching: find.byType(AnimatedContainer),
          )
          .first,
    );
    expect(
      (surface.decoration as BoxDecoration).color,
      NekkoTokens.dark.surface2,
    );
    await tester.tap(find.text('action'));
    expect(calls, 1);
    await tester.pumpWidget(
      MaterialApp(
        theme: buildAppTheme(),
        home: Scaffold(
          body: NekkoDialogAction(
            label: 'action',
            enabled: false,
            onPressed: () => calls++,
          ),
        ),
      ),
    );
    await tester.tap(find.text('action'));
    expect(calls, 1);
    await mouse.removePointer();
  });
}
