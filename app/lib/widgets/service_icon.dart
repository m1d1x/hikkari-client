import 'package:flutter/material.dart';
import 'package:flutter_svg/flutter_svg.dart';
import 'package:simple_icons/simple_icons.dart';

import 'brand_icons.dart';

export 'brand_icons.dart' show resolveBrandSvg;

/// Resolves a catalog service `icon` identifier to its brand glyph and accent
/// color, falling back to [Icons.apps] and [fallbackColor] for unknown IDs.
(IconData, Color) resolveServiceIcon(
  String iconId,
  Color fallbackColor,
) => switch (iconId) {
  'ru' || 'russia' => (Icons.language, const Color(0xFF4C8BF5)),
  'banks' ||
  'bank' => (Icons.account_balance_outlined, const Color(0xFF21A038)),
  'sber' ||
  'sberbank' => (Icons.account_balance_outlined, const Color(0xFF21A038)),
  'tbank' || 'tinkoff' => (Icons.credit_card_outlined, const Color(0xFFFFDD2D)),
  'vtb' => (Icons.account_balance_outlined, const Color(0xFF0A2896)),
  'alfa' ||
  'alfabank' => (Icons.account_balance_outlined, const Color(0xFFEF3124)),
  'gazprombank' ||
  'gpb' => (Icons.account_balance_outlined, const Color(0xFF004085)),
  'raiffeisen' => (Icons.account_balance_outlined, const Color(0xFFFFE600)),
  'sovcombank' => (Icons.account_balance_outlined, const Color(0xFF003366)),
  'tochka' => (Icons.account_balance_outlined, const Color(0xFF8C30F5)),
  'psb' => (Icons.account_balance_outlined, const Color(0xFFE35205)),
  'yoomoney' => (Icons.account_balance_outlined, const Color(0xFF8000FF)),
  'mir' => (Icons.credit_card_outlined, const Color(0xFF0F7544)),
  'gosuslugi' => (Icons.verified_user_outlined, const Color(0xFF0D4CD3)),
  'marketplaces' ||
  'shop' => (Icons.shopping_bag_outlined, const Color(0xFF005BFF)),
  'ozon' => (Icons.shopping_bag_outlined, const Color(0xFF005BFF)),
  'wildberries' ||
  'wb' => (Icons.shopping_bag_outlined, const Color(0xFFCB11AB)),
  'avito' => (Icons.storefront_outlined, const Color(0xFF96D000)),
  'megamarket' => (Icons.shopping_bag_outlined, const Color(0xFF6B18D4)),
  'lamoda' => (Icons.shopping_bag_outlined, const Color(0xFF111111)),
  'dns' => (Icons.shopping_bag_outlined, const Color(0xFFFF6A00)),
  'citilink' => (Icons.shopping_bag_outlined, const Color(0xFFFF6600)),
  'mvideo' => (Icons.shopping_bag_outlined, const Color(0xFFD50032)),
  'samokat' => (Icons.shopping_bag_outlined, const Color(0xFFFA3E54)),
  'kuper' => (Icons.shopping_bag_outlined, const Color(0xFF107C41)),
  'cian' => (Icons.storefront_outlined, const Color(0xFF0045EB)),
  'hh' => (Icons.storefront_outlined, const Color(0xFFD6001C)),
  'yandex' => (SimpleIcons.yandexcloud, const Color(0xFFFC3F1D)),
  'yandex-music' ||
  'yandex_music' ||
  'yandexmusic' => (Icons.graphic_eq_rounded, const Color(0xFFFFCC00)),
  'yandex-taxi' ||
  'yandex_taxi' ||
  'yandextaxi' ||
  'yandex-go' ||
  'yandex_go' ||
  'yandexgo' => (Icons.local_taxi_rounded, const Color(0xFFFFDD00)),
  'yandex-maps' ||
  'yandex_maps' ||
  'yandexmaps' => (Icons.map_rounded, const Color(0xFFFC3F1D)),
  'yandex-disk' ||
  'yandex_disk' ||
  'yandexdisk' => (Icons.cloud_queue_rounded, const Color(0xFF2A72F8)),
  'yandex-market' ||
  'yandex_market' ||
  'yandexmarket' => (Icons.shopping_bag_rounded, const Color(0xFFFFD500)),
  'vk' => (SimpleIcons.vk, const Color(0xFF0077FF)),
  'vk-video' ||
  'vkvideo' => (Icons.video_collection_outlined, const Color(0xFF0077FF)),
  'vk-play' ||
  'vkplay' => (Icons.sports_esports_outlined, const Color(0xFFFF4500)),
  'mailru' || 'mail' => (SimpleIcons.maildotru, const Color(0xFF005FF9)),
  'ok' ||
  'odnoklassniki' => (SimpleIcons.odnoklassniki, const Color(0xFFEE8208)),
  'kinopoisk' => (SimpleIcons.kinopoisk, const Color(0xFFFF6600)),
  'ivi' => (Icons.smart_display_outlined, const Color(0xFFEA1A4B)),
  'okko' => (Icons.smart_display_outlined, const Color(0xFF5D12D2)),
  'wink' => (Icons.smart_display_outlined, const Color(0xFFFF5C00)),
  'premier' => (Icons.smart_display_outlined, const Color(0xFFE50914)),
  'start' => (Icons.smart_display_outlined, const Color(0xFFFF5000)),
  'kion' => (Icons.smart_display_outlined, const Color(0xFFE40046)),
  'smotrim' => (Icons.smart_display_outlined, const Color(0xFF0066FF)),
  'streaming-ru' || 'tv' => (Icons.tv_outlined, const Color(0xFFFF6600)),
  'transport-ru' || 'rzd' => (Icons.train_outlined, const Color(0xFFE21A1A)),
  'aeroflot' => (Icons.flight_outlined, const Color(0xFF002B49)),
  's7' => (Icons.flight_outlined, const Color(0xFFB4D435)),
  'pobeda' => (Icons.flight_outlined, const Color(0xFF009CE8)),
  'aviasales' => (Icons.flight_outlined, const Color(0xFF0C73FE)),
  'mts' => (Icons.signal_cellular_alt_rounded, const Color(0xFFE30611)),
  'megafon' => (Icons.signal_cellular_alt_rounded, const Color(0xFF00B956)),
  'beeline' => (Icons.signal_cellular_alt_rounded, const Color(0xFFFFBE00)),
  't2' ||
  'tele2' => (Icons.signal_cellular_alt_rounded, const Color(0xFFFF007A)),
  'rostelecom' => (Icons.signal_cellular_alt_rounded, const Color(0xFFFF5C00)),
  'rutube' => (Icons.smart_display_outlined, const Color(0xFF00A8FF)),
  'habr' => (SimpleIcons.habr, const Color(0xFF6593A6)),
  'pikabu' => (Icons.article_outlined, const Color(0xFF1B9E43)),
  'vc' => (Icons.article_outlined, const Color(0xFFE6E6E6)),
  'dtf' => (Icons.sports_esports_outlined, const Color(0xFF00E5FF)),
  'rbc' => (Icons.newspaper_outlined, const Color(0xFF9A1820)),
  'kommersant' => (Icons.newspaper_outlined, const Color(0xFFFFFFFF)),
  'lenta' => (Icons.newspaper_outlined, const Color(0xFF002B49)),
  'ria' => (Icons.newspaper_outlined, const Color(0xFF004A99)),
  'tass' => (Icons.newspaper_outlined, const Color(0xFF003399)),
  'kaspersky' => (SimpleIcons.kaspersky, const Color(0xFF006D55)),
  'selectel' => (Icons.cloud_outlined, const Color(0xFF1E40AF)),
  'timeweb' => (Icons.cloud_outlined, const Color(0xFF0055FF)),
  'regru' => (Icons.language, const Color(0xFF1A80E5)),
  '1c' => (Icons.business_center_outlined, const Color(0xFFFFDD00)),
  'bitrix24' => (Icons.business_center_outlined, const Color(0xFF00A4E4)),
  'kontur' => (Icons.business_center_outlined, const Color(0xFFEF3124)),
  'sbis' => (Icons.business_center_outlined, const Color(0xFF00A651)),
  '2gis' || 'gis' => (Icons.map_outlined, const Color(0xFF83BA29)),
  'steam' => (SimpleIcons.steam, const Color(0xFFC7D0D8)),
  'faceit' => (Icons.sports_esports_outlined, const Color(0xFFFF5500)),
  'discord' => (SimpleIcons.discord, const Color(0xFF5865F2)),
  'telegram' => (SimpleIcons.telegram, const Color(0xFF26A5E4)),
  'youtube' => (SimpleIcons.youtube, const Color(0xFFFF0033)),
  'openai' || 'chatgpt' => (Icons.auto_awesome, const Color(0xFF10A37F)),
  'claude' || 'anthropic' => (SimpleIcons.claude, const Color(0xFFD97757)),
  'gemini' => (SimpleIcons.googlegemini, const Color(0xFF8E75B2)),
  'instagram' || 'meta' => (SimpleIcons.instagram, const Color(0xFFE4405F)),
  'x' || 'twitter' => (SimpleIcons.x, const Color(0xFFE7E9EA)),
  'spotify' => (SimpleIcons.spotify, const Color(0xFF1ED760)),
  'twitch' => (SimpleIcons.twitch, const Color(0xFF9146FF)),
  'notion' => (SimpleIcons.notion, const Color(0xFFE6E6E6)),
  'figma' => (SimpleIcons.figma, const Color(0xFFF24E1E)),
  'github' ||
  'copilot' ||
  'github-copilot' => (SimpleIcons.githubcopilot, const Color(0xFFCDD9E5)),
  'google' => (SimpleIcons.google, const Color(0xFF4285F4)),
  _ => (Icons.apps, fallbackColor),
};

