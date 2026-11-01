import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:nekkobox/core/models.dart';
import 'package:nekkobox/core/nekkobox_core.dart';
import 'package:nekkobox/state/app_state.dart';
import 'package:nekkobox/theme/app_theme.dart';
import 'package:nekkobox/widgets/import_routing_dialog.dart';
import 'package:nekkobox/widgets/route_section.dart';
import 'package:provider/provider.dart';

class _MockCore extends NekkoboxCore {
  String exportedJson = '''{
  "version": 1,
  "created_at": 1730430000,
  "mode": "bypass",
  "groups": {
    "discord": "proxy"
  },
  "services": {
    "example.com": "proxy"
  }
}''';
  String? lastImportedData;
  int resetCalls = 0;
  int exportCalls = 0;

  @override
  String rawCall(String method, [String argsJson = '']) =>
      '{"ok":true,"data":null}';

  @override
  Future<String> exportRoutingSnapshot() async {
    exportCalls++;
    return exportedJson;
  }

  @override
  Future<void> importRoutingSnapshot(String data) async {
    lastImportedData = data;
  }

  @override
  Future<void> resetToDefaults() async {
    resetCalls++;
  }

  @override
  Future<RoutingState> routing() async => const RoutingState.initial();
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  group('ImportRoutingDialog Widget Tests', () {
    testWidgets('shows validation error on empty or invalid json', (tester) async {
      final mock = _MockCore();
      final state = AppState(mock);
      addTearDown(state.dispose);

      await tester.pumpWidget(
        ChangeNotifierProvider<AppState>.value(
          value: state,
          child: MaterialApp(
            theme: buildAppTheme(),
            home: const Scaffold(
              body: ImportRoutingDialog(),
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();

      expect(find.text('импорт правил'), findsOneWidget);
      expect(find.byKey(const ValueKey('import_routing_text_field')), findsOneWidget);

      // 1. Submit empty text
      await tester.tap(find.byKey(const ValueKey('import_routing_submit_btn')));
      await tester.pumpAndSettle();

      expect(find.text('Вставьте JSON-снимок правил'), findsOneWidget);
      expect(mock.lastImportedData, isNull);

      // 2. Enter malformed JSON
      await tester.enterText(
        find.byKey(const ValueKey('import_routing_text_field')),
        '{not a valid json',
      );
      await tester.tap(find.byKey(const ValueKey('import_routing_submit_btn')));
      await tester.pumpAndSettle();

      expect(find.byKey(const ValueKey('import_routing_error_text')), findsOneWidget);
      expect(mock.lastImportedData, isNull);

      // 3. Enter JSON with unsupported version
      await tester.enterText(
        find.byKey(const ValueKey('import_routing_text_field')),
        '{"version": 99}',
      );
      await tester.tap(find.byKey(const ValueKey('import_routing_submit_btn')));
      await tester.pumpAndSettle();

      expect(find.text('Неподдерживаемая версия: 99 (ожидается 1)'), findsOneWidget);
      expect(mock.lastImportedData, isNull);
    });

    testWidgets('submits valid json and closes dialog', (tester) async {
      final mock = _MockCore();
      final state = AppState(mock);
      addTearDown(state.dispose);

      await tester.pumpWidget(
        ChangeNotifierProvider<AppState>.value(
          value: state,
          child: MaterialApp(
            theme: buildAppTheme(),
            home: Builder(
              builder: (ctx) => Scaffold(
                body: Center(
                  child: ElevatedButton(
                    onPressed: () => ImportRoutingDialog.show(ctx),
                    child: const Text('Open'),
                  ),
                ),
              ),
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();

      await tester.tap(find.text('Open'));
      await tester.pumpAndSettle();

      const validJson = '''{
        "version": 1,
        "mode": "bypass",
        "groups": {"discord": "proxy"},
        "services": {"example.com": "proxy"}
      }''';

      await tester.enterText(
        find.byKey(const ValueKey('import_routing_text_field')),
        validJson,
      );
      await tester.tap(find.byKey(const ValueKey('import_routing_submit_btn')));
      await tester.pumpAndSettle();

      expect(mock.lastImportedData, validJson);
      expect(find.byType(ImportRoutingDialog), findsNothing);
    });
  });

  group('RouteSection routing preset actions Tests', () {
    testWidgets('export button in preset menu copies JSON to clipboard and shows toast', (tester) async {
      final mock = _MockCore();
      final state = AppState(mock);
      addTearDown(state.dispose);

      String? clipboardText;
      tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
        SystemChannels.platform,
        (call) async {
          if (call.method == 'Clipboard.setData') {
            clipboardText = (call.arguments as Map)['text'] as String?;
            return null;
          }
          return null;
        },
      );

      await tester.pumpWidget(
        ChangeNotifierProvider<AppState>.value(
          value: state,
          child: MaterialApp(
            theme: buildAppTheme(),
            home: const Scaffold(
              body: SingleChildScrollView(
                child: RouteSection(),
              ),
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();

      // Open preset menu via the 3 dots
      await tester.tap(find.byKey(const ValueKey('routing_presets_button')));
      await tester.pumpAndSettle();

      expect(find.byKey(const ValueKey('routing_preset_export_button')), findsOneWidget);
      await tester.tap(find.byKey(const ValueKey('routing_preset_export_button')));
      await tester.pump();
      await tester.pumpAndSettle();

      expect(mock.exportCalls, 1);
      expect(clipboardText, mock.exportedJson);
      expect(find.text('Правила скопированы'), findsOneWidget);
    });

    testWidgets('reset button prompts confirmation dialog and calls resetToDefaults when confirmed', (tester) async {
      final mock = _MockCore();
      final state = AppState(mock);
      addTearDown(state.dispose);

      await tester.pumpWidget(
        ChangeNotifierProvider<AppState>.value(
          value: state,
          child: MaterialApp(
            theme: buildAppTheme(),
            home: const Scaffold(
              body: SingleChildScrollView(
                child: RouteSection(),
              ),
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();

      // Open preset menu via the 3 dots
      await tester.tap(find.byKey(const ValueKey('routing_presets_button')));
      await tester.pumpAndSettle();

      // 1. Tap reset icon button
      expect(find.byKey(const ValueKey('reset_routing_button_pill')), findsOneWidget);
      await tester.tap(find.byKey(const ValueKey('reset_routing_button_pill')));
      await tester.pumpAndSettle();

      // Confirmation dialog should be visible
      expect(find.text('Сбросить все правила к стандартным?'), findsOneWidget);

      // Cancel first
      await tester.tap(find.text('отмена'));
      await tester.pumpAndSettle();

      expect(mock.resetCalls, 0);
      expect(find.text('Сбросить все правила к стандартным?'), findsNothing);

      // 2. Tap reset again and confirm
      await tester.tap(find.byKey(const ValueKey('reset_routing_button_pill')));
      await tester.pumpAndSettle();

      expect(find.text('Сбросить все правила к стандартным?'), findsOneWidget);
      await tester.tap(find.text('сбросить'));
      await tester.pumpAndSettle();

      expect(mock.resetCalls, 1);
    });
  });
}
