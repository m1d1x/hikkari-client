import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:ui' show ImageByteFormat;

import 'package:flutter/material.dart';
import 'package:flutter/gestures.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:nekkobox/core/nekkobox_core.dart';
import 'package:nekkobox/core/app_discovery.dart';
import 'package:nekkobox/state/app_state.dart';
import 'package:nekkobox/screens/home_screen.dart';
import 'package:nekkobox/theme/app_theme.dart';
import 'package:nekkobox/widgets/service_presets.dart';
import 'package:nekkobox/widgets/route_section.dart';
import 'package:nekkobox/ui/nekko_switch.dart';
import 'package:nekkobox/ui/nekko_animated_height.dart';
import 'package:nekkobox/widgets/service_icon.dart';
import 'package:nekkobox/theme/tokens.dart';
import 'package:provider/provider.dart';

/// Longest routing reveal, including the spring's sub-pixel tail.
const _revealSettles = Duration(milliseconds: 1500);

class PresetCore extends NekkoboxCore {
  final List<Map<String, dynamic>>? customServices;
  PresetCore({this.customServices});

  bool sync = true;
  bool? selected;
  int listAppsCalls = 0;

  @override
  String rawCall(String method, [String argsJson = '']) {
    final args = argsJson.isEmpty
        ? <String, dynamic>{}
        : jsonDecode(argsJson) as Map<String, dynamic>;
    switch (method) {
      case 'SetPresetSync':
        sync = args['on'] as bool;
      case 'SetServicePreset':
        selected = args['on'] as bool;
      case 'ListApps':
        listAppsCalls++;
        return jsonEncode({'ok': true, 'data': []});
    }
    return jsonEncode({
      'ok': true,
      'data': {
        'syncEnabled': sync,
        'configured': false,
        'connected': false,
        'source': 'bundled',
        'platform': 'windows',
        'lastChecked': '',
        'error': '',
        'choices': {
          'bypass': {if (selected != null) 'steam': selected},
        },
        'catalog': {
          'version': 2,
          'services':
              customServices ??
              [
                {
                  'id': 'steam',
                  'name': 'Steam',
                  'icon': 'steam',
                  'domains': ['steampowered.com', 'steamcommunity.com'],
                  'ips': [],
                  'processes': {
                    'windows': ['steam.exe', 'steamwebhelper.exe'],
                    'darwin': ['steam_osx'],
                  },
                  'defaultModes': ['bypass', 'proxy'],
                },
              ],
        },
      },
    });
  }
}

Future<AppState> showPresets(
  WidgetTester tester,
  PresetCore core, {
  bool settings = false,
}) async {
  final state = AppState(core);
  await state.refreshPresetState();
  await tester.pumpWidget(
    ChangeNotifierProvider.value(
      value: state,
      child: MaterialApp(
        theme: buildAppTheme(),
        home: Scaffold(
          body: Padding(
            padding: const EdgeInsets.all(16),
            child: settings
                ? const PresetSettingsSection()
                : const ServicePresets(),
          ),
        ),
      ),
    ),
  );
  return state;
}

