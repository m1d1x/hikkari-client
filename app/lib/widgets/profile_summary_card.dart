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
  final Widget? trailing;
  final Widget? footer;
  final bool footerVisible;
  final EdgeInsetsGeometry margin;
  final EdgeInsetsGeometry padding;
  final int titleFlex;
  final int chartFlex;
  final double titleFontSize;
  final FontWeight titleWeight;
  final double chartHeight;

  const ProfileSummaryCard({
    super.key,
    required this.title,
    required this.samples,
    required this.selected,
    this.onTap,
    this.trailing,
    this.footer,
    this.footerVisible = false,
    this.margin = EdgeInsets.zero,
    this.padding = const EdgeInsets.fromLTRB(14, 12, 10, 12),
    this.titleFlex = 3,
    this.chartFlex = 4,
    this.titleFontSize = 14,
    this.titleWeight = FontWeight.w600,
    this.chartHeight = 30,
  });

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final rtt = UptimeStats.avgRtt(samples);
    final lineColor = selected ? t.primary : t.textMuted;
    final bgColor = selected ? const Color(0xFF1A1014) : t.surface;
    final borderColor = selected ? t.primary.withValues(alpha: 0.32) : t.border;

    return Padding(
      padding: margin,
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
              InkWell(
                onTap: onTap,
                mouseCursor: onTap == null
                    ? SystemMouseCursors.basic
                    : SystemMouseCursors.click,
                child: Padding(
                  padding: padding,
                  child: Row(
                    children: [
                      Expanded(
                        flex: titleFlex,
                        child: Text(
                          title,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(
                            color: t.text,
                            fontFamily: t.monoFamily,
                            fontSize: titleFontSize,
                            fontWeight: titleWeight,
                            height: 1.1,
                          ),
                        ),
                      ),
                      const SizedBox(width: 12),
                      Expanded(
                        flex: chartFlex,
                        child: SizedBox(
                          height: chartHeight,
                          child: Sparkline(samples: samples, color: lineColor),
                        ),
                      ),
                      const SizedBox(width: 10),
                      SizedBox(
                        width: 64,
                        child: Text.rich(
                          TextSpan(
                            text: rtt < 0 ? '-' : '$rtt',
                            children: rtt < 0
                                ? null
                                : [
                                    TextSpan(
                                      text: ' ms',
                                      style: TextStyle(
                                        color:
                                            t.textMuted.withValues(alpha: 0.62),
                                        fontWeight: FontWeight.w500,
                                      ),
                                    ),
                                  ],
                          ),
                          textAlign: TextAlign.right,
                          style: TextStyle(
                            color: selected ? t.text : t.textMuted,
                            fontFamily: t.monoFamily,
                            fontSize: 12,
                            fontWeight:
                                selected ? FontWeight.w700 : FontWeight.w600,
                            height: 1.05,
                          ),
                        ),
                      ),
                      if (trailing != null) ...[
                        const SizedBox(width: 6),
                        trailing!,
                      ],
                    ],
                  ),
                ),
              ),
              if (footer != null)
                AnimatedSize(
                  duration: const Duration(milliseconds: 180),
                  curve: Curves.easeOut,
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
    );
  }
}
