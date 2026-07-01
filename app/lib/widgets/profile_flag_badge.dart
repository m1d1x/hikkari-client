import 'package:country_flags/country_flags.dart';
import 'package:flutter/material.dart';

/// Flat rectangular country flag shown to the left of a profile's title,
/// resolved from its server IP/host (see [AppState.profileCountries]).
/// Renders nothing when the code is empty/unresolved — no placeholder, so
/// profiles without a known country just show their title flush left, same
/// as before this feature existed.
class ProfileFlagBadge extends StatelessWidget {
  final String? countryCode;
  final double width;
  final double height;

  const ProfileFlagBadge({
    super.key,
    required this.countryCode,
    this.width = 18,
    this.height = 13,
  });

  @override
  Widget build(BuildContext context) {
    final code = countryCode;
    if (code == null || code.isEmpty) return const SizedBox.shrink();
    return ClipRRect(
      borderRadius: BorderRadius.circular(2),
      child: CountryFlag.fromCountryCode(
        code,
        theme: ImageTheme(
          width: width,
          height: height,
          shape: const Rectangle(),
        ),
      ),
    );
  }
}
