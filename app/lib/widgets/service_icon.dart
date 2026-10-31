import 'package:flutter/material.dart';
import 'package:simple_icons/simple_icons.dart';

/// Resolves a catalog service `icon` identifier to its brand glyph and accent
/// color, falling back to [Icons.apps] and [fallbackColor] for unknown IDs.
(IconData, Color) resolveServiceIcon(String iconId, Color fallbackColor) =>
    switch (iconId) {
      'ru' || 'russia' => (Icons.language, const Color(0xFF4C8BF5)),
      'banks' || 'bank' => (
        Icons.account_balance_outlined,
        const Color(0xFF21A038),
      ),
      'gosuslugi' => (Icons.verified_user_outlined, const Color(0xFF0D4CD3)),
      'marketplaces' || 'shop' => (
        Icons.shopping_bag_outlined,
        const Color(0xFF005BFF),
      ),
      'yandex' => (SimpleIcons.yandexcloud, const Color(0xFFFC3F1D)),
      'vk' => (SimpleIcons.vk, const Color(0xFF0077FF)),
      'steam' => (SimpleIcons.steam, const Color(0xFFC7D0D8)),
      'discord' => (SimpleIcons.discord, const Color(0xFF5865F2)),
      'telegram' => (SimpleIcons.telegram, const Color(0xFF26A5E4)),
      'youtube' => (SimpleIcons.youtube, const Color(0xFFFF0033)),
      'openai' || 'chatgpt' => (Icons.auto_awesome, const Color(0xFF7FD9B0)),
      'claude' || 'anthropic' => (SimpleIcons.claude, const Color(0xFFD97757)),
      'gemini' => (SimpleIcons.googlegemini, const Color(0xFF8E75B2)),
      'instagram' || 'meta' => (
        SimpleIcons.instagram,
        const Color(0xFFE4405F),
      ),
      'x' || 'twitter' => (SimpleIcons.x, const Color(0xFFE7E9EA)),
      'spotify' => (SimpleIcons.spotify, const Color(0xFF1ED760)),
      'twitch' => (SimpleIcons.twitch, const Color(0xFF9146FF)),
      'notion' => (SimpleIcons.notion, const Color(0xFFE6E6E6)),
      'figma' => (SimpleIcons.figma, const Color(0xFFF24E1E)),
      'github' || 'copilot' || 'github-copilot' => (
        SimpleIcons.githubcopilot,
        const Color(0xFFCDD9E5),
      ),
      'google' => (SimpleIcons.google, const Color(0xFF4285F4)),
      _ => (Icons.apps, fallbackColor),
    };

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
