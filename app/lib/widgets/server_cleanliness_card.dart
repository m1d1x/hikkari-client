import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../core/models.dart';
import '../state/app_state.dart';
import '../theme/tokens.dart';

/// Плашка «чистоты» активного сервера — как интернет видит наш выход.
/// В свёрнутом виде: цветной индикатор + индекс чистоты + краткая сводка
/// (датацентр/чистый/помечен · страна · ASN). По тапу разворачивается в
/// подробную статистику (IP, ASN, хостинг-флаг, fraud-score, Cloudflare).
///
/// Данные тянутся из движкового `ServerCleanliness` (метод `ServerCleanliness`),
/// который гоняет проверки ЧЕРЕЗ активный туннель — те же байты, что и трафик
/// пользователя, поэтому картина честная, а не оценочная.
class ServerCleanlinessCard extends StatefulWidget {
  const ServerCleanlinessCard({super.key});

  @override
  State<ServerCleanlinessCard> createState() => _ServerCleanlinessCardState();
}

class _ServerCleanlinessCardState extends State<ServerCleanlinessCard> {
  bool _expanded = false;

  // Цвет вердикта: зелёный — чистый, оранжевый — датацентр, красный — помечен,
  // приглушённый — проверка/нет данных.
  Color _verdictColor(NekkoTokens t, String verdict) {
    switch (verdict) {
      case 'clean':
        return t.ok;
      case 'datacenter':
      case 'suspicious':
        return t.warn;
      case 'flagged':
        return t.bad;
      default:
        return t.textMuted;
    }
  }

