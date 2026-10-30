import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:provider/provider.dart';
import 'package:nekkobox/core/models.dart';
import 'package:nekkobox/core/nekkobox_core.dart';
import 'package:nekkobox/state/app_state.dart';
import 'package:nekkobox/theme/app_theme.dart';
import 'package:nekkobox/widgets/profiles_sheet.dart';

class _Core extends NekkoboxCore {
  @override
  String rawCall(String method, [String argsJson = '']) =>
      throw StateError('unexpected engine call: $method');
}

class _State extends AppState {
  _State() : super(_Core());
  SubscriptionInfo info = _info(1, 2, 10, 5);

  static SubscriptionInfo _info(
    int upload,
    int download,
    int total,
    int days,
  ) => SubscriptionInfo(
    id: 'subscription',
    title: 'Provider',
    uploadBytes: upload * 1000000000,
    downloadBytes: download * 1000000000,
    totalBytes: total * 1000000000,
    expiresAt:
        DateTime.now().add(Duration(days: days)).millisecondsSinceEpoch ~/ 1000,
    updateIntervalSeconds: 300,
    webPageUrl: '',
    supportUrl: '',
  );

  @override
  List<ProfileInfo> get profiles => [
    ProfileInfo(
      name: 'server',
      label: 'Server',
      active: false,
      endpoint: 'example.test:443',
      protocol: 'vless',
      subscription: info,
    ),
  ];

  void update(int upload, int download, int total, int days) {
    info = _info(upload, download, total, days);
    notifyListeners();
  }
}

void main() {
  testWidgets(
    'subscription card updates days and remaining traffic without interaction',
    (tester) async {
      final state = _State();
      addTearDown(state.dispose);
      await tester.pumpWidget(
        ChangeNotifierProvider<AppState>.value(
          value: state,
          child: MaterialApp(
            theme: buildAppTheme(),
            home: const Scaffold(body: ProfilesSheet()),
          ),
        ),
      );
      expect(find.text('7 ГБ осталось'), findsOneWidget);
      expect(find.text('5 дней'), findsOneWidget);
      state.update(2, 4, 20, 12);
      await tester.pump();
      expect(find.text('14 ГБ осталось'), findsOneWidget);
      expect(find.text('12 дней'), findsOneWidget);
      expect(find.text('7 ГБ осталось'), findsNothing);
      state.update(20, 4, 20, -1);
      await tester.pump();
      expect(find.text('0 ГБ осталось'), findsOneWidget);
      expect(find.text('истекла'), findsOneWidget);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox());
    },
  );
}
