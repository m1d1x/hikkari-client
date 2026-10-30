import 'package:flutter/material.dart';

import '../core/models.dart';
import '../state/uptime_stats.dart';
import '../theme/tokens.dart';
import 'sparkline.dart';

class ProfileSummaryCard extends StatelessWidget {
  final String title;
  final List<PingSample> samples;
  final bool selected;
  final VoidCallback? onTap;
  // Отдельный колбэк для правой зоны карточки (ms + трейлинг-иконка).
  // Если не задан — правая зона просто наследует [onTap], как и раньше
  // (единая кликабельная карточка). Задаётся, когда правая часть должна
  // открывать/сворачивать подробности, а не выполнять основное действие
  // карточки (например, переключение профиля).
  final VoidCallback? onTrailingTap;
  final Widget? leading;
  final Widget? trailing;
  final Widget? footer;
  final bool footerVisible;
  final EdgeInsetsGeometry margin;
  final EdgeInsetsGeometry padding;
  // Заголовок и блок с ms «дышат» от min до max: при узком окне ужима-
  // ются до min (заголовок — ellipsis, ms — пересчитывает ширину под
  // текущий текст), при широком — стопорятся на max, чтобы график за-
  // бирал весь оставшийся «лишний» пиксель окна.
  final double minTitleWidth;
  final double maxTitleWidth;
  final double minRttWidth;
  final double maxRttWidth;
  final double trailingWidth;
  final double leadingWidth;
  final double titleFontSize;
  final FontWeight titleWeight;
  final double chartHeight;
  // Set by the caller right after a VPN start / profile switch (see
  // AppState.isWarmingUp) — forces the "checking" state for a few seconds
  // so the tunnel's own startup hiccup doesn't read as "down".
  final bool forceChecking;

  const ProfileSummaryCard({
    super.key,
    required this.title,
    required this.samples,
    required this.selected,
    this.onTap,
    this.onTrailingTap,
    this.leading,
    this.trailing,
    this.footer,
    this.footerVisible = false,
    this.margin = EdgeInsets.zero,
    this.padding = const EdgeInsets.fromLTRB(14, 12, 10, 12),
    this.minTitleWidth = 96,
    this.maxTitleWidth = 280,
    this.minRttWidth = 44,
    this.maxRttWidth = 110,
    this.trailingWidth = 18,
    this.leadingWidth = 18,
    this.titleFontSize = 14,
    this.titleWeight = FontWeight.w600,
    this.chartHeight = 30,
    this.forceChecking = false,
  });

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final rtt = UptimeStats.avgRtt(samples);
    final lineColor = selected ? t.primary : t.textMuted;
    final bgColor = selected ? const Color(0xFF1A1014) : t.surface;
    final borderColor = selected ? t.primary.withValues(alpha: 0.32) : t.border;
    // Три состояния, чтобы первые секунды после старта не выглядели как
    // "всё мертво":
    //   checking — проб ещё мало (история пустая или < 3 сэмплов).
    //              Карточка яркая, в углу "…", сортируется ниже рабочих,
    //              но выше мёртвых.
    //   dead     — есть достаточная история и в ней либо нет успешных
    //              проб, либо средний rtt за ~2 минуты > 1000ms.
    //              Гасим до 45%, в углу "down".
    //   ok       — обычное число ms.
    final recent = UptimeStats.recentAvgRtt(samples);
    final checking = forceChecking || samples.length < 3;
    final dead = !checking && (recent < 0 || recent > 1000);

