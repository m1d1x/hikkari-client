import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:nekkobox/core/models.dart';
import 'package:nekkobox/core/nekkobox_core.dart';
import 'package:nekkobox/state/app_state.dart';
import 'package:nekkobox/theme/app_theme.dart';
import 'package:nekkobox/ui/nekko_error_card.dart';
import 'package:nekkobox/widgets/add_profile_dialog.dart';
import 'package:nekkobox/widgets/brand_header.dart';
import 'package:nekkobox/widgets/import_profile_dialog.dart';
import 'package:nekkobox/widgets/power_toggle.dart';
import 'package:nekkobox/widgets/profiles_sheet.dart';
import 'package:provider/provider.dart';

class _TestEngineCore extends NekkoboxCore {
  bool running = false;
  bool failStart = true;
  String logOutput =
      'INFO[0000] starting engine\n'
      'FATAL[0001] configure tun interface: Access is denied (utun99)\n'
      'trace: stack line 1\n'
      'trace: stack line 2';

  @override
  String rawCall(String method, [String argsJson = '']) {
    final Object? data = switch (method) {
      'Profiles' => [
        {
          'name': 'nl-1',
          'label': 'Netherlands #1',
          'active': true,
          'endpoint': '198.51.100.7:443',
          'protocol': 'vless',
        },
      ],
      'Health' => {
        'running': running,
        'tunnelOK': running,
        'ip': running ? '198.51.100.7' : '—',
        'status': running ? 'ok' : 'off',
        'message': '',
      },
      'UptimeHistory' => <String, dynamic>{},
      'ServiceUptimeHistory' => <String, dynamic>{},
      'ServerCleanliness' => <String, dynamic>{'running': running},
      'AutoMode' => false,
      'ProfileCountries' => <String, dynamic>{'nl-1': 'nl'},
      'UpdateStatus' => <String, dynamic>{'state': 'idle', 'installed': ''},
      'CheckUpdate' => null,
      'LogTail' => logOutput,
      'RoutingStatus' => <String, dynamic>{
        'sequence': 1,
        'revision': 0,
        'appliedRevision': 0,
        'status': 'idle',
        'error': '',
        'appliedAt': 0,
        'preview': false,
        'manual': {'enabled': true, 'mode': 'bypass'},
        'effective': {'enabled': true, 'mode': 'bypass'},
      },
      'RoutingPresets' => <String, dynamic>{
        'syncEnabled': true,
        'configured': false,
        'connected': false,
        'source': 'bundled',
        'platform': 'darwin',
        'lastChecked': '',
        'error': '',
        'choices': <String, dynamic>{},
        'catalog': {'version': 1, 'services': <dynamic>[]},
      },
      _ => null,
    };
    return jsonEncode({'ok': true, 'data': data});
  }

  @override
  Future<void> startVpnAsync() async {
    if (failStart) {
      running = false;
      return;
    }
    running = true;
  }

  @override
  Future<void> stopVpnAsync() async {
    running = false;
  }
}

class _EmptyProfilesState extends AppState {
  _EmptyProfilesState() : super(_TestEngineCore());

  @override
  List<ProfileInfo> get profiles => const [];
}

