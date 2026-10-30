import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:nekkobox/core/nekkobox_core.dart';
import 'package:nekkobox/state/app_state.dart';

class SwitchingCore extends NekkoboxCore {
  final calls = <String>[];
  String? validationError;

  @override
  String rawCall(String method, [String argsJson = '']) {
    calls.add(method);
    final data = switch (method) {
      'RoutingStatus' => {
        'sequence': 1,
        'revision': 0,
        'appliedRevision': 0,
        'status': 'idle',
        'error': '',
        'appliedAt': 0,
        'preview': true,
        'manual': {'enabled': true, 'mode': 'bypass'},
        'effective': {'enabled': true, 'mode': 'bypass'},
      },
      'RoutingPresets' => {
        'syncEnabled': true,
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
    };
    if (method == 'ValidateConfiguration' && validationError != null) {
      return jsonEncode({'ok': false, 'error': validationError});
    }
    return jsonEncode({'ok': true, 'data': data});
  }

  @override
  void init(
    String baseDir, {
    String engineId = '',
    bool routingPreview = false,
  }) {
    calls.add('Init:$engineId');
  }

  @override
  void shutdown() => calls.add('Shutdown');
}

void main() {
  test(
    'engine choice persists and switch automates stop, shutdown, init, reload',
    () async {
      final dir = await Directory.systemTemp.createTemp(
        'hikkari-engine-switch-',
      );
      addTearDown(() => dir.delete(recursive: true));
      final core = SwitchingCore();
      final state = AppState(core, baseDir: dir.path, engineId: 'sing-box');

      await state.switchEngine('xray');

      expect(
        core.calls,
        containsAllInOrder([
          'StopVPN',
          'Shutdown',
          'Init:xray',
          'ValidateConfiguration',
          'RoutingPresets',
        ]),
      );
      expect(state.engineId, 'xray');
      expect(
        jsonDecode(
          await File('${dir.path}/engine-selection.json').readAsString(),
        ),
        {'engineId': 'xray'},
      );
      state.dispose();
    },
  );

  test('unsupported policy stays selected and shows a clear error', () async {
    final dir = await Directory.systemTemp.createTemp('hikkari-engine-policy-');
    addTearDown(() => dir.delete(recursive: true));
    final core = SwitchingCore()
      ..validationError = 'исключения приложений не поддерживаются';
    final state = AppState(core, baseDir: dir.path, engineId: 'sing-box');

    await state.switchEngine('xray');

    expect(state.engineId, 'xray');
    expect(state.lastError, contains('не поддерживает текущую политику'));
    expect(
      jsonDecode(
        await File('${dir.path}/engine-selection.json').readAsString(),
      ),
      {'engineId': 'xray'},
    );
    state.dispose();
  });
}