    return Padding(
      padding: margin,
      child: Opacity(
        opacity: dead ? 0.45 : 1.0,
        child: Material(
        color: bgColor,
        borderRadius: BorderRadius.circular(t.radiusLg),
        clipBehavior: Clip.antiAlias,
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 150),
          decoration: BoxDecoration(
            border: Border.all(color: borderColor),
            borderRadius: BorderRadius.circular(t.radiusLg),
          ),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              _adaptiveRow(
                t: t,
                textScaler: MediaQuery.textScalerOf(context),
                leading: leading,
                title: title,
                titleFontSize: titleFontSize,
                titleWeight: titleWeight,
                rtt: rtt,
                dead: dead,
                checking: checking,
                selected: selected,
                samples: samples,
                lineColor: lineColor,
              ),
              if (footer != null)
                AnimatedSize(
                  duration: const Duration(milliseconds: 180),
                  curve: AppCurves.fastInSlowOut,
                  alignment: Alignment.topCenter,
                  child: footerVisible
                      ? Column(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            Divider(height: 1, color: borderColor),
                            footer!,
                          ],
                        )
                      : const SizedBox.shrink(),
                ),
            ],
          ),
        ),
      ),
      ),
    );
  }

  // Layout-builder-based adaptive row.
  //
  // Стандартный Flexible/Expanded в Row плохо ведёт себя для нашего случая:
  // если Flexible-ребёнок (заголовок) использует меньше своей flex-доли,
  // освободившееся место не достаётся Expanded'у соседу (графику) —
  // оно остаётся «мёртвым» в конце Row. Поэтому считаем ширины руками.
  Widget _adaptiveRow({
    required NekkoTokens t,
    required TextScaler textScaler,
    Widget? leading,
    required String title,
    required double titleFontSize,
    required FontWeight titleWeight,
    required int rtt,
    required bool dead,
    required bool checking,
    required bool selected,
    required List<PingSample> samples,
    required Color lineColor,
  }) {
    final titleStyle = TextStyle(
      color: t.textSecondary,
      fontFamily: t.monoFamily,
      fontSize: titleFontSize,
      fontWeight: titleWeight,
      height: 1.1,
    );
    final rttColor = dead || checking
        ? t.textMuted
        : (selected ? t.text : t.textMuted);
    final rttWeight = dead || checking
        ? FontWeight.w500
        : (selected ? FontWeight.w700 : FontWeight.w600);
    final rttStyle = TextStyle(
      color: rttColor,
      fontFamily: t.monoFamily,
      fontSize: 12,
      fontWeight: rttWeight,
      height: 1.05,
    );
    final rttSpan = TextSpan(
      text: dead
          ? 'down'
          : checking
              ? '…'
              : '$rtt',
      children: dead || checking
          ? null
          : [
              TextSpan(
                text: ' ms',
                style: TextStyle(
                  color: t.textMuted.withValues(alpha: 0.62),
                  fontWeight: FontWeight.w500,
                ),
              ),
            ],
      style: rttStyle,
    );

    // Меряем НАТУРАЛЬНУЮ ширину (maxWidth: infinity), потом сами
    // зажимаем в max. Плюс +2px буфер на случай округления / snap'а в
    // глифовых метриках — иначе SizedBox может оказаться на пиксель уже,
    // чем настоящая отрисовка, и Text покажет ellipsis.
    // Меряем НАТУРАЛЬНУЮ ширину (maxWidth: infinity), c учётом текущего
    // textScaler — иначе на DPI ≠ 100% Text при отрисовке оказывается
    // шире замера и обрезается ellipsis'ом. +6px буфер на всякий снап
    // глифов / неточность font metrics при первом фрейме.
    double measure(TextSpan span, double cap) {
      final tp = TextPainter(
        text: span,
        textDirection: TextDirection.ltr,
        maxLines: 1,
        textScaler: textScaler,
      )..layout(minWidth: 0, maxWidth: double.infinity);
      final natural = tp.size.width.ceilToDouble() + 6;
      return natural > cap ? cap : natural;
    }

    final resolvedPadding = padding.resolve(TextDirection.ltr);

    return LayoutBuilder(
      builder: (ctx, c) {
        final fullW = c.maxWidth;
        const gapLead = 8.0; // leading (flag) → title
        const gapL = 12.0; // title → chart
        const gapR = 10.0; // chart → rtt
        const gapT = 6.0; // rtt → trailing
        final trailingW = trailing == null ? 0.0 : trailingWidth;
        final leadingW = leading == null ? 0.0 : leadingWidth;
        final gapsTotal = gapL +
            gapR +
            (trailing == null ? 0 : gapT) +
            (leading == null ? 0 : gapLead);
        final hPad = resolvedPadding.left + resolvedPadding.right;

        // Желаемая ширина rtt-блока — его натуральная, ограниченная max.
        final rttWanted = measure(rttSpan, maxRttWidth);
        // Желаемая ширина заголовка — натуральная, ограниченная max.
        final titleWanted = measure(
          TextSpan(text: title, style: titleStyle),
          maxTitleWidth,
        );

        // Считаем, помещается ли при их «хотелках» график шириной ≥ 60.
        // Если нет — поочерёдно ужимаем title (до 50% натурального) и rtt
        // (до 30%). График всегда берёт РОВНО остаток, чтобы никогда не
        // вылезти за ширину контейнера.
        const chartMin = 60.0;
        var titleW = titleWanted;
        var rttW = rttWanted;
        final budget = fullW - hPad - trailingW - leadingW - gapsTotal;
        var overflow = titleW + rttW + chartMin - budget;
        if (overflow > 0) {
          final titleSlack = titleW * 0.5;
          final tCut = overflow > titleSlack ? titleSlack : overflow;
          titleW -= tCut;
          overflow -= tCut;
        }
        if (overflow > 0) {
          final rttSlack = rttW * 0.3;
          final rCut = overflow > rttSlack ? rttSlack : overflow;
          rttW -= rCut;
          overflow -= rCut;
        }
        var chartW = budget - titleW - rttW;
        if (chartW < 0) chartW = 0;

        // Trim the font's built-in ascent/descent padding so the text's
        // visual center lines up with the sparkline and trailing icon —
        // otherwise the extra line-height space above digits/letters makes
        // the row look top-heavy next to icon-based siblings.
        const trimmedHeight = TextHeightBehavior(
          applyHeightToFirstAscent: false,
          applyHeightToLastDescent: false,
        );
        final row = Row(
          crossAxisAlignment: CrossAxisAlignment.center,
          children: [
            if (leading != null) ...[
              SizedBox(width: leadingW, child: Center(child: leading)),
              const SizedBox(width: gapLead),
            ],
            SizedBox(
              width: titleW,
              child: Text(
                title,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: titleStyle,
                textHeightBehavior: trimmedHeight,
              ),
            ),
            const SizedBox(width: gapL),
            SizedBox(
              width: chartW,
              height: chartHeight,
              child: Sparkline(samples: samples, color: lineColor),
            ),
            const SizedBox(width: gapR),
            SizedBox(
              width: rttW,
              child: Text.rich(
                rttSpan,
                textAlign: TextAlign.right,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                textHeightBehavior: trimmedHeight,
              ),
            ),
            if (trailing != null) ...[
              const SizedBox(width: gapT),
              trailing!,
            ],
          ],
        );

        // Один общий InkWell (единый hover/ripple на всю карточку, без
        // визуального шва посередине), но какой колбэк дёргать — решаем
        // по X-координате тапа: правее середины графика (ms + стрелочка)
        // уходит на [onTrailingTap], иначе — обычное [onTap]. Так вся
        // правая часть карточки, а не только 34px-иконка, разворачивает
        // подробности — это удобнее пальцем на телефоне.
        final splitX = resolvedPadding.left +
            leadingW +
            (leading == null ? 0 : gapLead) +
            titleW +
            gapL +
            chartW +
            gapR / 2;
        final hasAnyTap = onTap != null || onTrailingTap != null;
        var tapX = 0.0;

        return Material(
          color: Colors.transparent,
          child: InkWell(
            onTapDown: hasAnyTap ? (d) => tapX = d.localPosition.dx : null,
            onTap: hasAnyTap
                ? () {
                    final cb = (onTrailingTap != null && tapX >= splitX)
                        ? onTrailingTap
                        : onTap;
                    (cb ?? onTap ?? onTrailingTap)?.call();
                  }
                : null,
            mouseCursor: hasAnyTap
                ? SystemMouseCursors.click
                : SystemMouseCursors.basic,
            hoverColor:
                hasAnyTap ? t.primary.withValues(alpha: 0.06) : Colors.transparent,
            child: Padding(padding: resolvedPadding, child: row),
          ),
        );
      },
    );
  }
}
