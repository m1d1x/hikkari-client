import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:nekkobox/core/nekkobox_core.dart';
import 'package:nekkobox/state/app_state.dart';

class _PreviewCore extends NekkoboxCore {
  @override
  String rawCall(String method, [String argsJson = '']) => jsonEncode({
    'ok': true,
    'data': switch (method) {
      'RoutingStatus' => {
        'sequence': 0,
        'revision': 0,
        'appliedRevision': 0,
        'status': 'idle',
        'error': '',
        'appliedAt': 0,
        'preview': true,
        'manual': {'enabled': false, 'mode': 'bypass'},
        'effective': {'enabled': false, 'mode': 'bypass'},
      },
      'RoutingPresets' => {
        'syncEnabled': false,
        'configured': false,
        'connected': false,
        'source': 'bundled',
        'platform': 'darwin',
        'lastChecked': '',
        'error': '',
        'choices': {},
        'catalog': {'version': 1, 'services': []},
      },
      _ => null,
    },
  });
}

void main() {
  testWidgets(
    'preview retains metric data when selecting or renaming profiles',
    (tester) async {
      final state = AppState(
        _PreviewCore(),
        dryRun: true,
        routingPreview: true,
      );
      await state.start();
      expect(state.profiles.length, 3);
      expect(state.profiles.first.capacity!.rttMs, 87);
      expect(state.profiles.first.capacity!.history.length, 48);
      expect(state.profiles[1].capacity!.mbps, 74);
      expect(state.profiles.last.capacity, isNull);
      final selecting = state.switchProfile('dry-frankfurt');
      await tester.pump(const Duration(seconds: 1));
      await selecting;
      expect(state.profiles[1].active, isTrue);
      expect(state.profiles[1].autoEligible, isTrue);
      expect(state.profiles[1].capacity!.rttMs, 112);
      final renaming = state.renameProfile('dry-frankfurt', 'проверка · демо');
      await tester.pump(const Duration(seconds: 1));
      await renaming;
      expect(state.profiles[1].capacity!.mbps, 74);
      state.dispose();
      await tester.pump(const Duration(seconds: 6));
    },
  );
}
