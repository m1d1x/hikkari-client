import 'package:flutter/material.dart';

import 'tokens.dart';

/// Builds the single ThemeData the app uses. All colour/shape/typography
/// decisions are sourced from [NekkoTokens] so widgets never hardcode hex.
ThemeData buildAppTheme() {
  const t = NekkoTokens.dark;
  const transparentOverlay = WidgetStatePropertyAll<Color?>(Colors.transparent);
  final clickCursor = WidgetStateProperty.resolveWith<MouseCursor?>(
    (states) => states.contains(WidgetState.disabled)
        ? SystemMouseCursors.basic
        : SystemMouseCursors.click,
  );
  final quietButtonStyle = ButtonStyle(
    overlayColor: transparentOverlay,
    mouseCursor: clickCursor,
  );
  final base = ThemeData(
    useMaterial3: true,
    brightness: Brightness.dark,
    scaffoldBackgroundColor: t.bg,
    canvasColor: t.bg,
    splashFactory: NoSplash.splashFactory,
    splashColor: Colors.transparent,
    highlightColor: Colors.transparent,
    hoverColor: Colors.transparent,
    focusColor: Colors.transparent,
    colorScheme: ColorScheme.fromSeed(
      seedColor: t.primary,
      brightness: Brightness.dark,
      surface: t.surface,
      onSurface: t.text,
      primary: t.primary,
      onPrimary: t.bg,
      error: t.bad,
    ),
    fontFamily: t.monoFamily,
    extensions: const [t],
  );

  return base.copyWith(
    textTheme: base.textTheme.apply(
      fontFamily: t.monoFamily,
      bodyColor: t.text,
      displayColor: t.text,
    ),
    primaryTextTheme: base.primaryTextTheme.apply(
      fontFamily: t.monoFamily,
      bodyColor: t.text,
      displayColor: t.text,
    ),
    textButtonTheme: TextButtonThemeData(style: quietButtonStyle),
    filledButtonTheme: FilledButtonThemeData(style: quietButtonStyle),
    iconButtonTheme: IconButtonThemeData(style: quietButtonStyle),
    dividerColor: t.border,
    iconTheme: IconThemeData(color: t.textMuted),
    appBarTheme: AppBarTheme(
      backgroundColor: t.bg,
      foregroundColor: t.text,
      elevation: 0,
      centerTitle: false,
    ),
    snackBarTheme: SnackBarThemeData(
      backgroundColor: t.surface2,
      contentTextStyle: TextStyle(color: t.text),
    ),
    tooltipTheme: TooltipThemeData(
      decoration: BoxDecoration(
        color: t.surface2,
        border: Border.all(color: t.borderStrong),
        borderRadius: BorderRadius.circular(t.radius),
      ),
      textStyle: TextStyle(
        color: t.text,
        fontFamily: t.monoFamily,
        fontSize: 13,
        height: 1.4,
      ),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
      constraints: const BoxConstraints(maxWidth: 280),
      waitDuration: const Duration(milliseconds: 450),
      showDuration: const Duration(seconds: 4),
      preferBelow: false,
    ),
    inputDecorationTheme: InputDecorationTheme(
      filled: true,
      fillColor: t.surface2,
      hintStyle: TextStyle(color: t.textFaint),
      contentPadding:
          const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
      border: OutlineInputBorder(
        borderRadius: BorderRadius.circular(t.radius),
        borderSide: BorderSide(color: t.border),
      ),
      enabledBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(t.radius),
        borderSide: BorderSide(color: t.border),
      ),
      focusedBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(t.radius),
        borderSide: BorderSide(color: t.borderStrong),
      ),
    ),
  );
}
