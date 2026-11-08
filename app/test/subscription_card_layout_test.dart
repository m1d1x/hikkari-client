import 'dart:convert';

import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:nekkobox/core/models.dart';
import 'package:nekkobox/core/nekkobox_core.dart';
import 'package:nekkobox/state/app_state.dart';
import 'package:nekkobox/theme/app_theme.dart';
import 'package:nekkobox/widgets/profiles_sheet.dart';
import 'package:provider/provider.dart';

class _Core extends NekkoboxCore {
  @override
  String rawCall(String method, [String argsJson = '']) {
    final Object? data = switch (method) {
      'Health' => {
        'running': false,
        'tunnelOK': false,
        'ip': '-',
        'status': 'off',
        'message': '',
      },
      'UptimeHistory' || 'ServiceUptimeHistory' => <String, dynamic>{},
      'ServerCleanliness' => <String, dynamic>{'running': false},
      'AutoMode' => false,
      'ProfileCountries' => <String, dynamic>{},
      'UpdateStatus' => <String, dynamic>{'state': 'idle', 'installed': ''},
      _ => null,
    };
    return jsonEncode({'ok': true, 'data': data});
  }
}

class _State extends AppState {
  final List<ProfileInfo> _profiles;
  _State(this._profiles) : super(_Core());

  @override
  List<ProfileInfo> get profiles => _profiles;
}

SubscriptionInfo _sub({required int days, required int leftGb}) {
  const total = 1000 * 1000000000;
  return SubscriptionInfo(
    id: 'hk',
    title: '',
    uploadBytes: 0,
    downloadBytes: total - leftGb * 1000000000,
    totalBytes: total,
    expiresAt:
        DateTime.now().add(Duration(days: days)).millisecondsSinceEpoch ~/
            1000 -
        60,
    updateIntervalSeconds: 0,
    webPageUrl: 'https://hikkari.net/pay#t=x',
    supportUrl: '',
  );
}

Future<void> _pump(WidgetTester tester, Size size, SubscriptionInfo sub) async {
  // Real glyph widths: the default test font draws every glyph as a square
  // and would overflow rows that fit in the app.
  final font = FontLoader('IBM Plex Mono');
  for (final w in ['Regular', 'Medium', 'SemiBold', 'Bold']) {
    font.addFont(rootBundle.load('assets/fonts/IBMPlexMono-$w.ttf'));
  }
  await font.load();
  await tester.binding.setSurfaceSize(size);
  addTearDown(() => tester.binding.setSurfaceSize(null));
  final state = _State([
    ProfileInfo(
      name: 'riga-003',
      label: 'рига-003',
      active: false,
      endpoint: '',
      protocol: 'vless',
      subscription: sub,
    ),
  ]);
  await tester.pumpWidget(
    ChangeNotifierProvider<AppState>.value(
      value: state,
      child: MaterialApp(
        theme: buildAppTheme(),
        home: const Scaffold(body: ProfilesSheet()),
      ),
    ),
  );
  await tester.pump();
}

Finder _renew() => find.text('продлить').hitTestable();

void main() {
  testWidgets('title left, period centred, traffic right; no static button', (
    tester,
  ) async {
    await _pump(tester, const Size(900, 400), _sub(days: 30, leftGb: 800));
    expect(_renew(), findsNothing);
    expect(find.textContaining('осталось'), findsNothing);
    final title = tester.getRect(find.text('hikkari'));
    final days = tester.getRect(find.text('30 дней'));
    final traffic = tester.getRect(find.text('800 ГБ'));
    final tile = tester.getRect(find.text('рига-003'));
    expect(title.left, lessThan(tile.left + 1));
    // The period is at the card's centre, the traffic at its right edge.
    expect((days.center.dx - 450).abs(), lessThan(2));
    expect(traffic.right, greaterThan(900 - 60));
    expect(tester.takeException(), isNull);
  });

  testWidgets('hovering a value puts the renew button in its place', (
    tester,
  ) async {
    await _pump(tester, const Size(900, 400), _sub(days: 30, leftGb: 800));
    final mouse = await tester.createGesture(kind: PointerDeviceKind.mouse);
    addTearDown(mouse.removePointer);
    await mouse.addPointer(location: Offset.zero);
    for (final value in ['30 дней', '800 ГБ']) {
      final at = tester.getCenter(find.text(value));
      await mouse.moveTo(at);
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 300));
      expect(_renew(), findsOneWidget, reason: value);
      expect((tester.getCenter(_renew()).dx - at.dx).abs(), lessThan(40));
      await mouse.moveTo(Offset.zero);
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 300));
      expect(_renew(), findsNothing);
    }
  });

  testWidgets('renew button is static when 7 days or 100 GB are left', (
    tester,
  ) async {
    await _pump(tester, const Size(900, 400), _sub(days: 5, leftGb: 800));
    expect(_renew(), findsOneWidget);
    await _pump(tester, const Size(900, 400), _sub(days: 30, leftGb: 90));
    expect(_renew(), findsOneWidget);
  });

  testWidgets('narrow window keeps the header on one line', (tester) async {
    await _pump(tester, const Size(320, 500), _sub(days: 5, leftGb: 90));
    expect(tester.takeException(), isNull);
    final title = tester.getRect(find.text('hikkari'));
    final button = tester.getRect(_renew());
    expect((title.center.dy - button.center.dy).abs(), lessThan(2));
  });

  testWidgets('right click on the header offers renew and delete', (
    tester,
  ) async {
    await _pump(tester, const Size(900, 400), _sub(days: 30, leftGb: 800));
    await tester.tapAt(
      tester.getCenter(find.text('hikkari')),
      buttons: kSecondaryMouseButton,
      kind: PointerDeviceKind.mouse,
    );
    await tester.pumpAndSettle();
    expect(_renew(), findsOneWidget);
    expect(find.text('удалить'), findsOneWidget);
  });
}
