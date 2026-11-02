import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:nekkobox/core/nekkobox_core.dart';
import 'package:nekkobox/core/routing_presets.dart';
import 'package:nekkobox/state/app_state.dart';
import 'package:nekkobox/theme/app_theme.dart';
import 'package:nekkobox/widgets/brand_header.dart';
import 'package:nekkobox/widgets/network_doctor_dialog.dart';
import 'package:provider/provider.dart';

class _FakeCore extends NekkoboxCore {
  final calls = <String>[];
  final args = <String>[];
  String dpiTools = '["winws","windivert"]';
  String foreignWfp = '["ProtonVPN"]';
  String host = '{"iface":"pinned","dns":"fallback","probed":true}';
  String direct = '{"checked":true,"internet":true,"secureDnsBlocked":[]}';
  String? scanError;
  bool testerMode = false;

  String os = 'windows';
  bool networkPending = false;

  @override
  Future<RoutingPresets> routingPresets() async => RoutingPresets(
    syncEnabled: false,
    tester: testerMode,
    configured: false,
    connected: false,
    source: '',
    platform: '',
    error: '',
    lastChecked: '',
    version: 0,
    services: const [],
    choices: const {},
  );

  @override
  String rawCall(String method, [String argsJson = '']) {
    calls.add(method);
    args.add(argsJson);
    if (method == 'NetworkDiagnose') {
      return '{"ok":true,"data":{"supported":true,"os":"$os",'
          '"proxy":{"setting":{"enabled":true,"server":"127.0.0.1:7890"},"verdict":"orphan","endpoints":["127.0.0.1:7890"]},'
          '"host":$host,"direct":$direct,${scanError == null ? "" : '"scanError":"$scanError",'}'
          '"dpiTools":$dpiTools,"foreignWfp":$foreignWfp}}';
    }
    if (method == 'RepairNetwork') {
      return '{"ok":true,"data":{"actions":[{"name":"proxy_disable","done":true,"detail":"disabled orphan system proxy 127.0.0.1:7890"},{"name":"dns_flush","done":true}]}}';
    }
    if (method == 'ResetNetworkStack') {
      if (os == 'darwin') {
        return '{"ok":true,"data":{"hostsDisabled":1,"networkPending":$networkPending}}';
      }
      return argsJson.contains('"force":true')
          ? '{"ok":true,"data":{"rebootRequired":true,"dpiStopped":["winws"],"dpiFailed":["windivert"]}}'
          : '{"ok":true,"data":{"refused":true,"staticAdapters":["Ethernet"]}}';
    }
    if (method == 'Health') {
      return '{"ok":true,"data":{"running":false,"tunnelOK":false,"ip":"","status":"off","message":""}}';
    }
    if (method == 'SubmitSupportReport') {
      return '{"ok":true,"data":null}';
    }
    return '{"ok":true,"data":null}';
  }
}

