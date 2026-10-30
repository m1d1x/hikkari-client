import 'package:country_flags/country_flags.dart';
import 'package:flutter/material.dart';

/// Flat rectangular country flag shown to the left of a profile's title,
/// resolved from its server IP/host (see [AppState.countryCodeFor]).
///
/// Three states, all handled here so call sites just forward the resolve
/// status:
///  · [loading] true            → a small spinner sits where the flag will be;
///  · resolved with a code       → the flag bounces in (elastic scale);
///  · resolved without a code    → nothing (renders zero-size), same as a
///    profile whose country simply couldn't be geolocated.
class ProfileFlagBadge extends StatelessWidget {
  final String? countryCode;
  final bool loading;
  final double width;
  final double height;

  const ProfileFlagBadge({
    super.key,
    required this.countryCode,
    this.loading = false,
    this.width = 18,
    this.height = 13,
  });

  @override
  Widget build(BuildContext context) {
    if (loading) {
      return SizedBox(
        width: width,
        height: height,
        child: Center(
          child: SizedBox(
            width: height - 2,
            height: height - 2,
            child: CircularProgressIndicator(
              strokeWidth: 1.5,
              valueColor: AlwaysStoppedAnimation<Color>(
                Theme.of(context).colorScheme.onSurface.withValues(alpha: 0.35),
              ),
            ),
          ),
        ),
      );
    }

    final code = countryCode;
    if (code == null || code.isEmpty) return const SizedBox.shrink();

    // Keyed by the code so a fresh flag re-mounts the builder and the bounce
    // plays exactly once, right when the resolved flag first appears.
    return TweenAnimationBuilder<double>(
      key: ValueKey('flag-$code'),
      tween: Tween(begin: 0, end: 1),
      duration: const Duration(milliseconds: 460),
      curve: Curves.elasticOut,
      builder: (context, t, child) => Transform.scale(
        scale: t,
        child: Opacity(opacity: t.clamp(0.0, 1.0), child: child),
      ),
      child: ClipRRect(
        borderRadius: BorderRadius.circular(2),
        child: CountryFlag.fromCountryCode(
          code,
          theme: ImageTheme(
            width: width,
            height: height,
            shape: const Rectangle(),
          ),
        ),
      ),
    );
  }
}
