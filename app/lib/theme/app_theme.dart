import 'package:flutter/material.dart';

import 'tokens.dart';

/// Builds the single ThemeData the app uses. All colour/shape/typography
/// decisions are sourced from [NekkoTokens] so widgets never hardcode hex.
ThemeData buildAppTheme() {
  const t = NekkoTokens.dark;
  final base = ThemeData(
    useMaterial3: true,
    brightness: Brightness.dark,
    scaffoldBackgroundColor: t.bg,
    canvasColor: t.bg,
    colorScheme: ColorScheme.fromSeed(
      seedColor: t.text,
      brightness: Brightness.dark,
      surface: t.surface,
      onSurface: t.text,
      primary: t.text,
      onPrimary: t.bg,
      error: t.bad,
    ),
    extensions: const [t],
  );

  return base.copyWith(
    textTheme: base.textTheme.apply(
      bodyColor: t.text,
      displayColor: t.text,
    ),
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