void main() {
  late Directory tempDir;
  late AppState state;
  late _FakeCore core;

  setUp(() {
    tempDir = Directory.systemTemp.createTempSync('doctor_test_');
    core = _FakeCore();
    state = AppState(
      core,
      baseDir: tempDir.path,
      routingPreview: true,
      engineId: 'sing-box',
    );
  });

  tearDown(() {
    state.dispose();
    try {
      tempDir.deleteSync(recursive: true);
    } catch (_) {}
  });

  testWidgets(
    'NetworkDoctorDialog renders header, status cards and action buttons',
    (tester) async {
      await tester.pumpWidget(
        ChangeNotifierProvider<AppState>.value(
          value: state,
          child: MaterialApp(
            theme: buildAppTheme(),
            home: Scaffold(
              body: NetworkDoctorDialog(
                diagnosticRunner: (dState) async {
                  dState.setProxyHealth(ComponentHealth.ok);
                  dState.setDnsHealth(ComponentHealth.ok);
                  dState.setAdapterHealth(ComponentHealth.ok);
                  dState.setCoreHealth(ComponentHealth.ok);
                  dState.appendLog('OK', 'тестовая диагностика завершена');
                },
              ),
            ),
          ),
        ),
      );
      await tester.pump();

      expect(find.text('диагностика и авто-фикс'), findsOneWidget);
      expect(find.text('сетевой доктор и восстановление стека'), findsNothing);
      expect(find.byIcon(Icons.close_rounded), findsNothing);
      expect(find.text('системный прокси'), findsOneWidget);
      expect(find.text('кэш dns / резолвер'), findsOneWidget);
      expect(find.text('сетевые адаптеры'), findsOneWidget);
      expect(find.text('состояние ядра'), findsOneWidget);
      expect(find.text('починить сеть'), findsOneWidget);
      expect(find.text('проверить'), findsOneWidget);
      expect(find.text('глубокий сброс'), findsOneWidget);
      expect(find.text('журнал диагностики'), findsNothing);
      expect(find.byIcon(Icons.copy_rounded), findsOneWidget);
      expect(find.byTooltip('скопировать лог'), findsOneWidget);
      expect(find.text('отправить в поддержку'), findsOneWidget);
      expect(find.text('симулировать поломку'), findsNothing);
      expect(find.text('очистить лог'), findsOneWidget);
    },
  );

  testWidgets('Soft repair button runs and logs fix actions', (tester) async {
    state.simulateEngineFailure('test simulated crash');
    expect(state.lastError, isNotNull);

    await tester.pumpWidget(
      ChangeNotifierProvider<AppState>.value(
        value: state,
        child: MaterialApp(
          theme: buildAppTheme(),
          home: Scaffold(
            body: NetworkDoctorDialog(
              diagnosticRunner: (dState) async {
                dState.appendLog('INFO', 'готов к диагностике');
              },
              softRepairRunner: (dState) async {
                state.clearError();
                dState.appendLog('FIX', '=== запуск авто-исправления ===');
                dState.appendLog('OK', 'кэш сброшен, ошибка очищена');
              },
            ),
          ),
        ),
      ),
    );
    await tester.pump();

    // Click 'починить сеть'
    await tester.tap(find.text('починить сеть'));
    await tester.pump();

    // After soft repair, engine error is cleared
    expect(state.lastError, isNull);
    // Console log should contain fix markers
    expect(find.textContaining('авто-исправления'), findsWidgets);
  });

  testWidgets('Only a tester gets the simulated breakdown', (tester) async {
    await tester.pumpWidget(
      ChangeNotifierProvider<AppState>.value(
        value: state,
        child: MaterialApp(
          theme: buildAppTheme(),
          home: Scaffold(
            body: NetworkDoctorDialog(diagnosticRunner: (dState) async {}),
          ),
        ),
      ),
    );
    await state.refreshPresetState();
    await tester.pump();
    expect(find.text('симулировать поломку'), findsNothing);

    // Seven taps on the title can flip the mode while the window is open.
    core.testerMode = true;
    await state.refreshPresetState();
    await tester.pump();
    expect(find.text('симулировать поломку'), findsOneWidget);
  });

  testWidgets(
    'Simulate total breakdown turns on errors and clear logs clears console',
    (tester) async {
      core.testerMode = true;
      await state.refreshPresetState();
      await tester.pumpWidget(
        ChangeNotifierProvider<AppState>.value(
          value: state,
          child: MaterialApp(
            theme: buildAppTheme(),
            home: Scaffold(
              body: NetworkDoctorDialog(diagnosticRunner: (dState) async {}),
            ),
          ),
        ),
      );
      await tester.pump();

      // Tap simulate breakdown
      await tester.tap(find.text('симулировать поломку'));
      await tester.pump();

      expect(state.lastError, isNotNull);
      expect(find.textContaining('симуляция полной поломки'), findsOneWidget);

      // Tap clear logs
      await tester.tap(find.text('очистить лог'));
      await tester.pump();

      expect(find.textContaining('симуляция полной поломки'), findsNothing);
      expect(find.textContaining('журнал очищен'), findsWidgets);
    },
  );

  testWidgets('Settings sheet no longer contains "симулировать ошибку"', (
    tester,
  ) async {
    await tester.pumpWidget(
      ChangeNotifierProvider<AppState>.value(
        value: state,
        child: MaterialApp(
          theme: buildAppTheme(),
          home: Scaffold(body: BrandHeader(onOpenLogs: () {})),
        ),
      ),
    );
    await tester.pump();

    // Open settings sheet
    final settingsIcon = find.byIcon(Icons.settings_outlined);
    expect(settingsIcon, findsOneWidget);
    await tester.tap(settingsIcon);
    await tester.pumpAndSettle();

    // Verify "симулировать ошибку" is completely gone from settings
    expect(find.text('симулировать ошибку'), findsNothing);

    // Verify "диагностика" is present
    expect(find.text('диагностика'), findsOneWidget);
  });

  testWidgets('diagnostics closes the settings sheet; outside tap leaves no '
      'sheet underneath', (tester) async {
    await tester.pumpWidget(
      ChangeNotifierProvider<AppState>.value(
        value: state,
        child: MaterialApp(
          theme: buildAppTheme(),
          home: Scaffold(body: BrandHeader(onOpenLogs: () {})),
        ),
      ),
    );
    await tester.pump();

    await tester.tap(find.byIcon(Icons.settings_outlined));
    await tester.pumpAndSettle();
    await tester.tap(find.text('диагностика'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 600));
    await tester.pump(const Duration(milliseconds: 600));

    expect(find.text('диагностика и авто-фикс'), findsOneWidget);
    // The sheet is gone, not hidden behind the dialog.
    expect(find.text('логи', skipOffstage: false), findsNothing);

    await tester.tapAt(const Offset(2, 2));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 600));
    await tester.pump(const Duration(milliseconds: 600));
    expect(find.text('диагностика и авто-фикс'), findsNothing);
    expect(find.text('логи', skipOffstage: false), findsNothing);
  });

  testWidgets('a click outside is ignored for a second, then closes even '
      'if the check hangs', (tester) async {
    await tester.pumpWidget(
      ChangeNotifierProvider<AppState>.value(
        value: state,
        child: MaterialApp(
          theme: buildAppTheme(),
          home: Builder(
            builder: (c) => Scaffold(
              body: TextButton(
                onPressed: () => NetworkDoctorDialog.show(
                  c,
                  diagnosticRunner: (_) =>
                      Completer<void>().future, // never finishes
                ),
                child: const Text('open'),
              ),
            ),
          ),
        ),
      ),
    );
    await tester.tap(find.text('open'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 600));

    await tester.tapAt(const Offset(2, 2));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 600));
    expect(find.text('диагностика и авто-фикс'), findsOneWidget);

    await tester.pump(const Duration(seconds: 1));
    await tester.tapAt(const Offset(2, 2));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 600));
    expect(find.text('диагностика и авто-фикс'), findsNothing);
  });

  // The engine-backed path: dry run off, so calls reach the fake core.
  Widget realDialog() {
    state.dispose();
    state = AppState(
      core,
      baseDir: tempDir.path,
      routingPreview: true,
      engineId: 'sing-box',
      dryRun: false,
    );
    return ChangeNotifierProvider<AppState>.value(
      value: state,
      child: MaterialApp(
        theme: buildAppTheme(),
        home: const Scaffold(body: NetworkDoctorDialog()),
      ),
    );
  }

  // The whole log, through the window's own «скопировать лог»: the list is
  // lazy, so lines scrolled out of view are not in the widget tree.
  Future<String> logText(WidgetTester tester) async {
    String copied = '';
    tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
      SystemChannels.platform,
      (call) async {
        if (call.method == 'Clipboard.setData') {
          copied = (call.arguments as Map)['text'] as String;
        }
        return null;
      },
    );
    await tester.tap(find.byTooltip('скопировать лог'));
    await tester.pump();
    return copied;
  }

  testWidgets(
    'Diagnosis comes from the engine and the log names no addresses',
    (tester) async {
      await tester.pumpWidget(realDialog());
      await tester.pumpAndSettle();

      expect(core.calls, contains('NetworkDiagnose'));
      final log = await logText(tester);
      expect(log, contains('неработающий локальный порт'));
      expect(log, contains('резервный'));
      expect(log, isNot(contains('127.0.0.1')));
      expect(log, isNot(contains('7890')));
    },
  );

  testWidgets('The scan names DPI tools and foreign WFP only in the log', (
    tester,
  ) async {
    await tester.pumpWidget(realDialog());
    await tester.pumpAndSettle();

    final log = await logText(tester);
    expect(log, contains('инструменты обхода блокировок: winws, windivert'));
    expect(log, contains('сторонние WFP-провайдеры: ProtonVPN'));
    expect(log, contains('не трогаем'));
  });

  testWidgets('A scan that could not run does not read as nothing found', (
    tester,
  ) async {
    core
      ..dpiTools = 'null'
      ..foreignWfp = 'null';
    await tester.pumpWidget(realDialog());
    await tester.pumpAndSettle();

    final log = await logText(tester);
    expect(
      log,
      contains('инструменты обхода блокировок: проверка не выполнена'),
    );
    expect(log, contains('сторонние WFP-провайдеры: проверка не выполнена'));
    expect(log, isNot(contains('не обнаружены')));
  });

  group('Green means checked', () {
    // The fixture's orphan proxy is always one red card.
    const healthy = '{"iface":"auto","dns":"system","probed":true}';
    Future<int> redCards(WidgetTester tester) async {
      await tester.pumpWidget(realDialog());
      await tester.pumpAndSettle();
      return tester.widgetList(find.byIcon(Icons.cancel_rounded)).length;
    }

    testWidgets('A healthy machine has only the proxy card red', (
      tester,
    ) async {
      core.host = healthy;
      expect(await redCards(tester), 1);
    });

    testWidgets('A dead direct path turns a card red', (tester) async {
      core
        ..host = healthy
        ..direct = '{"checked":true,"internet":false,"secureDnsBlocked":[]}';
      expect(await redCards(tester), 2);
      expect(await logText(tester), contains('сайты мимо VPN не открываются'));
    });

    testWidgets('A blocked Secure DNS is named and warns without the tunnel', (
      tester,
    ) async {
      core
        ..host = healthy
        ..direct =
            '{"checked":true,"internet":true,"secureDnsBlocked":["Cloudflare"]}';
      expect(await redCards(tester), 2);
      expect(await logText(tester), contains('безопасный DNS (Cloudflare)'));
    });

    testWidgets('A selection nobody made is not green', (tester) async {
      core
        ..host = '{}'
        ..direct = '{"checked":false}';
      expect(await redCards(tester), 3);
    });

    testWidgets('Attached to a running tunnel, the direct probe answers', (
      tester,
    ) async {
      core
        ..host = '{"probed":false}'
        ..direct = '{"checked":true,"internet":true,"secureDnsBlocked":[]}';
      expect(await redCards(tester), 1);
    });

    testWidgets('A failed scan is shown, not swallowed', (tester) async {
      core
        ..host = healthy
        ..scanError = 'служба hikkari не запущена';
      expect(await redCards(tester), 2);
      expect(await logText(tester), contains('служба hikkari не запущена'));
    });
  });

  testWidgets('A clean scan says so', (tester) async {
    core
      ..dpiTools = '[]'
      ..foreignWfp = '[]';
    await tester.pumpWidget(realDialog());
    await tester.pumpAndSettle();

    final log = await logText(tester);
    expect(log, contains('инструменты обхода блокировок: не обнаружены'));
    expect(log, contains('сторонние WFP-провайдеры: не обнаружены'));
  });

  testWidgets('Soft repair calls the engine instead of shelling out', (
    tester,
  ) async {
    await tester.pumpWidget(realDialog());
    await tester.pumpAndSettle();
    await tester.tap(find.text('починить сеть'));
    await tester.pumpAndSettle();

    expect(core.calls, contains('RepairNetwork'));
    final log = await logText(tester);
    expect(log, contains('отключение зависшего системного прокси: выполнено'));
    expect(log, isNot(contains('127.0.0.1')));
    await tester.pump(const Duration(seconds: 10));
  });

  testWidgets('Deep reset asks again before wiping static IP settings', (
    tester,
  ) async {
    await tester.pumpWidget(realDialog());
    await tester.pumpAndSettle();
    await tester.tap(find.text('глубокий сброс'));
    await tester.pumpAndSettle();
    // The confirmation is what makes stopping third-party tools a choice.
    expect(find.textContaining('WinDivert'), findsOneWidget);
    await tester.tap(find.text('сбросить стек'));
    await tester.pumpAndSettle();

    expect(find.textContaining('Ethernet'), findsOneWidget);
    expect(core.args.where((a) => a.contains('"force":true')), isEmpty);

    await tester.tap(find.text('сбросить и удалить'));
    await tester.pumpAndSettle();
    expect(core.args.where((a) => a.contains('"force":true')), hasLength(1));
    final log = await logText(tester);
    expect(log, contains('перезагрузите компьютер'));
    expect(log, contains('остановлены, запуск ручной: winws'));
    expect(log, contains('не удалось полностью остановить: windivert'));
    await tester.pump(const Duration(seconds: 10));
  });

  testWidgets('On macOS the window speaks of macOS and asks for no reboot', (
    tester,
  ) async {
    core
      ..os = 'darwin'
      ..dpiTools = 'null'
      ..foreignWfp = '["mullvad"]';
    await tester.pumpWidget(realDialog());
    await tester.pumpAndSettle();

    var log = await logText(tester);
    expect(log, isNot(contains('инструменты обхода блокировок')));
    expect(log, isNot(contains('WFP')));
    expect(log, contains('сторонние правила pf: mullvad'));

    await tester.tap(find.text('глубокий сброс'));
    await tester.pumpAndSettle();
    expect(find.textContaining('Winsock'), findsNothing);
    expect(find.textContaining('Перезагрузка не нужна'), findsOneWidget);
    await tester.tap(find.text('сбросить стек'));
    await tester.pumpAndSettle();

    log = await logText(tester);
    expect(log, contains('файл hosts: отключено записей — 1'));
    expect(log, contains('перезагрузка не нужна'));
    expect(log, isNot(contains('перезагрузите компьютер')));
    expect(core.calls.where((c) => c == 'NetworkDiagnose'), hasLength(2));
    await tester.pump(const Duration(seconds: 10));
  });

  testWidgets('A network that is not back yet does not read as repaired', (
    tester,
  ) async {
    core
      ..os = 'darwin'
      ..networkPending = true;
    await tester.pumpWidget(realDialog());
    await tester.pumpAndSettle();
    await tester.tap(find.text('глубокий сброс'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('сбросить стек'));
    await tester.pumpAndSettle();

    final log = await logText(tester);
    expect(log, contains('сеть ещё не поднялась'));
    expect(log, isNot(contains('глубокий сброс завершён')));
    // Probing now would only report a network that is still coming up.
    expect(core.calls.where((c) => c == 'NetworkDiagnose'), hasLength(1));
    await tester.pump(const Duration(seconds: 10));
  });
}
