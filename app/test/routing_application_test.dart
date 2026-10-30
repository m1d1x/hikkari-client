import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:nekkobox/core/models.dart';
import 'package:nekkobox/core/nekkobox_core.dart';
import 'package:nekkobox/core/routing_document.dart';
import 'package:nekkobox/state/app_state.dart';

class RoutingCore extends NekkoboxCore {
  final commands = <String>[];
  var manual = const RoutingState.initial();
  var phase = RoutingApplicationPhase.idle;
  var sequence = 0;
  var error = '';
  var restartCalls = 0;
  bool failFlush = false;
  Completer<void>? gate;

  RoutingApplicationSnapshot snapshot() => RoutingApplicationSnapshot(
    sequence: sequence,
    revision: commands.length,
    appliedRevision: phase == RoutingApplicationPhase.applied
        ? commands.length
        : 0,
    status: phase,
    error: error,
    appliedAt: DateTime.now().millisecondsSinceEpoch,
    preview: phase == RoutingApplicationPhase.preview,
    manual: manual,
    effective: manual,
  );

  @override
  String rawCall(String method, [String argsJson = '']) =>
      throw StateError('unexpected: $method');

  @override
  Future<RoutingApplicationSnapshot> routingStatus() async => snapshot();

  @override
  Future<void> addRule(RoutingMode mode, String category, String value) async {
    commands.add(value);
    if (gate != null) await gate!.future;
    manual = manual.copyWith(bypass: manual.bypass.withAdded(category, value));
    phase = RoutingApplicationPhase.pending;
    sequence++;
  }

  @override
  Future<void> restartVpnAsync() async => restartCalls++;

  @override
  Future<void> flushRouting() async {
    sequence++;
    if (failFlush) {
      phase = RoutingApplicationPhase.failed;
      error = 'Start failed; old policy restored';
      manual = const RoutingState.initial();
      throw StateError(error);
    }
    phase = RoutingApplicationPhase.applied;
  }
}

void main() {
  test(
    'concurrent commands are delivered and Flutter never restarts VPN',
    () async {
      final core = RoutingCore()..gate = Completer<void>();
      final state = AppState(core);
      addTearDown(state.dispose);
      final first = state.addRule('domains', 'one.example');
      final second = state.addRule('domains', 'two.example');
      expect(core.commands, ['one.example', 'two.example']);
      core.gate!.complete();
      await Future.wait([first, second]);
      expect(state.routing.bypass.domains, ['one.example', 'two.example']);
      expect(state.routingApply, RoutingApplyStatus.pending);
      await state.flushRouting();
      expect(state.routingApply, RoutingApplyStatus.applied);
      expect(core.restartCalls, 0);
    },
  );

  test(
    'failed Go apply restores displayed rules and never shows applied',
    () async {
      final core = RoutingCore()..failFlush = true;
      final state = AppState(core);
      addTearDown(state.dispose);
      await state.addRule('domains', 'candidate.example');
      await state.flushRouting();
      expect(state.routingApply, RoutingApplyStatus.failed);
      expect(state.routing.bypass.domains, isEmpty);
      expect(state.routingApplyError, contains('old policy restored'));
      expect(state.lastError, contains('Start failed'));
      expect(core.restartCalls, 0);
    },
  );

  test('Go preview is displayed as preview rather than applied', () async {
    final core = RoutingCore()..phase = RoutingApplicationPhase.preview;
    final state = AppState(core);
    addTearDown(state.dispose);
    await state.refreshRoutingState();
    expect(state.routingApply, RoutingApplyStatus.preview);
    expect(core.restartCalls, 0);
  });

  test('late status snapshots cannot overwrite newer state', () async {
    final core = DelayedRoutingCore();
    final state = AppState(core);
    addTearDown(state.dispose);
    final old = state.refreshRoutingState();
    core.sequence = 2;
    core.phase = RoutingApplicationPhase.failed;
    core.error = 'current failure';
    await state.refreshRoutingState();
    core.oldRead.complete(
      RoutingApplicationSnapshot(
        sequence: 1,
        revision: 1,
        appliedRevision: 1,
        status: RoutingApplicationPhase.applied,
        error: '',
        appliedAt: DateTime.now().millisecondsSinceEpoch,
        preview: false,
        manual: const RoutingState.initial(),
        effective: const RoutingState.initial(),
      ),
    );
    await old;
    expect(state.routingApply, RoutingApplyStatus.failed);
    expect(state.routingApplyError, 'current failure');
  });
}

class DelayedRoutingCore extends RoutingCore {
  final oldRead = Completer<RoutingApplicationSnapshot>();
  var reads = 0;
  @override
  Future<RoutingApplicationSnapshot> routingStatus() =>
      reads++ == 0 ? oldRead.future : super.routingStatus();
}
