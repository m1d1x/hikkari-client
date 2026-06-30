import 'package:flutter/material.dart';

/// Design tokens ported 1:1 from `NekkoVPN/frontend/src/App.svelte` (the
/// `:root` block). Exposed through a [ThemeExtension] so widgets can pull
/// them from the BuildContext via `Theme.of(context).extension<NekkoTokens>()!`
/// without globals or hand-passed props.
@immutable
class NekkoTokens extends ThemeExtension<NekkoTokens> {
  // ----- surfaces -----
  final Color bg;          // page background
  final Color surface;     // raised card / row
  final Color surface2;    // nested card / input
  final Color border;      // hairline
  final Color borderStrong;

  // ----- text -----
  final Color text;
  final Color textMuted;
  final Color textFaint;

  // ----- accent -----
  final Color primary;

  // ----- status -----
  final Color ok;
  final Color warn;
  final Color bad;

  // ----- shape -----
  final double radius;
  final double radiusLg;

  // ----- type -----
  final String monoFamily;

  const NekkoTokens({
    required this.bg,
    required this.surface,
    required this.surface2,
    required this.border,
    required this.borderStrong,
    required this.text,
    required this.textMuted,
    required this.textFaint,
    required this.primary,
    required this.ok,
    required this.warn,
    required this.bad,
    required this.radius,
    required this.radiusLg,
    required this.monoFamily,
  });

  /// The default dark palette — same hex values the Svelte UI uses.
  static const dark = NekkoTokens(
    bg: Color(0xFF0E0E10),
    surface: Color(0xFF18181B),
    surface2: Color(0xFF1F1F23),
    border: Color(0x0FFFFFFF),       // rgba(255,255,255,.06)
    borderStrong: Color(0x1FFFFFFF), // rgba(255,255,255,.12)
    text: Color(0xFFEDEDEE),
    textMuted: Color(0xFF8A8A8E),
    textFaint: Color(0xFF5B5B60),
    primary: Color(0xFFFF7EB9),
    ok: Color(0xFF34C759),
    warn: Color(0xFFFF9F0A),
    bad: Color(0xFFFF453A),
    radius: 14,
    radiusLg: 18,
    // The app bundles IBM Plex Mono and also uses it as the global font family.
    monoFamily: 'IBM Plex Mono',
  );

  @override
  NekkoTokens copyWith({
    Color? bg,
    Color? surface,
    Color? surface2,
    Color? border,
    Color? borderStrong,
    Color? text,
    Color? textMuted,
    Color? textFaint,
    Color? primary,
    Color? ok,
    Color? warn,
    Color? bad,
    double? radius,
    double? radiusLg,
    String? monoFamily,
  }) =>
      NekkoTokens(
        bg: bg ?? this.bg,
        surface: surface ?? this.surface,
        surface2: surface2 ?? this.surface2,
        border: border ?? this.border,
        borderStrong: borderStrong ?? this.borderStrong,
        text: text ?? this.text,
        textMuted: textMuted ?? this.textMuted,
        textFaint: textFaint ?? this.textFaint,
        primary: primary ?? this.primary,
        ok: ok ?? this.ok,
        warn: warn ?? this.warn,
        bad: bad ?? this.bad,
        radius: radius ?? this.radius,
        radiusLg: radiusLg ?? this.radiusLg,
        monoFamily: monoFamily ?? this.monoFamily,
      );

  @override
  NekkoTokens lerp(ThemeExtension<NekkoTokens>? other, double t) {
    if (other is! NekkoTokens) return this;
    return NekkoTokens(
      bg: Color.lerp(bg, other.bg, t)!,
      surface: Color.lerp(surface, other.surface, t)!,
      surface2: Color.lerp(surface2, other.surface2, t)!,
      border: Color.lerp(border, other.border, t)!,
      borderStrong: Color.lerp(borderStrong, other.borderStrong, t)!,
      text: Color.lerp(text, other.text, t)!,
      textMuted: Color.lerp(textMuted, other.textMuted, t)!,
      textFaint: Color.lerp(textFaint, other.textFaint, t)!,
      primary: Color.lerp(primary, other.primary, t)!,
      ok: Color.lerp(ok, other.ok, t)!,
      warn: Color.lerp(warn, other.warn, t)!,
      bad: Color.lerp(bad, other.bad, t)!,
      radius: _lerpD(radius, other.radius, t),
      radiusLg: _lerpD(radiusLg, other.radiusLg, t),
      monoFamily: t < 0.5 ? monoFamily : other.monoFamily,
    );
  }

  static double _lerpD(double a, double b, double t) => a + (b - a) * t;
}

/// Convenience extension so widgets can write `context.tokens` instead of
/// `Theme.of(context).extension<NekkoTokens>()!`.
extension NekkoTokensContext on BuildContext {
  NekkoTokens get tokens => Theme.of(this).extension<NekkoTokens>()!;
}
