import 'package:flutter/material.dart';

/// Unified "service icon" badge: a rounded, brand-tinted container with the
/// icon centered inside. Used by [ServicePingSheet] so every row — built-in
/// brand icon or custom favicon — looks like it belongs to the same family
/// instead of a grab-bag of generic outlined glyphs.
///
/// Two icon sources:
///  - [icon] — a static [IconData] (brand glyph from `simple_icons`, or a
///    Material fallback for services without one).
///  - [faviconUrl] — fetched over the network for user-added custom
///    endpoints; falls back to [icon] while loading or on error.
class ServiceIconBadge extends StatelessWidget {
  final IconData icon;
  final Color color;
  final String? faviconUrl;
  final double size;

  const ServiceIconBadge({
    super.key,
    required this.icon,
    required this.color,
    this.faviconUrl,
    this.size = 30,
  });

  @override
  Widget build(BuildContext context) {
    final iconSize = size * 16 / 30;
    return Container(
      width: size,
      height: size,
      alignment: Alignment.center,
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(size * 8 / 30),
      ),
      child: _glyph(iconSize),
    );
  }

  Widget _glyph(double iconSize) {
    final fallback = Icon(icon, size: iconSize, color: color);
    final url = faviconUrl;
    if (url == null) return fallback;
    return ClipRRect(
      borderRadius: BorderRadius.circular(4),
      child: Image.network(
        url,
        width: iconSize,
        height: iconSize,
        fit: BoxFit.contain,
        gaplessPlayback: true,
        errorBuilder: (_, _, _) => fallback,
        loadingBuilder: (context, child, progress) =>
            progress == null ? child : fallback,
      ),
    );
  }
}