  String _verdictLabel(String verdict) {
    switch (verdict) {
      case 'clean':
        return 'чистый выход';
      case 'datacenter':
        return 'датацентр';
      case 'flagged':
        return 'помечен';
      case 'suspicious':
        return 'под вопросом';
      case 'checking':
        return 'проверка…';
      default:
        return 'чистота сервера';
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final c = context.watch<AppState>().cleanliness;

    // Туннель выключен — плашке нечего показывать, прячем целиком.
    if (c.verdict == 'off') return const SizedBox.shrink();

    final color = _verdictColor(t, c.verdict);
    final checking = c.verdict == 'checking' || !c.checked;

    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
      child: ClipRRect(
        borderRadius: BorderRadius.circular(t.radiusLg),
        child: Material(
          color: t.surface2,
          child: InkWell(
            onTap: () => setState(() => _expanded = !_expanded),
            mouseCursor: SystemMouseCursors.click,
            hoverColor: color.withValues(alpha: 0.06),
            child: AnimatedSize(
              duration: const Duration(milliseconds: 180),
              curve: AppCurves.fastInSlowOut,
              alignment: Alignment.topCenter,
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  _collapsedRow(t, c, color, checking),
                  if (_expanded && c.checked) _details(t, c),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }

  Widget _collapsedRow(
    NekkoTokens t,
    ServerCleanliness c,
    Color color,
    bool checking,
  ) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(14, 12, 12, 12),
      child: Row(
        children: [
          // Пульсирующая точка-индикатор вердикта.
          Container(
            width: 10,
            height: 10,
            decoration: BoxDecoration(
              color: color,
              shape: BoxShape.circle,
              boxShadow: [
                BoxShadow(color: color.withValues(alpha: 0.5), blurRadius: 6),
              ],
            ),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(
                  _verdictLabel(c.verdict),
                  style: TextStyle(
                    color: t.text,
                    fontSize: 13,
                    fontWeight: FontWeight.w700,
                  ),
                ),
                const SizedBox(height: 2),
                Text(
                  checking ? 'смотрю, как выглядит выход…' : _summaryLine(c),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(
                    color: t.textMuted,
                    fontSize: 11,
                    fontFamily: t.monoFamily,
                  ),
                ),
              ],
            ),
          ),
          const SizedBox(width: 10),
          if (!checking) _scoreBadge(t, c.score, color),
          const SizedBox(width: 6),
          AnimatedRotation(
            turns: _expanded ? 0.5 : 0,
            duration: const Duration(milliseconds: 180),
            child: Icon(Icons.expand_more, color: t.textMuted, size: 20),
          ),
        ],
      ),
    );
  }

  // Краткая строка под заголовком: страна · ASN (+ метки хостинга).
  String _summaryLine(ServerCleanliness c) {
    final parts = <String>[];
    if (c.country.isNotEmpty) parts.add(c.country);
    if (c.asname.isNotEmpty) parts.add(c.asname);
    if (c.isHosting) parts.add('датацентр');
    if (c.isMobile) parts.add('mobile');
    return parts.isEmpty ? (c.ip.isEmpty ? '—' : c.ip) : parts.join(' · ');
  }

  // Бейдж с числовым индексом чистоты 0-100.
  Widget _scoreBadge(NekkoTokens t, int score, Color color) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 4),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.14),
        borderRadius: BorderRadius.circular(8),
      ),
      child: Text(
        '$score',
        style: TextStyle(
          color: color,
          fontSize: 14,
          fontWeight: FontWeight.w800,
          fontFamily: t.monoFamily,
        ),
      ),
    );
  }

  Widget _details(NekkoTokens t, ServerCleanliness c) {
    final fraud = c.fraudScore >= 0 ? '${c.fraudScore}/100' : '— (нет ключа)';
    final cf = [
      if (c.cfColo.isNotEmpty) c.cfColo,
      if (c.cfWarp.isNotEmpty) 'warp=${c.cfWarp}',
    ].join(' · ');
    final asnLine = [
      if (c.asname.isNotEmpty) c.asname else c.asn,
      if (c.asnType.isNotEmpty) '(${c.asnType})',
      if (c.asnCountry.isNotEmpty && c.asnCountry != c.country)
        '· рег. ${c.asnCountry}',
    ].join(' ');

    return Padding(
      padding: const EdgeInsets.fromLTRB(14, 0, 14, 14),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Divider(color: t.border, height: 1),
          const SizedBox(height: 12),
          _scoreBar(t, c),

          // --- ПОЧЕМУ фродит: список конкретных причин с фиксами ---
          if (c.reasons.isNotEmpty) ...[
            const SizedBox(height: 14),
            Text(
              'почему так',
              style: TextStyle(
                color: t.textMuted,
                fontSize: 11,
                fontWeight: FontWeight.w600,
              ),
            ),
            const SizedBox(height: 8),
            for (final r in c.reasons) _reasonTile(t, r),
          ],

          const SizedBox(height: 14),
          Divider(color: t.border, height: 1),
          const SizedBox(height: 12),
          Text(
            'детали',
            style: TextStyle(
              color: t.textMuted,
              fontSize: 11,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(height: 6),
          _kv(t, 'IP', c.ip.isEmpty ? '—' : c.ip),
          _kv(t, 'ASN', asnLine),
          _kv(t, 'ISP', c.isp.isEmpty ? '—' : c.isp),
          _kv(
            t,
            'локация',
            [c.country, c.city].where((s) => s.isNotEmpty).join(' · '),
          ),
          _kvFlag(t, 'хостинг / датацентр', c.isHosting),
          _kvFlag(t, 'proxy / vpn / tor', c.isProxy || c.isVpn || c.isTor),
          _kvFlag(t, 'в базах злоупотреблений', c.isAbuser),
          if (c.abuserLabel.isNotEmpty)
            _kv(t, 'репутация подсети', c.abuserLabel),
          _kv(t, 'fraud-score (IPQS)', fraud),
          if (cf.isNotEmpty) _kv(t, 'Cloudflare', cf),
          if (c.abuseEmail.isNotEmpty) _kv(t, 'abuse-контакт', c.abuseEmail),
          if (c.fraudScore < 0) ...[
            const SizedBox(height: 8),
            Text(
              'Оценка по бесплатным источникам (ip-api + ipapi.is). Точный '
              'числовой fraud-score появится, если положить IPQS-ключ в '
              'ipqs-key.txt рядом с exe.',
              style: TextStyle(color: t.textFaint, fontSize: 10.5, height: 1.4),
            ),
          ],
        ],
      ),
    );
  }

  // Цвет по severity причины.
  Color _sevColor(NekkoTokens t, String sev) {
    switch (sev) {
      case 'high':
        return t.bad;
      case 'med':
        return t.warn;
      case 'good':
        return t.ok;
      default:
        return t.textMuted;
    }
  }

  // Одна причина: цветная точка + заголовок + (если есть) подсказка-фикс.
  Widget _reasonTile(NekkoTokens t, CleanlinessReason r) {
    final color = _sevColor(t, r.severity);
    return Padding(
      padding: const EdgeInsets.only(bottom: 10),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.only(top: 4),
            child: Container(
              width: 7,
              height: 7,
              decoration: BoxDecoration(color: color, shape: BoxShape.circle),
            ),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  r.title,
                  style: TextStyle(color: t.text, fontSize: 11.5, height: 1.35),
                ),
                if (r.fix.isNotEmpty) ...[
                  const SizedBox(height: 3),
                  Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Icon(Icons.arrow_right_alt, size: 14, color: t.textMuted),
                      const SizedBox(width: 4),
                      Expanded(
                        child: Text(
                          r.fix,
                          style: TextStyle(
                            color: t.textMuted,
                            fontSize: 10.5,
                            height: 1.35,
                          ),
                        ),
                      ),
                    ],
                  ),
                ],
              ],
            ),
          ),
        ],
      ),
    );
  }

  // Полоса индекса чистоты во всю ширину.
  Widget _scoreBar(NekkoTokens t, ServerCleanliness c) {
    final color = _verdictColor(t, c.verdict);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Text(
              'индекс чистоты',
              style: TextStyle(color: t.textMuted, fontSize: 11),
            ),
            const Spacer(),
            Text(
              '${c.score}/100',
              style: TextStyle(
                color: color,
                fontSize: 11,
                fontWeight: FontWeight.w700,
                fontFamily: t.monoFamily,
              ),
            ),
          ],
        ),
        const SizedBox(height: 6),
        ClipRRect(
          borderRadius: BorderRadius.circular(3),
          child: LinearProgressIndicator(
            value: c.score / 100.0,
            minHeight: 5,
            backgroundColor: t.bg,
            valueColor: AlwaysStoppedAnimation(color),
          ),
        ),
      ],
    );
  }

  Widget _kv(NekkoTokens t, String k, String v) => Padding(
    padding: const EdgeInsets.symmetric(vertical: 4),
    child: Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        SizedBox(
          width: 130,
          child: Text(k, style: TextStyle(color: t.textMuted, fontSize: 11.5)),
        ),
        Expanded(
          child: Text(
            v.isEmpty ? '—' : v,
            style: TextStyle(
              color: t.text,
              fontSize: 11.5,
              fontFamily: t.monoFamily,
            ),
          ),
        ),
      ],
    ),
  );

  Widget _kvFlag(NekkoTokens t, String k, bool on) => Padding(
    padding: const EdgeInsets.symmetric(vertical: 4),
    child: Row(
      children: [
        SizedBox(
          width: 130,
          child: Text(k, style: TextStyle(color: t.textMuted, fontSize: 11.5)),
        ),
        Icon(
          on ? Icons.check_circle : Icons.remove_circle_outline,
          size: 14,
          color: on ? t.warn : t.textFaint,
        ),
        const SizedBox(width: 6),
        Text(
          on ? 'да' : 'нет',
          style: TextStyle(color: on ? t.warn : t.textMuted, fontSize: 11.5),
        ),
      ],
    ),
  );
}