void main() {
  testWidgets(
    'VPN start error survives background 1s polling ticks, fits 420x650 and copies full error on click',
    (tester) async {
      await tester.binding.setSurfaceSize(const Size(420, 650));
      addTearDown(() => tester.binding.setSurfaceSize(null));

      String? copiedText;
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(SystemChannels.platform, (call) async {
            if (call.method == 'Clipboard.setData') {
              final args = call.arguments as Map<dynamic, dynamic>;
              copiedText = args['text'] as String?;
            }
            return null;
          });
      addTearDown(() {
        TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
            .setMockMethodCallHandler(SystemChannels.platform, null);
      });

      final core = _TestEngineCore();
      final state = AppState(core, dryRun: false);

      await state.start();
      await tester.pumpWidget(
        ChangeNotifierProvider<AppState>.value(
          value: state,
          child: MaterialApp(
            theme: buildAppTheme(),
            home: Scaffold(
              body: Column(
                children: [
                  BrandHeader(onOpenLogs: () {}),
                  const Expanded(
                    child: Align(
                      alignment: Alignment(0, -0.22),
                      child: PowerToggle(),
                    ),
                  ),
                ],
              ),
            ),
          ),
        ),
      );
      await tester.pump();

      // Trigger VPN start that fails and writes the fatal log error.
      final toggleFuture = state.toggleVpn();
      await tester.pump(const Duration(milliseconds: 750));
      await toggleFuture;
      await tester.pump();

      expect(state.lastError, isNotNull);
      expect(state.lastError, contains('Access is denied'));

      // Advance several 1-second background polling ticks: the error must NOT
      // disappear while the tunnel remains stopped.
      for (var i = 0; i < 3; i++) {
        await tester.pump(const Duration(seconds: 1));
      }
      expect(state.lastError, isNotNull);
      expect(find.byType(NekkoErrorCard), findsOneWidget);
      expect(find.text('не удалось подключиться'), findsOneWidget);
      expect(tester.takeException(), isNull);

      // Clicking the error card copies the full diagnostic text and flashes.
      await tester.tap(find.byType(NekkoErrorCard));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 100));
      expect(copiedText, equals(state.lastError));
      expect(find.text('скопировано'), findsOneWidget);
      await tester.pump(const Duration(milliseconds: 700));

      // When the tunnel later transitions to running == true on a background
      // tick, the stale error is cleared automatically.
      core.running = true;
      await tester.pump(const Duration(seconds: 1));
      expect(state.lastError, isNull);
      expect(find.byType(NekkoErrorCard), findsNothing);

      await tester.pumpWidget(const SizedBox());
      state.dispose();
    },
  );

  testWidgets(
    'AddProfileDialog fork opens from empty main screen and ProfilesSheet, launches pay URL or opens ImportProfileDialog',
    (tester) async {
      await tester.binding.setSurfaceSize(const Size(420, 650));
      addTearDown(() => tester.binding.setSurfaceSize(null));

      final state = _EmptyProfilesState();
      addTearDown(state.dispose);

      await tester.pumpWidget(
        ChangeNotifierProvider<AppState>.value(
          value: state,
          child: MaterialApp(
            theme: buildAppTheme(),
            home: const Scaffold(body: Center(child: PowerToggle())),
          ),
        ),
      );
      await tester.pumpAndSettle();

      // Clicking «добавить профиль» on the main screen opens the fork dialog.
      await tester.tap(find.text('добавить профиль'));
      await tester.pumpAndSettle();

      expect(find.byType(AddProfileDialog), findsOneWidget);
      expect(find.text('подключить подписку hikkari'), findsOneWidget);
      expect(find.text('импортировать свой профиль'), findsOneWidget);

      // Choosing «импортировать свой профиль» transitions to ImportProfileDialog.
      await tester.tap(find.text('импортировать свой профиль'));
      await tester.pumpAndSettle();

      expect(find.byType(AddProfileDialog), findsNothing);
      expect(find.byType(ImportProfileDialog), findsOneWidget);
      expect(find.text('импорт по ссылке'), findsOneWidget);

      // Close ImportProfileDialog.
      await tester.tap(find.text('отмена'));
      await tester.pumpAndSettle();
      expect(find.byType(ImportProfileDialog), findsNothing);

      // Verify subscription purchase option calls the pay URL.
      Uri? launched;
      await tester.pumpWidget(
        ChangeNotifierProvider<AppState>.value(
          value: state,
          child: MaterialApp(
            theme: buildAppTheme(),
            home: Scaffold(
              body: Builder(
                builder: (ctx) => TextButton(
                  onPressed: () => AddProfileDialog.show(
                    ctx,
                    onLaunchUrl: (uri) async {
                      launched = uri;
                      return true;
                    },
                  ),
                  child: const Text('open-fork'),
                ),
              ),
            ),
          ),
        ),
      );
      await tester.tap(find.text('open-fork'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('подключить подписку hikkari'));
      await tester.pumpAndSettle();
      expect(launched, Uri.parse('https://hikkari.net/pay'));

      // Direct deep-link call to ImportProfileDialog.show(..., initialLink: ...)
      // still opens ImportProfileDialog straight away with the link filled in.
      const deepLinkUrl = 'https://sub.hikkari.net/sub/demo123';
      await tester.pumpWidget(
        ChangeNotifierProvider<AppState>.value(
          value: state,
          child: MaterialApp(
            theme: buildAppTheme(),
            home: Scaffold(
              body: Builder(
                builder: (ctx) => TextButton(
                  onPressed: () => ImportProfileDialog.show(
                    ctx,
                    initialLink: deepLinkUrl,
                  ),
                  child: const Text('open-direct'),
                ),
              ),
            ),
          ),
        ),
      );
      await tester.tap(find.text('open-direct'));
      await tester.pumpAndSettle();
      expect(find.byType(AddProfileDialog), findsNothing);
      expect(find.byType(ImportProfileDialog), findsOneWidget);
      expect(find.widgetWithText(TextField, deepLinkUrl), findsOneWidget);
      await tester.tap(find.text('отмена'));
      await tester.pumpAndSettle();

      // Also verify ProfilesSheet's «+ добавить» button opens AddProfileDialog.
      await tester.pumpWidget(
        ChangeNotifierProvider<AppState>.value(
          value: state,
          child: MaterialApp(
            theme: buildAppTheme(),
            home: const Scaffold(body: ProfilesSheet()),
          ),
        ),
      );
      await tester.pumpAndSettle();
      await tester.tap(find.text('добавить'));
      await tester.pumpAndSettle();
      expect(find.byType(AddProfileDialog), findsOneWidget);
    },
  );

  testWidgets(
    'Settings sheet displays donate button and client version with fallback',
    (tester) async {
      await tester.binding.setSurfaceSize(const Size(420, 650));
      addTearDown(() => tester.binding.setSurfaceSize(null));

      final state = AppState(_TestEngineCore());
      addTearDown(state.dispose);

      Uri? launchedDonate;
      await tester.pumpWidget(
        ChangeNotifierProvider<AppState>.value(
          value: state,
          child: MaterialApp(
            theme: buildAppTheme(),
            home: Scaffold(
              body: BrandHeader(
                onOpenLogs: () {},
                onLaunchUrl: (uri) async {
                  launchedDonate = uri;
                  return true;
                },
              ),
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();

      // Open settings sheet.
      await tester.tap(find.byIcon(Icons.settings_outlined));
      await tester.pumpAndSettle();

      // Default fallback version is shown when updateStatus.installed is empty.
      expect(find.text('поддержать проект'), findsOneWidget);
      expect(find.text('hikkari v$kFallbackClientVersion'), findsOneWidget);

      // Updating installed version updates the footer label.
      state.debugSetUpdate(
        const UpdateStatus(state: 'idle', installed: '1.0.9'),
      );
      await tester.pumpAndSettle();
      expect(find.text('hikkari v1.0.9'), findsOneWidget);

      // Tapping «поддержать проект» opens https://hikkari.net.
      await tester.ensureVisible(find.text('поддержать проект'));
      await tester.tap(find.text('поддержать проект'));
      await tester.pumpAndSettle();
      expect(launchedDonate, Uri.parse('https://hikkari.net'));
    },
  );
}