void main() {
  setUpAll(() async {
    final text = FontLoader('IBM Plex Mono')
      ..addFont(rootBundle.load('assets/fonts/IBMPlexMono-Regular.ttf'))
      ..addFont(rootBundle.load('assets/fonts/IBMPlexMono-Medium.ttf'))
      ..addFont(rootBundle.load('assets/fonts/IBMPlexMono-SemiBold.ttf'))
      ..addFont(rootBundle.load('assets/fonts/IBMPlexMono-Bold.ttf'));
    final icons = FontLoader('MaterialIcons')
      ..addFont(rootBundle.load('fonts/MaterialIcons-Regular.otf'));
    final brands = FontLoader('packages/simple_icons/SimpleIcons')
      ..addFont(rootBundle.load('packages/simple_icons/fonts/SimpleIcons.ttf'));
    await Future.wait([text.load(), icons.load(), brands.load()]);
  });
  for (final (windowHeight, serviceCount, staged) in [
    (450.0, 1, true),
    (650.0, 1, false),
    (1100.0, 1, false),
    (1100.0, 16, false),
  ]) {
    testWidgets(
      'routing adapts at height $windowHeight with $serviceCount services',
      (tester) async {
        tester.view.physicalSize = Size(420, windowHeight);
        tester.view.devicePixelRatio = 1;
        addTearDown(tester.view.resetPhysicalSize);
        addTearDown(tester.view.resetDevicePixelRatio);
        final state = AppState(
          PresetCore(
            customServices: [
              for (var i = 0; i < serviceCount; i++)
                {
                  'id': 'service-$i',
                  'name': 'Service $i',
                  'icon': 'steam',
                  'domains': ['example$i.com'],
                  'ips': [],
                  'processes': <String, dynamic>{},
                  'defaultModes': ['bypass', 'proxy'],
                },
            ],
          ),
        );
        addTearDown(state.dispose);
        await state.refreshPresetState();
        await tester.pumpWidget(
          ChangeNotifierProvider.value(
            value: state,
            child: MaterialApp(
              theme: buildAppTheme(),
              home: const HomeScreen(),
            ),
          ),
        );
        await tester.pumpAndSettle();
        final header = find.text('маршрутизация');
        final section = find.byType(RouteSection);
        final manual = find.byKey(
          const ValueKey('route_manual_surface'),
          skipOffstage: false,
        );
        double height() => tester.getSize(section).height;
        await tester.tap(header);
        await tester.pump();
        await tester.pump(const Duration(milliseconds: 100));
        expect(
          tester.getSize(manual).height,
          staged ? 0 : greaterThan(0),
          reason: staged
              ? 'lists below a viewport filled by the controls stay hidden'
              : 'lists that fit on screen grow together with the controls',
        );
        final partial = height();
        await tester.tap(header);
        await tester.pump();
        expect(height(), closeTo(partial, .01));
        await tester.pumpAndSettle();
        await tester.tap(header);
        await tester.pump();
        await tester.pump(_revealSettles);
        final finalHeight = height();
        await tester.pumpAndSettle();
        expect(
          height(),
          closeTo(finalHeight, .01),
          reason: 'opening finishes within the selected duration',
        );
        expect(tester.takeException(), isNull);
        await tester.pumpWidget(const SizedBox());
      },
    );
  }

  for (final windowHeight in [800.0, 1100.0, 1400.0]) {
    testWidgets(
      'routing header rises at a bounded speed in a $windowHeight window',
      (tester) async {
        tester.view.physicalSize = Size(480, windowHeight);
        tester.view.devicePixelRatio = 1;
        addTearDown(tester.view.resetPhysicalSize);
        addTearDown(tester.view.resetDevicePixelRatio);
        final state = AppState(
          PresetCore(
            customServices: [
              for (var i = 0; i < 100; i++)
                {
                  'id': 'service-$i',
                  'name': 'Service $i',
                  'icon': 'steam',
                  'domains': ['example$i.com'],
                  'ips': [],
                  'processes': <String, dynamic>{},
                  'defaultModes': ['bypass', 'proxy'],
                },
            ],
          ),
        );
        addTearDown(state.dispose);
        await state.refreshPresetState();
        await tester.pumpWidget(
          ChangeNotifierProvider.value(
            value: state,
            child: MaterialApp(
              theme: buildAppTheme(),
              home: const HomeScreen(),
            ),
          ),
        );
        await tester.pumpAndSettle();
        final header = find.text('маршрутизация');
        final start = tester.getTopLeft(header).dy;
        await tester.tap(header);
        await tester.pump();
        var previous = start;
        var peak = 0.0;
        var elapsed = Duration.zero;
        while (elapsed <= _revealSettles) {
          await tester.pump(const Duration(milliseconds: 16));
          elapsed += const Duration(milliseconds: 16);
          final y = tester.getTopLeft(header).dy;
          if ((previous - y) > peak) peak = previous - y;
          previous = y;
        }
        await tester.pumpAndSettle();
        expect(tester.getTopLeft(header).dy, closeTo(previous, .01));
        // A hundred services must not set the pace: only the on-screen path
        // does, and a tall window gets more time instead of a faster header.
        expect(
          peak,
          lessThan(90),
          reason: 'rose ${start - previous}px, peak ${peak}px per 16ms frame',
        );
        expect(tester.takeException(), isNull);
        await tester.pumpWidget(const SizedBox());
      },
    );
  }

  testWidgets('expanded routing fits a small desktop window', (tester) async {
    tester.view.physicalSize = const Size(420, 650);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(
          const MethodChannel('window_manager'),
          (_) async => false,
        );
    final core = PendingAppsCore();
    final state = AppState(core);
    await state.refreshPresetState();
    await tester.pumpWidget(
      ChangeNotifierProvider.value(
        value: state,
        child: MaterialApp(
          theme: buildAppTheme(),
          home: const RepaintBoundary(
            key: ValueKey('preview'),
            child: HomeScreen(),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.text('маршрутизация'));
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    expect(find.text('Обновлять пресеты'), findsNothing);
    expect(find.text('автообновление списков'), findsNothing);
    expect(find.text('преднастроенные'), findsOneWidget);
    expect(find.text('ручные'), findsOneWidget);
    expect(find.text('steam'), findsOneWidget);
    expect(core.listAppsCalls, 0);
    await tester.ensureVisible(find.text('steam'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('steam'), buttons: kSecondaryMouseButton);
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    expect(find.text('steampowered.com\nsteamcommunity.com'), findsOneWidget);
    expect(
      find.byType(EditableText),
      findsNWidgets(2),
    ); // manual input + preset search
    expect(core.pending.isCompleted, isFalse);
    core.pending.complete([]);
    expect(
      find.byWidgetPredicate(
        (widget) =>
            widget is Scrollable &&
            axisDirectionToAxis(widget.axisDirection) == Axis.vertical,
      ),
      findsOneWidget,
    );
    const preview = String.fromEnvironment('PRESET_PREVIEW_PATH');
    if (preview.isNotEmpty) {
      final boundary = tester.renderObject<RenderRepaintBoundary>(
        find.byKey(const ValueKey('preview')),
      );
      await tester.runAsync(() async {
        final image = await boundary.toImage();
        final bytes = await image.toByteData(format: ImageByteFormat.png);
        await File(preview).writeAsBytes(bytes!.buffer.asUint8List());
        image.dispose();
      });
    }
    await tester.tap(find.byIcon(Icons.settings_outlined));
    await tester.pumpAndSettle();
    expect(find.text('автообновление списков'), findsOneWidget);
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox());
    state.dispose();
  });
  testWidgets('routing moves continuously through reversal, settling and '
      'a scrolled collapse', (tester) async {
    tester.view.physicalSize = const Size(420, 650);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final core = PresetCore(
      customServices: [
        for (var i = 0; i < 40; i++)
          {
            'id': 'service-$i',
            'name': 'Service $i',
            'icon': 'steam',
            'domains': ['example$i.com'],
            'ips': [],
            'processes': <String, dynamic>{},
            'defaultModes': ['bypass', 'proxy'],
          },
      ],
    );
    final state = AppState(core);
    await state.refreshPresetState();
    await tester.pumpWidget(
      ChangeNotifierProvider.value(
        value: state,
        child: MaterialApp(theme: buildAppTheme(), home: const HomeScreen()),
      ),
    );
    await tester.pumpAndSettle();
    final header = find.text('маршрутизация');
    final scroll = tester
        .state<ScrollableState>(find.byType(Scrollable).first)
        .position;
    final row = find.byKey(
      const ValueKey('preset_surface_service-0'),
      skipOffstage: false,
    );
    final rowWidth = tester.getSize(row).width;
    final surfaces = [
      find.byKey(const ValueKey('route_manual_surface'), skipOffstage: false),
      find.byKey(const ValueKey('route_presets_surface'), skipOffstage: false),
    ];
    double y() => tester.getTopLeft(header).dy;
    List<double> surfaceHeights() =>
        surfaces.map((f) => tester.getSize(f).height).toList();

    // Pumps until the reveal settles and checks every frame on the way.
    Future<void> run({
      required bool opening,
      int? frames,
      bool reversed = false,
    }) async {
      var previous = y();
      var previousSurfaces = surfaceHeights();
      final viewportBottom = tester
          .getRect(find.byType(Scrollable).first)
          .bottom;
      // Space between the window edge and the catalog card, while it shows.
      double? edgeGap() => tester.getSize(surfaces.last).height == 0
          ? null
          : viewportBottom - tester.getRect(surfaces.last).bottom;
      var previousGap = edgeGap();
      final limit = frames ?? _revealSettles.inMilliseconds ~/ 16;
      for (var i = 0; i < limit; i++) {
        await tester.pump(const Duration(milliseconds: 16));
        final current = y();
        // Right after a reversal the spring may carry on briefly with the
        // velocity it had; after that it heads to the target only.
        if (!reversed || i > 6) {
          expect(
            opening ? current <= previous + .01 : current >= previous - .01,
            isTrue,
            reason: 'frame $i: header $previous -> $current',
          );
        }
        expect(
          (current - previous).abs(),
          lessThan(50),
          reason: 'frame $i: header must glide, not jump',
        );
        final currentSurfaces = surfaceHeights();
        if (frames == null && !reversed) {
          for (var j = 0; j < surfaces.length; j++) {
            expect(
              opening
                  ? currentSurfaces[j] >= previousSurfaces[j] - .01
                  : currentSurfaces[j] <= previousSurfaces[j] + .01,
              isTrue,
              reason: 'frame $i: each rounded surface follows its contents',
            );
          }
        }
        expect(
          scroll.pixels,
          inInclusiveRange(0, scroll.maxScrollExtent + .01),
          reason: 'frame $i: the viewport never shows out-of-range content',
        );
        expect(
          tester.getSize(row).width,
          closeTo(rowWidth, .001),
          reason: 'revealing vertically must not change text layout width',
        );
        final gap = edgeGap();
        if (opening && !reversed && gap != null && previousGap != null) {
          if (gap < -1) {
            expect(
              previousGap,
              lessThan(.5),
              reason:
                  'frame $i: the cut card reaches the window edge before the '
                  'rest of the list appears below it',
            );
          } else {
            expect(
              gap,
              lessThanOrEqualTo(previousGap + .01),
              reason: 'frame $i: the strip under the card only closes',
            );
          }
        }
        expect(tester.takeException(), isNull, reason: 'frame $i');
        previous = current;
        previousSurfaces = currentSurfaces;
        previousGap = gap;
      }
    }

    final closedY = y();
    await tester.tap(header);
    await tester.pump();
    expect(y(), closeTo(closedY, .01));
    await run(opening: true);
    final openY = y();
    expect(openY, lessThan(closedY - 100));
    await tester.pumpAndSettle();
    expect(
      y(),
      closeTo(openY, .01),
      reason: 'the list below the fold appears without moving the header',
    );
    expect(
      scroll.maxScrollExtent,
      greaterThan(0),
      reason: 'a long catalog continues below the fold once settled',
    );
    expect(core.listAppsCalls, 0);

    // Reversal keeps the current position and turns around smoothly.
    await tester.tap(header);
    await tester.pump();
    expect(y(), closeTo(openY, .01));
    await run(opening: false, frames: 8);
    final interrupted = y();
    expect(interrupted, greaterThan(openY + 20));
    await tester.tap(header);
    await tester.pump();
    expect(y(), closeTo(interrupted, .01));
    await run(opening: true, reversed: true);
    await tester.pumpAndSettle();
    expect(y(), closeTo(openY, .01));

    // Collapsing a page scrolled into the catalog: content and offset shrink
    // together until the card is back at the bottom.
    scroll.jumpTo(80);
    await tester.pump();
    final scrolledY = y();
    expect(scrolledY, closeTo(openY - 80, .01));
    await tester.tap(header);
    await tester.pump();
    expect(y(), closeTo(scrolledY, .01));
    await run(opening: false);
    await tester.pumpAndSettle();
    expect(y(), closeTo(closedY, .01));
    expect(scroll.pixels, 0);
    await tester.pumpWidget(const SizedBox());
    state.dispose();
  });

  testWidgets(
    'tab height animates without jumps, including reversal and parent collapse',
    (tester) async {
      tester.view.physicalSize = const Size(420, 650);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final state = AppState(PresetCore());
      await state.refreshPresetState();
      await tester.pumpWidget(
        ChangeNotifierProvider.value(
          value: state,
          child: MaterialApp(theme: buildAppTheme(), home: const HomeScreen()),
        ),
      );
      await tester.pumpAndSettle();
      final section = find.byType(RouteSection);
      double height() => tester.getSize(section).height;
      await tester.tap(find.text('маршрутизация'));
      await tester.pumpAndSettle();
      final domainsHeight = height();
      final top = tester.getTopLeft(section).dy;
      await tester.tap(find.text('ip'));
      await tester.pump();
      expect(
        height(),
        closeTo(domainsHeight, .01),
        reason: 'first frame keeps the visible height',
      );
      await tester.pump(const Duration(milliseconds: 64));
      final midway = height();
      expect(midway, lessThan(domainsHeight));
      await tester.tap(find.text('приложения'));
      await tester.pump();
      expect(
        height(),
        closeTo(midway, .01),
        reason: 'retarget from the current height',
      );
      var previous = height();
      for (var i = 0; i < 30; i++) {
        await tester.pump(const Duration(milliseconds: 16));
        expect(height(), greaterThanOrEqualTo(previous - .01));
        expect(tester.getTopLeft(section).dy, closeTo(top, .01));
        expect(tester.takeException(), isNull);
        previous = height();
      }
      expect(find.text('2 файла'), findsOneWidget);
      await tester.tap(find.text('ip'));
      await tester.pumpAndSettle();
      expect(height(), lessThan(domainsHeight));
      expect(
        find.text('нет готовых правил для этой категории'),
        findsOneWidget,
      );
      await tester.tap(find.text('домены'));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 64));
      // Closing drops what is below the fold at once, down to the page's
      // bottom padding strip at the edge; the card itself stays put.
      double visibleHeight() => tester
          .getRect(section)
          .intersect(tester.getRect(find.byType(Scrollable).first))
          .height;
      final cardTop = tester.getTopLeft(section).dy;
      final beforeClose = visibleHeight();
      await tester.tap(find.text('маршрутизация'));
      await tester.pump();
      expect(tester.getTopLeft(section).dy, closeTo(cardTop, .01));
      expect(
        visibleHeight(),
        inInclusiveRange(beforeClose - 16.01, beforeClose),
      );
      await tester.pumpAndSettle();
      expect(tester.takeException(), isNull);
      await tester.tap(find.text('маршрутизация'));
      await tester.pumpAndSettle();
      expect(height(), closeTo(domainsHeight, .01));
      // A nested group's own animation must not be chased by the tab viewport.
      await tester.tap(
        find.text('steam'),
        buttons: kSecondaryMouseButton,
        warnIfMissed: false,
      );
      await tester.pump();
      expect(height(), closeTo(domainsHeight, .01));
      await tester.pump(AppDurations.routingDetails * .5);
      final groupMidpoint = height();
      await tester.pumpAndSettle();
      final expandedHeight = height();
      expect(
        groupMidpoint,
        closeTo(
          domainsHeight +
              (expandedHeight - domainsHeight) *
                  AppCurves.fastInSlowOut.transform(.5),
          .01,
        ),
      );
      await tester.tap(find.text('ip'));
      await tester.pump();
      expect(height(), closeTo(expandedHeight, .01));
      await tester.pumpAndSettle();
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox());
      state.dispose();
    },
  );

  testWidgets('tab viewport keeps its heading attached to the card top', (
    tester,
  ) async {
    var tall = true;
    late StateSetter update;
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: StatefulBuilder(
            builder: (context, setState) {
              update = setState;
              return Align(
                alignment: Alignment.bottomCenter,
                child: SizedBox(
                  width: 400,
                  child: NekkoAnimatedHeight(
                    key: const ValueKey('heading_viewport'),
                    contentKey: tall,
                    duration: AppDurations.routingDetails,
                    curve: AppCurves.fastInSlowOut,
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        const Text('ручные'),
                        SizedBox(height: tall ? 200 : 40),
                      ],
                    ),
                  ),
                ),
              );
            },
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    final viewport = find.byKey(const ValueKey('heading_viewport'));
    double inset() =>
        tester.getTopLeft(find.text('ручные')).dy -
        tester.getTopLeft(viewport).dy;
    final initialInset = inset();
    update(() => tall = false);
    await tester.pump();
    expect(
      inset(),
      closeTo(initialInset, .01),
      reason: 'heading must not jump on the first frame',
    );
    for (var i = 0; i < 6; i++) {
      await tester.pump(const Duration(milliseconds: 16));
      expect(inset(), closeTo(initialInset, .01));
    }
    update(() => tall = true);
    await tester.pump();
    expect(inset(), closeTo(initialInset, .01));
    await tester.pumpAndSettle();
    expect(inset(), closeTo(initialInset, .01));
    expect(tester.takeException(), isNull);
  });

  testWidgets(
    'outside click does not collapse routing, tapping header collapses',
    (tester) async {
      var outsideClicks = 0;
      final state = AppState(PresetCore());
      await state.refreshPresetState();
      await tester.pumpWidget(
        ChangeNotifierProvider.value(
          value: state,
          child: MaterialApp(
            theme: buildAppTheme(),
            home: Scaffold(
              body: Column(
                children: [
                  TextButton(
                    onPressed: () => outsideClicks++,
                    child: const Text('снаружи'),
                  ),
                  const Expanded(
                    child: SingleChildScrollView(child: RouteSection()),
                  ),
                ],
              ),
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();
      final section = find.byType(RouteSection);
      double height() => tester.getSize(section).height;
      final closed = height();
      await tester.tap(find.text('маршрутизация'));
      await tester.pumpAndSettle();
      final opened = height();
      await tester.tap(find.text('приложения'));
      await tester.pumpAndSettle();
      expect(height(), greaterThan(closed));
      await tester.tap(find.text('домены'));
      await tester.pumpAndSettle();
      await tester.enterText(
        find.byType(EditableText).first,
        'draft.example.com',
      );
      expect(height(), closeTo(opened, .01));
      await tester.tap(find.text('steam'), buttons: kSecondaryMouseButton);
      await tester.pumpAndSettle();
      expect(height(), greaterThan(opened));
      await tester.tap(find.text('снаружи'));
      await tester.pumpAndSettle();
      expect(height(), greaterThan(closed));
      expect(outsideClicks, 1);
      expect(state.routing.enabled, isTrue);
      await tester.tap(find.text('маршрутизация'));
      await tester.pumpAndSettle();
      expect(height(), closeTo(closed, .01));
      await tester.tap(find.text('маршрутизация'));
      await tester.pumpAndSettle();
      expect(
        tester
            .widget<EditableText>(find.byType(EditableText).first)
            .controller
            .text,
        'draft.example.com',
      );
      await tester.tap(find.text('приложения'));
      await tester.pumpAndSettle();
      final beforeDialog = height();
      var dialogClicks = 0;
      final dialog = showDialog<void>(
        context: tester.element(section),
        builder: (_) => AlertDialog(
          content: TextButton(
            onPressed: () => dialogClicks++,
            child: const Text('внутри окна'),
          ),
        ),
      );
      await tester.pumpAndSettle();
      await tester.tap(find.text('внутри окна'));
      await tester.pumpAndSettle();
      expect(dialogClicks, 1);
      expect(
        height(),
        closeTo(beforeDialog, .01),
        reason: 'a modal internal workflow must not collapse routing',
      );
      tester.state<NavigatorState>(find.byType(Navigator).first).pop();
      await dialog;
      await tester.pumpAndSettle();
      expect(height(), closeTo(beforeDialog, .01));
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox());
      state.dispose();
    },
  );

  testWidgets('ready-made groups select without changing manual rules', (
    tester,
  ) async {
    final core = PresetCore();
    final state = await showPresets(tester, core);
    final surface = find.byKey(const ValueKey('preset_surface_steam'));
    final initialHeight = tester.getSize(surface).height;
    expect(find.text('автообновление списков'), findsNothing);
    expect(find.byType(NekkoSwitch), findsNothing);
    expect(
      tester.widget<ServiceIconBadge>(find.byType(ServiceIconBadge)).color,
      const Color(0xFFC7D0D8),
    );
    await tester.tap(find.text('steam'));
    await tester.pumpAndSettle();
    expect(core.selected, isFalse);
    expect(tester.getSize(surface).height, closeTo(initialHeight, .01));
    expect(core.sync, isTrue);
    expect(
      tester.widget<ServiceIconBadge>(find.byType(ServiceIconBadge)).color,
      NekkoTokens.dark.textFaint,
    );
    expect(find.text('steampowered.com\nsteamcommunity.com'), findsNothing);
    await tester.tap(find.text('steam'));
    await tester.pumpAndSettle();
    expect(core.selected, isTrue);
    expect(tester.getSize(surface).height, closeTo(initialHeight, .01));
    await tester.pumpWidget(const SizedBox());
    state.dispose();
  });

  testWidgets('sync preference lives in settings and keeps selected groups', (
    tester,
  ) async {
    final core = PresetCore()..selected = true;
    final state = await showPresets(tester, core, settings: true);
    expect(find.text('автообновление списков'), findsOneWidget);
    expect(tester.widget<NekkoSwitch>(find.byType(NekkoSwitch)).value, isTrue);
    await tester.tap(find.byType(NekkoSwitch));
    await tester.pumpAndSettle();
    expect(core.sync, isFalse);
    expect(core.selected, isTrue);
    expect(find.textContaining('встроенный список'), findsNothing);
    await tester.pumpWidget(const SizedBox());
    state.dispose();
  });

  testWidgets('group expands its rules without selecting them', (tester) async {
    final core = PresetCore();
    final state = await showPresets(tester, core);
    await tester.tap(find.text('steam'), buttons: kSecondaryMouseButton);
    await tester.pumpAndSettle();
    expect(find.text('steampowered.com\nsteamcommunity.com'), findsOneWidget);
    expect(core.selected, isNull);
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox());
    state.dispose();
  });

  testWidgets(
    'preset details decelerate both ways and retain content on reversal',
    (tester) async {
      final state = await showPresets(tester, PresetCore());
      final panel = find.byKey(const ValueKey('preset_surface_steam'));
      final action = find.text('steam');
      final closed = tester.getSize(panel).height;
      await tester.tap(action, buttons: kSecondaryMouseButton);
      await tester.pumpAndSettle();
      final opened = tester.getSize(panel).height;
      expect(opened, greaterThan(closed));
      await tester.tap(action, buttons: kSecondaryMouseButton);
      await tester.pump();
      expect(tester.getSize(panel).height, closeTo(opened, .01));
      await tester.pump(AppDurations.routingDetails * (40 / 220));
      final interrupted = tester.getSize(panel).height;
      expect(opened - interrupted, greaterThan((opened - closed) / 2));
      expect(find.text('steampowered.com\nsteamcommunity.com'), findsOneWidget);
      await tester.tap(action, buttons: kSecondaryMouseButton);
      await tester.pump();
      expect(tester.getSize(panel).height, closeTo(interrupted, .01));
      await tester.pumpAndSettle();
      expect(tester.getSize(panel).height, closeTo(opened, .01));
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox());
      state.dispose();
    },
  );

  testWidgets('apps tab shows only the current platform rules', (tester) async {
    final state = AppState(PresetCore());
    await state.refreshPresetState();
    await tester.pumpWidget(
      ChangeNotifierProvider.value(
        value: state,
        child: MaterialApp(
          theme: buildAppTheme(),
          home: const Scaffold(body: ServicePresets(category: 'processes')),
        ),
      ),
    );
    expect(find.text('2 файла'), findsOneWidget);
    await tester.tap(find.text('steam'), buttons: kSecondaryMouseButton);
    await tester.pumpAndSettle();
    expect(find.text('steam.exe\nsteamwebhelper.exe'), findsOneWidget);
    expect(find.textContaining('steam_osx'), findsNothing);
    await tester.pumpWidget(const SizedBox());
    state.dispose();
  });
  testWidgets('service hover shows the next action and keeps clicks working', (
    tester,
  ) async {
    final core = PresetCore();
    final state = await showPresets(tester, core);
    final container = find.byKey(const ValueKey('preset_surface_steam'));
    final before = tester.widget<AnimatedContainer>(container).decoration;
    final mouse = await tester.createGesture(kind: PointerDeviceKind.mouse);
    await mouse.addPointer(location: Offset.zero);
    await mouse.moveTo(tester.getCenter(find.text('steam')));
    await tester.pump();
    final hovered = tester.widget<AnimatedContainer>(container);
    expect(hovered.duration, AppDurations.fast);
    expect(hovered.decoration, isNot(before));
    expect(
      tester
          .widget<InkWell>(find.byKey(const ValueKey('preset_action_steam')))
          .mouseCursor,
      SystemMouseCursors.click,
    );
    expect(
      tester
          .widget<AnimatedOpacity>(
            find.byKey(const ValueKey('preset_hover_steam')),
          )
          .opacity,
      1,
    );
    expect(
      tester
          .widget<Text>(find.byKey(const ValueKey('preset_hover_action_steam')))
          .data,
      'отключить',
    );
    await tester.tap(find.text('steam'));
    await tester.pumpAndSettle();
    expect(core.selected, isFalse);
    expect(
      tester
          .widget<Text>(find.byKey(const ValueKey('preset_hover_action_steam')))
          .data,
      'включить',
    );
    await tester.tap(find.text('steam'), buttons: kSecondaryMouseButton);
    await tester.pumpAndSettle();
    expect(find.text('steampowered.com\nsteamcommunity.com'), findsOneWidget);
    expect(core.selected, isFalse);
    await mouse.removePointer();
    await tester.pumpWidget(const SizedBox());
    state.dispose();
  });
  testWidgets('long press opens composition without changing selection', (
    tester,
  ) async {
    final core = PresetCore();
    final state = await showPresets(tester, core);
    await tester.longPress(find.text('steam'));
    await tester.pumpAndSettle();
    expect(find.text('steampowered.com\nsteamcommunity.com'), findsOneWidget);
    expect(core.selected, isNull);
    await tester.pumpWidget(const SizedBox());
    state.dispose();
  });

  test(
    'discovery shares in-flight decoding and retries after failures',
    () async {
      final core = DiscoveryCore();
      final first = core.listApps();
      final second = core.listApps();
      expect(identical(first, second), isTrue);
      core.reply.complete(
        jsonEncode({
          'ok': true,
          'data': [
            {
              'name': 'Example',
              'exe': 'example',
              'path': '/example',
              'icon': base64Encode([1, 2, 3]),
            },
          ],
        }),
      );
      final apps = await first;
      expect(core.calls, 1);
      expect(apps.single.iconBytes, [1, 2, 3]);
      core.reply = Completer<String>();
      final failed = core.listApps();
      final check = expectLater(failed, throwsA(isA<Exception>()));
      core.reply.complete('{"ok":false,"error":"unavailable"}');
      await check;
      core.reply = Completer<String>();
      final retried = core.listApps();
      core.reply.complete('{"ok":true,"data":[]}');
      expect(await retried, isEmpty);
      expect(core.calls, 3);
    },
  );

  test('resolveServiceIcon maps every catalog icon to a dedicated glyph', () {
    const fallback = Color(0xFF010203);
    for (final id in [
      'ru',
      'yandex',
      'yandex-music',
      'yandex-taxi',
      'yandex-maps',
      'yandex-disk',
      'yandex-market',
      'vk',
      'vk-video',
      'vk-play',
      'mailru',
      'kinopoisk',
      'ivi',
      'okko',
      'wink',
      'premier',
      'start',
      'kion',
      'smotrim',
      'streaming-ru',
      'transport-ru',
      'rzd',
      'aeroflot',
      's7',
      'pobeda',
      'aviasales',
      'rutube',
      'banks',
      'sber',
      'tbank',
      'vtb',
      'alfa',
      'gazprombank',
      'raiffeisen',
      'sovcombank',
      'tochka',
      'psb',
      'yoomoney',
      'mir',
      'gosuslugi',
      'marketplaces',
      'ozon',
      'wildberries',
      'avito',
      'megamarket',
      'lamoda',
      'dns',
      'citilink',
      'mvideo',
      'samokat',
      'kuper',
      'cian',
      'hh',
      'mts',
      'megafon',
      'beeline',
      't2',
      'rostelecom',
      'ok',
      'habr',
      'pikabu',
      'vc',
      'dtf',
      'rbc',
      'kommersant',
      'lenta',
      'ria',
      'tass',
      'kaspersky',
      'selectel',
      'timeweb',
      'regru',
      '1c',
      'bitrix24',
      'kontur',
      'sbis',
      '2gis',
      'steam',
      'faceit',
      'discord',
      'telegram',
      'youtube',
      'openai',
      'claude',
      'gemini',
      'instagram',
      'x',
      'spotify',
      'twitch',
      'notion',
      'figma',
      'github',
      'google',
    ]) {
      final (icon, color) = resolveServiceIcon(id, fallback);
      expect(icon, isNot(Icons.apps), reason: 'icon $id fell back to generic');
      expect(color, isNot(fallback), reason: 'icon $id used fallback color');
    }
  });

  test('resolveBrandSvg resolves authentic vector SVG for brands', () {
    for (final id in [
      'yandex',
      'yandex-music',
      'yandex-taxi',
      'yandex-maps',
      'yandex-disk',
      'yandex-market',
      'sber',
      'tbank',
      'vtb',
      'alfa',
      'ozon',
      'wildberries',
      'avito',
      'gosuslugi',
      'vk',
      'openai',
      'steam',
      'youtube',
      'telegram',
      'discord',
      'github',
    ]) {
      final svg = resolveBrandSvg(id);
      expect(svg, isNotNull, reason: 'brand $id must have authentic SVG');
      expect(
        svg,
        contains('<svg'),
        reason: 'brand $id SVG is missing <svg tag',
      );
    }
  });

  testWidgets(
    'preset search filters services by name, id, domains and aliases',
    (tester) async {
      final core = PresetCore(
        customServices: [
          {
            'id': 'steam',
            'name': 'Steam',
            'icon': 'steam',
            'domains': ['steampowered.com', 'steamcommunity.com'],
            'ips': [],
            'processes': {},
            'defaultModes': ['bypass', 'proxy'],
          },
          {
            'id': 'discord',
            'name': 'Discord',
            'icon': 'discord',
            'domains': ['discord.gg', 'discord.com'],
            'ips': [],
            'processes': {},
            'defaultModes': ['proxy'],
          },
          {
            'id': 'banks',
            'name': 'Банки РФ',
            'icon': 'banks',
            'domains': ['sberbank.ru', 'tinkoff.ru'],
            'ips': [],
            'processes': {},
            'defaultModes': ['bypass'],
          },
        ],
      );
      final state = await showPresets(tester, core);
      expect(find.byKey(const ValueKey('preset_action_steam')), findsOneWidget);

      final searchField = find.byKey(const ValueKey('preset_search_field'));
      expect(searchField, findsOneWidget);

      // Filter by name
      await tester.enterText(searchField, 'steam');
      await tester.pumpAndSettle();
      expect(find.byKey(const ValueKey('preset_action_steam')), findsOneWidget);
      expect(find.byKey(const ValueKey('preset_action_discord')), findsNothing);

      // Filter by Russian alias
      await tester.enterText(searchField, 'дискорд');
      await tester.pumpAndSettle();
      expect(
        find.byKey(const ValueKey('preset_action_discord')),
        findsOneWidget,
      );
      expect(find.byKey(const ValueKey('preset_action_steam')), findsNothing);

      // Filter by domain inside rules
      await tester.enterText(searchField, 'sberbank');
      await tester.pumpAndSettle();
      expect(find.byKey(const ValueKey('preset_action_banks')), findsOneWidget);
      expect(find.byKey(const ValueKey('preset_action_steam')), findsNothing);

      // Nothing found
      await tester.enterText(searchField, 'nonexistentxyz123');
      await tester.pumpAndSettle();
      expect(find.text('ничего не найдено'), findsOneWidget);

      // Clear search
      await tester.tap(find.byIcon(Icons.close));
      await tester.pumpAndSettle();
      expect(find.byKey(const ValueKey('preset_action_steam')), findsOneWidget);
      expect(
        find.byKey(const ValueKey('preset_action_discord')),
        findsOneWidget,
      );

      await tester.pumpWidget(const SizedBox());
      state.dispose();
    },
  );
}

class DiscoveryCore extends NekkoboxCore {
  Completer<String> reply = Completer<String>();
  int calls = 0;
  @override
  String rawCall(String method, [String argsJson = '']) =>
      throw UnimplementedError();
  @override
  Future<String> rawCallAsync(String method, [String argsJson = '']) {
    calls++;
    return reply.future;
  }
}

class PendingAppsCore extends PresetCore {
  final pending = Completer<List<DiscoveredApp>>();
  @override
  Future<List<DiscoveredApp>> listApps({bool all = false}) {
    listAppsCalls++;
    return pending.future;
  }
}
