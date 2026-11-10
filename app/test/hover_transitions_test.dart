import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/gestures.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:nekkobox/core/models.dart';
import 'package:nekkobox/core/nekkobox_core.dart';
import 'package:nekkobox/state/app_state.dart';
import 'package:nekkobox/theme/app_theme.dart';
import 'package:nekkobox/theme/tokens.dart';
import 'package:nekkobox/widgets/network_doctor_dialog.dart';
import 'package:nekkobox/widgets/power_ring.dart';
import 'package:nekkobox/widgets/power_toggle.dart';
import 'package:nekkobox/widgets/route_section.dart';
import 'package:provider/provider.dart';

class _QuietCore extends NekkoboxCore {
  @override
  String rawCall(String method, [String argsJson = '']) =>
      '{"ok":true,"data":null}';
}

class _MockCore extends NekkoboxCore {
  @override
  String rawCall(String method, [String argsJson = '']) =>
      '{"ok":true,"data":null}';

  @override
  Future<RoutingState> routing() async => const RoutingState.initial();
}

void main() {
  testWidgets('the shared hover easing is a linear crossfade', (tester) async {
    // Hover transitions are 140 ms; an ease-out curve spends almost all of
    // that change in the first two frames, which reads as a snap. The hover
    // token must stay linear — the glide the routing header's title takes.
    for (final t in const [0.1, 0.25, 0.5, 0.75, 0.9]) {
      expect(AppCurves.hover.transform(t), closeTo(t, 1e-9));
    }
  });

  testWidgets('network doctor buttons glide into their hover look', (
    tester,
  ) async {
    final tempDir = Directory.systemTemp.createTempSync('hover_test_');
    addTearDown(() {
      try {
        tempDir.deleteSync(recursive: true);
      } catch (_) {}
    });
    final state = AppState(
      _QuietCore(),
      baseDir: tempDir.path,
      routingPreview: true,
      engineId: 'sing-box',
    );
    addTearDown(state.dispose);

    await tester.pumpWidget(
      ChangeNotifierProvider<AppState>.value(
        value: state,
        child: MaterialApp(
          theme: buildAppTheme(),
          home: Scaffold(
            body: NetworkDoctorDialog(diagnosticRunner: (s) async {}),
          ),
        ),
      ),
    );
    await tester.pump();

    final label = find.text('проверить');
    expect(label, findsOneWidget);
    final buttonBox = find.ancestor(
      of: label,
      matching: find.byType(Container),
    ).first;

    Color buttonColor() {
      final c = tester.widget<Container>(buttonBox);
      final deco = c.decoration as BoxDecoration?;
      return deco?.color ?? (c.color ?? const Color(0xFFFFFFFF));
    }

    final normal = buttonColor();

    final pointer = TestPointer(1, PointerDeviceKind.mouse);
    await tester.sendEventToBinding(pointer.hover(tester.getCenter(label)));

    // The first frame after entry is still the normal look: no snap.
    await tester.pump();
    expect(buttonColor(), normal);

    // Mid-flight the fill is partway — a glide, not an instant change.
    await tester.pump(const Duration(milliseconds: 20));
    final mid = buttonColor();
    expect(mid, isNot(equals(normal)), reason: 'the hover has begun');

    await tester.pumpAndSettle();
    final hovered = buttonColor();
    expect(hovered, isNot(equals(normal)), reason: 'the hover look lands');

    // Linear easing puts 20/140 ms ≈ 1/7 of the trip behind us; easeOutExpo
    // would already be ~60% there — which is the snap this pins out.
    final progress = (mid.r - normal.r) / (hovered.r - normal.r).toDouble();
    expect(progress, greaterThan(0.03));
    expect(progress, lessThan(0.45));

    // Leaving the button glides back the same way.
    await tester.sendEventToBinding(
      pointer.hover(tester.getCenter(label) + const Offset(600, 600)),
    );
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 20));
    expect(buttonColor(), isNot(equals(hovered)), reason: 'exit glides too');
    await tester.pumpAndSettle();
    expect(buttonColor(), normal);
  });

  testWidgets('power toggle hover flag propagates into PowerRing', (
    tester,
  ) async {
    final state = AppState(_QuietCore(), dryRun: true);
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
    await tester.pump();

    final ring = find.byType(PowerRing);
    expect(ring, findsOneWidget);
    expect(tester.widget<PowerRing>(ring).hover, isFalse);

    final pointer = TestPointer(1, PointerDeviceKind.mouse);
    await tester.sendEventToBinding(pointer.hover(tester.getCenter(ring)));
    await tester.pump();
    expect(tester.widget<PowerRing>(ring).hover, isTrue);

    await tester.sendEventToBinding(
      pointer.hover(tester.getCenter(ring) + const Offset(400, 400)),
    );
    await tester.pump();
    expect(tester.widget<PowerRing>(ring).hover, isFalse);
  });

  testWidgets('preset menu buttons and catalog header glide on the hover token',
      (tester) async {
    final state = AppState(_MockCore());
    addTearDown(state.dispose);

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

    final pointer = TestPointer(1, PointerDeviceKind.mouse);

    // Linear 140 ms glide, checked channel-summed so a same-red pair of
    // token colors cannot divide by zero: first frame stays put (no snap),
    // 20 ms in the trip is ~1/7 done — an ease-out would already be ~60%
    // there — and leaving glides back the same way.
    Future<void> glide(Finder target, Color Function() color) async {
      double span(Color a, Color b) =>
          (a.r - b.r).abs() + (a.g - b.g).abs() + (a.b - b.b).abs();
      final normal = color();
      await tester.sendEventToBinding(pointer.hover(tester.getCenter(target)));
      await tester.pump();
      expect(color(), normal, reason: 'first frame keeps the normal look');
      await tester.pump(const Duration(milliseconds: 20));
      final mid = color();
      expect(mid, isNot(equals(normal)), reason: 'the hover has begun');
      await tester.pumpAndSettle();
      final hovered = color();
      expect(hovered, isNot(equals(normal)), reason: 'the hover look lands');
      final progress = span(normal, mid) / span(normal, hovered);
      expect(progress, greaterThan(0.03));
      expect(progress, lessThan(0.45));
      await tester.sendEventToBinding(
        pointer.hover(tester.getCenter(target) + const Offset(600, 600)),
      );
      await tester.pumpAndSettle();
      expect(color(), normal, reason: 'leaving glides back');
    }

    // The routing card starts collapsed (`_open = false`) and the catalog
    // header rides the reveal — open it so the header is onstage.
    await tester.tap(find.text('маршрутизация'));
    await tester.pumpAndSettle();

    // «преднастроенные» header: the label and the chevron crossfade.
    final header = find.text('преднастроенные');
    expect(header, findsOneWidget);
    await glide(header, () => tester.widget<Text>(header).style!.color!);

    // Open the preset menu: import/export pills and the reset icon button.
    await tester.tap(find.byKey(const ValueKey('routing_presets_button')));
    await tester.pumpAndSettle();

    for (final (label, key) in [
      ('импорт', 'routing_preset_import_button'),
      ('экспорт', 'routing_preset_export_button'),
    ]) {
      final pill = find.descendant(
        of: find.byKey(ValueKey(key)),
        matching: find.text(label),
      );
      await glide(pill, () => tester.widget<Text>(pill).style!.color!);
    }

    final resetIcon = find.descendant(
      of: find.byKey(const ValueKey('reset_routing_button_pill')),
      matching: find.byIcon(Icons.refresh_rounded),
    );
    expect(resetIcon, findsOneWidget);
    await glide(resetIcon, () => tester.widget<Icon>(resetIcon).color!);
  });
}
