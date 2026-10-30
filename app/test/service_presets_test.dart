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

class PresetCore extends NekkoboxCore {
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
          'services': [
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
    expect(find.byType(EditableText), findsOneWidget); // only the manual input
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
  testWidgets('routing stays continuous through reversal and scroll bounds', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(420, 650);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    final core = PresetCore();
    final state = AppState(core);
    await state.refreshPresetState();
    await tester.pumpWidget(
      ChangeNotifierProvider.value(
        value: state,
        child: MaterialApp(theme: buildAppTheme(), home: const HomeScreen()),
      ),
    );
    await tester.pumpAndSettle();
    final section = find.byType(RouteSection);
    final header = find.text('маршрутизация');
    double height() => tester.getSize(section).height;
    final ruleRow = find.byKey(
      const ValueKey('preset_surface_steam'),
      skipOffstage: false,
    );
    final naturalRowWidth = tester.getSize(ruleRow).width;
    final anchoredBottom = tester.getBottomRight(section).dy;
    final headerInset =
        tester.getTopLeft(header).dy - tester.getTopLeft(section).dy;
    final surfaces = [
      find.byKey(const ValueKey('route_manual_surface'), skipOffstage: false),
      find.byKey(const ValueKey('route_presets_surface'), skipOffstage: false),
    ];
    final closed = height();
    double? fullHeight;
    List<double>? fullSurfaces;
    final reveals = find.descendant(
      of: section,
      matching: find.byWidgetPredicate(
        (w) => w is Align && w.heightFactor != null,
        skipOffstage: false,
      ),
      skipOffstage: false,
    );
    Future<void> frames({required bool opening, bool anchored = true}) async {
      final samples = <(double, double, List<double>)>[];
      var previous = height();
      var previousSurfaces = surfaces
          .map((f) => tester.getSize(f).height)
          .toList();
      for (
        var i = 0;
        i < (AppDurations.routingExpand.inMilliseconds / 16).ceil() + 1;
        i++
      ) {
        await tester.pump(const Duration(milliseconds: 16));
        final current = height();
        expect(
          opening ? current >= previous - .01 : current <= previous + .01,
          isTrue,
          reason: 'frame $i: $previous -> $current',
        );
        if (anchored) {
          expect(
            tester.getBottomRight(section).dy,
            closeTo(anchoredBottom, .01),
            reason: 'bottom must remain fixed across the flex/scroll threshold',
          );
          expect(
            tester.getTopLeft(header).dy + current,
            closeTo(anchoredBottom + headerInset, .01),
            reason: 'header must keep moving as total height grows',
          );
        }
        final currentSurfaces = surfaces
            .map((f) => tester.getSize(f).height)
            .toList();
        for (var j = 0; j < surfaces.length; j++) {
          expect(
            opening
                ? currentSurfaces[j] >= previousSurfaces[j] - .01
                : currentSurfaces[j] <= previousSurfaces[j] + .01,
            isTrue,
            reason: 'each rounded surface must expand with its contents',
          );
        }
        samples.add((
          tester.widget<Align>(reveals.first).heightFactor!,
          current,
          currentSurfaces,
        ));
        previousSurfaces = currentSurfaces;
        expect(
          tester.getSize(ruleRow).width,
          closeTo(naturalRowWidth, .001),
          reason: 'revealing vertically must not change text layout width',
        );
        expect(tester.takeException(), isNull, reason: 'frame $i');
        previous = current;
      }
      fullHeight ??= height();
      fullSurfaces ??= surfaces.map((f) => tester.getSize(f).height).toList();
      for (final (progress, total, sizes) in samples) {
        expect(
          total,
          closeTo(closed + (fullHeight! - closed) * progress, .01),
          reason:
              'layout must preserve the controller curve without extra impulses',
        );
        for (var j = 0; j < sizes.length; j++) {
          expect(
            sizes[j],
            closeTo(fullSurfaces![j] * progress, .01),
            reason: 'every card must follow the same animation progress',
          );
        }
      }
    }

    await tester.tap(header);
    await tester.pump();
    expect(height(), closeTo(closed, .01));
    await frames(opening: true);
    final opened = height();
    expect(opened, greaterThan(closed + 100));
    expect(core.listAppsCalls, 0);
    await tester.tap(header);
    await tester.pump();
    expect(height(), closeTo(opened, .01));
    await tester.pump(AppDurations.routingExpand * (48 / 260));
    final interrupted = height();
    expect(opened - interrupted, greaterThan((opened - closed) / 2));
    await tester.tap(header);
    await tester.pump();
    expect(height(), closeTo(interrupted, .01));
    await frames(opening: true);
    final scroll = tester
        .state<ScrollableState>(find.byType(Scrollable).first)
        .position;
    scroll.jumpTo(scroll.maxScrollExtent);
    await tester.pump();
    await tester.tap(header);
    await tester.pump();
    await frames(opening: false, anchored: false);
    expect(height(), closeTo(closed, .01));
    expect(scroll.pixels, closeTo(0, .01));
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
      final bottom = tester.getBottomRight(section).dy;
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
        expect(tester.getBottomRight(section).dy, closeTo(bottom, .01));
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
      final beforeClose = height();
      await tester.tap(find.text('маршрутизация'));
      await tester.pump();
      expect(height(), closeTo(beforeClose, .01));
      await tester.pumpAndSettle();
      expect(tester.takeException(), isNull);
      await tester.tap(find.text('маршрутизация'));
      await tester.pumpAndSettle();
      expect(height(), closeTo(domainsHeight, .01));
      // A nested group's own animation must not be chased by the tab viewport.
      await tester.tap(find.text('steam'), buttons: kSecondaryMouseButton);
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
    'outside click collapses routing without consuming the target action',
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
                    child: SingleChildScrollView(
                      reverse: true,
                      child: RouteSection(),
                    ),
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
      await tester.enterText(find.byType(EditableText), 'draft.example.com');
      expect(height(), closeTo(opened, .01));
      await tester.tap(find.text('steam'), buttons: kSecondaryMouseButton);
      await tester.pumpAndSettle();
      expect(height(), greaterThan(opened));
      await tester.tap(find.text('снаружи'));
      await tester.pumpAndSettle();
      expect(height(), closeTo(closed, .01));
      expect(outsideClicks, 1);
      expect(state.routing.enabled, isTrue);
      await tester.tap(find.text('маршрутизация'));
      await tester.pumpAndSettle();
      expect(
        tester.widget<EditableText>(find.byType(EditableText)).controller.text,
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
