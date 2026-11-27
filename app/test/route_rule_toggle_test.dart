import 'dart:convert';

import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:nekkobox/core/models.dart';
import 'package:nekkobox/core/nekkobox_core.dart';
import 'package:nekkobox/state/app_state.dart';
import 'package:nekkobox/theme/app_theme.dart';
import 'package:nekkobox/ui/nekko_switch.dart';
import 'package:nekkobox/widgets/route_section.dart';
import 'package:provider/provider.dart';

/// A manual list in proxy mode: two domains, the second switched off.
class _RulesCore extends NekkoboxCore {
  final calls = <String>[];
  var domains = ['example.com', 'playfabapi.com'];
  var off = <String>{'playfabapi.com'};

  var sequence = 0;

  // The manual list goes over the wire the way the engine sends it, so the
  // test also reads `off` through RoutingState.fromJson.
  @override
  String rawCall(String method, [String argsJson = '']) {
    if (method != 'RoutingStatus') return '{"ok":true,"data":null}';
    final manual = {
      'enabled': true,
      'mode': 'proxy',
      'bypass': {'domains': [], 'ips': [], 'processes': []},
      'proxy': {
        'domains': domains,
        'ips': [],
        'processes': [],
        if (off.isNotEmpty)
          'off': {'domains': off.toList(), 'ips': [], 'processes': []},
      },
    };
    return jsonEncode({
      'ok': true,
      'data': {
        'sequence': ++sequence,
        'revision': sequence,
        'appliedRevision': sequence,
        'status': 'applied',
        'error': '',
        'appliedAt': 0,
        'preview': false,
        'manual': manual,
        'effective': manual,
      },
    });
  }

  @override
  Future<void> setRuleEnabled(
    RoutingMode mode,
    String category,
    String value,
    bool on,
  ) async {
    calls.add('enabled ${mode.wire} $category $value $on');
    on ? off.remove(value) : off.add(value);
  }

  @override
  Future<void> removeRule(
    RoutingMode mode,
    String category,
    String value,
  ) async {
    calls.add('remove ${mode.wire} $category $value');
    domains = domains.where((d) => d != value).toList();
    off.remove(value);
  }
}

Future<(AppState, _RulesCore)> _show(WidgetTester tester) async {
  final core = _RulesCore();
  final state = AppState(core);
  addTearDown(state.dispose);
  await state.refreshRoutingState();
  await tester.pumpWidget(
    ChangeNotifierProvider<AppState>.value(
      value: state,
      child: MaterialApp(
        theme: buildAppTheme(),
        home: const Scaffold(
          body: SingleChildScrollView(child: RouteSection()),
        ),
      ),
    ),
  );
  await tester.pumpAndSettle();
  await tester.tap(find.text('маршрутизация'));
  await tester.pumpAndSettle();
  return (state, core);
}

Finder _row(String value) => find.byKey(ValueKey('manual_rule_$value'));

Finder _switchIn(String value) =>
    find.descendant(of: _row(value), matching: find.byType(NekkoSwitch));

NekkoSwitch _switchOf(WidgetTester tester, String value) =>
    tester.widget(_switchIn(value));

void main() {
  testWidgets('a manual rule switches off and back on and stays in the list', (
    tester,
  ) async {
    final (_, core) = await _show(tester);

    expect(_switchOf(tester, 'example.com').value, isTrue);
    expect(_switchOf(tester, 'playfabapi.com').value, isFalse);
    expect(_switchOf(tester, 'example.com').dense, isTrue);
    expect(find.byIcon(Icons.close), findsNothing, reason: 'no close button');

    await tester.tap(_switchIn('example.com'));
    await tester.pumpAndSettle();
    expect(core.calls, ['enabled proxy domains example.com false']);
    expect(_row('example.com'), findsOneWidget);
    expect(_switchOf(tester, 'example.com').value, isFalse);

    await tester.tap(_switchIn('playfabapi.com'));
    await tester.pumpAndSettle();
    expect(core.calls.last, 'enabled proxy domains playfabapi.com true');
    expect(_switchOf(tester, 'playfabapi.com').value, isTrue);
    expect(tester.takeException(), isNull);
  });

  testWidgets('a manual rule is removed from its context menu', (tester) async {
    final (_, core) = await _show(tester);

    await tester.tap(_row('example.com'), buttons: kSecondaryButton);
    await tester.pumpAndSettle();
    expect(find.text('удалить'), findsOneWidget);
    await tester.tap(find.text('удалить'));
    await tester.pumpAndSettle();

    expect(core.calls, ['remove proxy domains example.com']);
    expect(_row('example.com'), findsNothing);
    expect(_row('playfabapi.com'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}