String _normalizeIconId(String raw) {
  final k = raw.trim().toLowerCase().replaceAll('_', '-');
  return switch (k) {
    'yandex-go' || 'yandexgo' || 'yandextaxi' => 'yandex-taxi',
    'yandexmusic' => 'yandex-music',
    'yandexmaps' => 'yandex-maps',
    'yandexdisk' => 'yandex-disk',
    'yandexmarket' => 'yandex-market',
    'vkvideo' => 'vk-video',
    'vkplay' => 'vk-play',
    'alfabank' => 'alfa',
    'sberbank' => 'sber',
    'tinkoff' => 'tbank',
    'twitter' => 'x',
    'chatgpt' => 'openai',
    _ => k,
  };
}

/// Unified "service icon" badge: a rounded, brand-tinted container with the
/// authentic corporate brand asset centered inside.
///
/// Priority:
/// 1. Bundled official high-res PNG from `assets/services/<iconId>.png`
/// 2. Embedded authentic vector SVG via [resolveBrandSvg]
/// 3. Remote [faviconUrl]
/// 4. Material/SimpleIcons glyph via [icon]
class ServiceIconBadge extends StatelessWidget {
  final IconData? icon;
  final String? iconId;
  final Color color;
  final String? faviconUrl;
  final double size;

  const ServiceIconBadge({
    super.key,
    this.icon,
    this.iconId,
    required this.color,
    this.faviconUrl,
    this.size = 30,
  });

