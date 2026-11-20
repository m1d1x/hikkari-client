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

  testWidgets('the swap is a sideway reel, not a fade', (tester) async {
    // A word dissolving in place reads as a blink: at 12 px there is nothing
    // for the eye to follow. The reel pushes the words sideways instead, and
    // keeps both at full opacity the whole way.
    await _pumpCaption(tester);
    // The slot is as wide as the wider word: that is the reel's travel step.
    final slot = tester.getSize(find.byKey(const ValueKey('bypass'))).width;
    double gap() =>
        tester.getCenter(find.byKey(const ValueKey('bypass'))).dx -
        tester.getCenter(find.byKey(const ValueKey('proxy'))).dx;
    expect(gap(), greaterThan(slot), reason: 'the parked word is clear of the slot');

    await tester.tap(_pill());
    var widestGap = 0.0;
    for (var ms = 0; ms <= 200; ms += 10) {
      await tester.pump(const Duration(milliseconds: 10));
      expect(
        find.descendant(of: _pill(), matching: find.byType(FadeTransition)),
        findsNothing,
        reason: 'the word slot must not fade',
      );
      widestGap = widestGap > gap() ? widestGap : gap();
    }
    expect(
      widestGap,
      greaterThan(slot * .95),
      reason: 'the two words never travelled a full slot apart',
    );
    await tester.pumpAndSettle();
    expect(
      gap(),
      greaterThan(slot),
      reason: 'the reel comes to rest one step from the other word',
    );
  });

  testWidgets('each mode parks its word in the slot', (tester) async {
    final state = await _pumpCaption(tester); // starts in bypass
    final parked = tester.getCenter(find.byKey(const ValueKey('bypass'))).dx;
    await state.setRoutingMode(RoutingMode.proxy);
    await tester.pumpAndSettle();
    expect(
      tester.getCenter(find.byKey(const ValueKey('proxy'))).dx,
      closeTo(parked, .01),
      reason: 'proxy must land where bypass stood, not somewhere else',
    );
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