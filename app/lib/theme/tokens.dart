import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/physics.dart';

/// System animation curves.
abstract final class AppCurves {
  /// Shared hover easing for buttons, labels, and icons.
  ///
  /// A linear crossfade — the same glide the routing header's title and the
  /// header icons take. Ease-out curves at hover durations (140 ms) spend
  /// almost the whole change in the first two frames, which reads as a snap,
  /// not a transition.
  static const Curve hover = Curves.linear;

  /// Snappy start with smooth deceleration ("fast in, slow out").
  /// Matches standard fluid desktop curve cubic-bezier(0.16, 1, 0.3, 1).
  static const Curve fastInSlowOut = Cubic(0.16, 1.0, 0.3, 1.0);
}

/// Standard system transition durations.
abstract final class AppDurations {
  /// Shared hover transition for coordinated foreground and surface changes.
  static const Duration hover = Duration(milliseconds: 140);

  /// Fast UI transitions (e.g. icon tint, text highlights).
  static const Duration fast = Duration(milliseconds: 160);

  /// Standard element transitions (e.g. hover crossfade, card surface, toggle).
  static const Duration normal = Duration(milliseconds: 220);

  /// Accordion panels and large layout expansions.
  static const Duration expand = Duration(milliseconds: 260);

  /// Tab height changes and preset row details inside routing.
  static const Duration routingDetails = Duration(milliseconds: 440);
}

/// Physical motion for large on-screen travel.
abstract final class AppMotion {
  static const double _settleMin = .27;
  static const double _settleMax = .44;
  static const double _settleReference = 300;

  /// Critically damped spring for moving a surface [distance] logical pixels,
  /// such as the routing card rising to the top of the window.
  ///
  /// It answers the click within a frame, peaks at a sixth of the settling
  /// time and lands without a bounce, in both directions and when reversed
  /// mid-flight (the spring keeps the current velocity). Settling time grows
  /// with the square root of the path: a tall window gets more time instead of
  /// a faster card, without making it wait linearly longer.
  static SpringDescription travelSpring(double distance) {
    final settle = (_settleMin * math.sqrt(distance / _settleReference)).clamp(
      _settleMin,
      _settleMax,
    );
    // After 6/ω a critically damped spring has under 2% of the path left;
    // the rest is a sub-pixel-per-frame tail.
    final omega = 6 / settle;
    return SpringDescription(
      mass: 1,
      stiffness: omega * omega,
      damping: 2 * omega,
    );
  }
}

/// Design tokens ported 1:1 from `NekkoVPN/frontend/src/App.svelte` (the
/// `:root` block). Exposed through a [ThemeExtension] so widgets can pull
/// them from the BuildContext via `Theme.of(context).extension<NekkoTokens>()!`
/// without globals or hand-passed props.
@immutable
class NekkoTokens extends ThemeExtension<NekkoTokens> {
  // ----- surfaces -----
  final Color bg; // page background
  final Color surface; // raised card / row
  final Color surface2; // nested card / input
  final Color border; // hairline
  final Color borderStrong;

  // ----- text -----
  final Color text;
  final Color textSecondary; // dimmed a notch below [text] — flat alternative
  // to `text.withValues(alpha: ...)`, for headings that shouldn't compete
  // with a brighter [text] element elsewhere on screen.
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

  // ----- type scale -----
  // Единая лестница размеров, чтобы заголовки/лейблы не расходились по
  // экранам. Меняешь константу здесь — двигаются все места разом.
  static const double sizeSectionTitle = 16; // маршрутизация / профили
  static const double sizeSubtitle = 15; // счётчик рядом с заголовком
  static const double sizeLabel = 13; // чипы, вторичные подписи
  static const double sizeBody = 12; // строки, поиск, тело
  static const double sizeCaption = 11; // мелкие пометки

  /// Общий стиль для заголовков секций верхнего уровня. Цвет можно
  /// переопределить через copyWith (напр. hover-подсветка).
  TextStyle get sectionTitle => TextStyle(
    color: text,
    fontSize: sizeSectionTitle,
    fontWeight: FontWeight.w700,
    height: 1,
  );

  const NekkoTokens({
    required this.bg,
    required this.surface,
    required this.surface2,
    required this.border,
    required this.borderStrong,
    required this.text,
    required this.textSecondary,
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
    border: Color(0x0FFFFFFF), // rgba(255,255,255,.06)
    borderStrong: Color(0x1FFFFFFF), // rgba(255,255,255,.12)
    text: Color(0xFFEDEDEE),
    textSecondary: Color(0xFFC8C8CA),
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
    Color? textSecondary,
    Color? textMuted,
    Color? textFaint,
    Color? primary,
    Color? ok,
    Color? warn,
    Color? bad,
    double? radius,
    double? radiusLg,
    String? monoFamily,
  }) => NekkoTokens(
    bg: bg ?? this.bg,
    surface: surface ?? this.surface,
    surface2: surface2 ?? this.surface2,
    border: border ?? this.border,
    borderStrong: borderStrong ?? this.borderStrong,
    text: text ?? this.text,
    textSecondary: textSecondary ?? this.textSecondary,
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
      textSecondary: Color.lerp(textSecondary, other.textSecondary, t)!,
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