  @override
  Widget build(BuildContext context) {
    final id = iconId;
    final iconSize = size * 20 / 30;
    final fallback = Icon(icon ?? Icons.apps, size: iconSize, color: color);
    final fallbackBox = Container(
      width: size,
      height: size,
      alignment: Alignment.center,
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(size * 8 / 30),
      ),
      child: fallback,
    );

    if (id != null) {
      final normalized = _normalizeIconId(id);
      return ClipRRect(
        borderRadius: BorderRadius.circular(size * 8 / 30),
        child: Container(
          width: size,
          height: size,
          color: Colors.transparent,
          child: Image.asset(
            'assets/services/$normalized.png',
            width: size,
            height: size,
            fit: BoxFit.cover,
            errorBuilder: (_, _, _) {
              final svg = resolveBrandSvg(normalized);
              if (svg != null) {
                return SvgPicture.string(
                  svg,
                  width: size,
                  height: size,
                  fit: BoxFit.cover,
                );
              }
              return fallbackBox;
            },
          ),
        ),
      );
    }

    final url = faviconUrl;
    if (url != null) {
      return ClipRRect(
        borderRadius: BorderRadius.circular(size * 8 / 30),
        child: Container(
          width: size,
          height: size,
          color: color.withValues(alpha: 0.15),
          alignment: Alignment.center,
          child: Image.network(
            url,
            width: size,
            height: size,
            fit: BoxFit.contain,
            gaplessPlayback: true,
            errorBuilder: (_, _, _) => fallbackBox,
            loadingBuilder: (context, child, progress) =>
                progress == null ? child : fallbackBox,
          ),
        ),
      );
    }

    return fallbackBox;
  }
}
