import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:nekkobox/core/models.dart';
import 'package:nekkobox/core/nekkobox_core.dart';
import 'package:nekkobox/core/routing_document.dart';
import 'package:nekkobox/state/app_state.dart';
import 'package:nekkobox/theme/app_theme.dart';
import 'package:nekkobox/widgets/route_section.dart';
import 'package:provider/provider.dart';

/// Serves the routing snapshot from memory and flips the mode on command, the
/// way the engine does — the pill must animate the real state change, not a
/// local guess.
class _ModeCore extends NekkoboxCore {
  _ModeCore(this.manual);
  RoutingState manual;

  @override
  String rawCall(String method, [String argsJson = '']) =>
      jsonEncode({'ok': true, 'data': null});

  @override
  Future<RoutingApplicationSnapshot> routingStatus() async =>
      RoutingApplicationSnapshot(
        sequence: 0,
        revision: 0,
        appliedRevision: 0,
        status: RoutingApplicationPhase.idle,
        error: '',
        appliedAt: 0,
        preview: false,
        manual: manual,
        effective: manual,
      );

  @override
  Future<void> setRoutingMode(RoutingMode mode) async =>
      manual = manual.copyWith(mode: mode);
}

/// The pill: the only animated container in the sheet that is 28 px tall.
Finder _pill() => find.byWidgetPredicate(
  (w) => w is AnimatedContainer && w.constraints?.maxHeight == 28,
);

double _currentOpacity(WidgetTester tester, String word) {
  // The slot carries both words invisibly, so only the live one has a key.
  final label = find.byKey(ValueKey(word));
  if (label.evaluate().isEmpty) return 0;
  final fades = find.ancestor(of: label, matching: find.byType(FadeTransition));
  if (fades.evaluate().isEmpty) return 1;
  return tester.widget<FadeTransition>(fades.first).opacity.value;
}

Future<AppState> _pumpCaption(WidgetTester tester) async {
  final core = _ModeCore(
    const RoutingState.initial().copyWith(mode: RoutingMode.bypass),
  );
  final state = AppState(core);
  addTearDown(state.dispose);
  await tester.pumpWidget(
    ChangeNotifierProvider<AppState>.value(
      value: state,
      child: MaterialApp(
        theme: buildAppTheme(),
        home: const Scaffold(body: RouteSettingsSection()),
      ),
    ),
  );
  await tester.pumpAndSettle();
  return state;
}

void main() {
  testWidgets('the pill keeps its width through the mode flip', (tester) async {
    // 'proxy' and 'bypass' are different lengths. A switcher that sizes to its
    // widest child makes the pill, the icon and the row's right edge jump on
    // the first frame of the swap — before any transition has even started.
    await _pumpCaption(tester);
    final width = tester.getSize(_pill()).width;
    await tester.tap(_pill());
    for (var ms = 0; ms <= 200; ms += 10) {
      await tester.pump(const Duration(milliseconds: 10));
      expect(
        tester.getSize(_pill()).width,
        closeTo(width, .01),
        reason: 'the pill resized ${ms}ms into the flip',
      );
    }
    expect(tester.getSize(_pill()).width, closeTo(width, .01));
  });

  testWidgets('the two mode words never sit on top of each other', (
    tester,
  ) async {
    // A plain cross-fade leaves 'proxy' and 'bypass' at half opacity in the
    // same spot for a few frames: smeared text, not a swap. The old word has
    // to be gone before the new one arrives.
    await _pumpCaption(tester);
    expect(find.byKey(const ValueKey('bypass')), findsOneWidget);
    await tester.tap(_pill());
    for (var ms = 0; ms <= 200; ms += 5) {
      await tester.pump(const Duration(milliseconds: 5));
      final out = _currentOpacity(tester, 'bypass');
      final into = _currentOpacity(tester, 'proxy');
      expect(
        out < .35 || into < .35,
        isTrue,
        reason:
            'at ${ms}ms both words are half-visible '
            '(outgoing $out, incoming $into)',
      );
    }
    await tester.pumpAndSettle();
    expect(_currentOpacity(tester, 'proxy'), 1);
  });

  testWidgets('the pill answers a press before the mode changes', (
    tester,
  ) async {
    // The mode flip goes through the engine, so the label lands a frame or two
    // later. A press that shows nothing until then reads as a dead button.
    await _pumpCaption(tester);
    final decoration = find
        .descendant(of: _pill(), matching: find.byType(DecoratedBox))
        .first;
    Color? fill() =>
        (tester.widget<DecoratedBox>(decoration).decoration as BoxDecoration)
            .color;
    final idle = fill();
    final gesture = await tester.startGesture(tester.getCenter(_pill()));
    // The tap recognizer reports the press once the gesture arena settles, and
    // the surface then eases into its pressed tint.
    await tester.pump(const Duration(milliseconds: 120));
    await tester.pump(const Duration(milliseconds: 60));
    expect(fill(), isNot(idle), reason: 'the press is invisible');
    expect(
      find.byKey(const ValueKey('bypass')),
      findsOneWidget,
      reason: 'the mode must not flip before the pointer is released',
    );
    await gesture.up();
    await tester.pumpAndSettle();
  });
}