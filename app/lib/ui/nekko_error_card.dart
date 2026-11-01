import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';

import '../state/app_state.dart';
import '../theme/tokens.dart';
import 'copy_flash.dart';
import 'nekko_toast.dart';

/// Shared Nekko-styled diagnostic error card with hover «скопировать» hint
/// and springy [CopyFlash] confirmation when clicked.
class NekkoErrorCard extends StatefulWidget {
  const NekkoErrorCard({
    super.key,
    required this.summary,
    required this.title,
    this.details,
    this.onCopy,
    this.tokens,
    this.maxLines,
  });

  final String summary;
  final String title;

  /// Full diagnostic text copied to the clipboard when [onCopy] is not set.
  /// Defaults to [summary].
  final String? details;

  /// Optional custom copy callback; when omitted, copies [details] ?? [summary]
  /// into [Clipboard].
  final Future<void> Function()? onCopy;

  final NekkoTokens? tokens;

  /// Optional line limit for [summary] when rendered in tight spaces.
  final int? maxLines;

  /// Condenses a raw multi-line engine or HTTP error into a concise summary
  /// suitable for the card body while keeping the full text for clipboard copy.
  static String summarize(String details) {
    final httpStatus = RegExp(
      r'HTTP\s+(\d{3})',
      caseSensitive: false,
    ).firstMatch(details)?.group(1);
    if (httpStatus != null) {
      return 'Сервер подписки вернул HTTP $httpStatus. Проверьте ссылку или попробуйте позже.';
    }

    final firstLine = details
        .replaceFirst(
          RegExp(r'^(?:NekkoboxError|Exception|StateError):\s*'),
          '',
        )
        .trim()
        .split('\n')
        .first
        .trim();
    if (firstLine.length <= 120) return firstLine;
    return '${firstLine.substring(0, 117).trimRight()}…';
  }

  @override
  State<NekkoErrorCard> createState() => _NekkoErrorCardState();
}

class _NekkoErrorCardState extends State<NekkoErrorCard> {
  static final Map<String, DateTime> _recentlyReported = {};

  final _flash = GlobalKey<CopyFlashState>();
  bool _hovered = false;
  bool _flashing = false;
  String _flashLabel = 'скопировано';

  Future<void> _copy() async {
    // One confirmation at a time: taps while it plays are ignored instead of
    // restarting it mid-flight.
    if (_flashing) return;
    setState(() => _flashing = true);

    final text = widget.details ?? widget.summary;
    // The report goes first and on its own: copying and the confirmation
    // animation must not decide whether support hears about the error. The
    // same error is reported at most once per 5 minutes.
    final now = DateTime.now();
    final last = _recentlyReported[text];
    if (last == null || now.difference(last) >= const Duration(minutes: 5)) {
      _recentlyReported[text] = now;
      unawaited(_report(context.read<AppState>()));
    }

    if (widget.onCopy != null) {
      await widget.onCopy!();
    } else {
      await Clipboard.setData(ClipboardData(text: text));
    }

    final flash = _flash.currentState;
    if (!mounted) return;
    if (flash == null) {
      setState(() => _flashing = false);
      return;
    }
    // The hover hint steps aside for the whole confirmation, so «скопировать»
    // never shares the card with «скопировано».
    flash.animation.removeStatusListener(_onFlashStatus);
    flash.animation.addStatusListener(_onFlashStatus);
    flash.flash();
  }

  /// Hands the error to support and says so. The engine queues the report on
  /// disk and delivers it whenever the hub is reachable, so the only failure
  /// left is a report that could not be saved.
  Future<void> _report(AppState appState) async {
    var sent = true;
    try {
      await appState.submitSupportReport(
        'Ошибка подключения: ${widget.title} — ${widget.summary}',
      );
    } catch (_) {
      sent = false;
      // Not sent: a retap within the dedup window must try again.
      _recentlyReported.remove(widget.details ?? widget.summary);
    }
    if (!mounted) return;
    NekkoToast.show(
      context,
      sent ? 'передано поддержке' : 'не удалось сохранить отчёт',
      icon: sent ? Icons.check_rounded : Icons.error_outline_rounded,
      hold: NekkoToast.glanceHold,
    );
  }

  void _onFlashStatus(AnimationStatus status) {
    if (status == AnimationStatus.dismissed && mounted) {
      setState(() => _flashing = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = widget.tokens ?? context.tokens;
    // The card stays dark through the whole confirmation, not just on hover,
    // so the error text never shows through «скопировано».
    final dark = _hovered || _flashing;
    final hint = _hovered && !_flashing;
    final surface = t.bad.withValues(alpha: 0.07);
    return Semantics(
      container: true,
      button: true,
      label: 'скопировать текст ошибки',
      child: MouseRegion(
        cursor: SystemMouseCursors.click,
        onEnter: (_) => setState(() => _hovered = true),
        onExit: (_) => setState(() => _hovered = false),
        child: GestureDetector(
          behavior: HitTestBehavior.opaque,
          onTap: _copy,
          // The whole card, background included, recedes under «скопировано».
          child: CopyFlash(
            key: _flash,
            color: t.bad,
            label: _flashLabel,
            iconSize: 20,
            fontSize: 15,
            bodyOpacity: 1,
            child: AnimatedContainer(
              duration: AppDurations.normal,
              curve: AppCurves.fastInSlowOut,
              decoration: BoxDecoration(
                color: dark
                    ? Color.alphaBlend(
                        Colors.black.withValues(alpha: 0.6),
                        surface,
                      )
                    : surface,
                border: Border.all(color: t.bad.withValues(alpha: 0.24)),
                borderRadius: BorderRadius.circular(t.radius),
              ),
              // The hint centres on the whole card, not the padded content box,
              // so it sits at equal distance from the top and bottom edges.
              child: Stack(
                alignment: Alignment.center,
                children: [
                  Padding(
                    padding: const EdgeInsets.fromLTRB(14, 12, 14, 10),
                    child: AnimatedOpacity(
                      opacity: dark ? 0.08 : 1,
                      duration: AppDurations.normal,
                      curve: AppCurves.fastInSlowOut,
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        crossAxisAlignment: CrossAxisAlignment.stretch,
                        children: [
                          Icon(
                            Icons.error_outline_rounded,
                            color: t.bad,
                            size: 20,
                          ),
                          const SizedBox(height: 5),
                          Text(
                            widget.title.toLowerCase(),
                            textAlign: TextAlign.center,
                            style: TextStyle(
                              color: t.bad,
                              fontSize: 12,
                              fontWeight: FontWeight.w700,
                            ),
                          ),
                          const SizedBox(height: 5),
                          Text(
                            widget.summary.toLowerCase(),
                            textAlign: TextAlign.center,
                            maxLines: widget.maxLines,
                            overflow: widget.maxLines != null
                                ? TextOverflow.ellipsis
                                : null,
                            style: TextStyle(
                              color: t.textMuted,
                              fontSize: 12,
                              fontWeight: FontWeight.w500,
                              height: 1.4,
                            ),
                          ),
                        ],
                      ),
                    ),
                  ),
                  Positioned.fill(
                    child: IgnorePointer(
                      child: ExcludeSemantics(
                        child: AnimatedOpacity(
                          opacity: hint ? 1 : 0,
                          duration: _flashing
                              ? AppDurations.hover
                              : AppDurations.normal,
                          curve: AppCurves.fastInSlowOut,
                          child: Center(
                            child: Row(
                              mainAxisAlignment: MainAxisAlignment.center,
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                Icon(
                                  Icons.send_rounded,
                                  size: 15,
                                  color: t.bad,
                                ),
                                const SizedBox(width: 6),
                                Flexible(
                                  child: Text(
                                    'отправить поддержке',
                                    maxLines: 1,
                                    overflow: TextOverflow.ellipsis,
                                    textHeightBehavior:
                                        const TextHeightBehavior(
                                          applyHeightToFirstAscent: false,
                                          applyHeightToLastDescent: false,
                                          leadingDistribution:
                                              TextLeadingDistribution.even,
                                        ),
                                    style: TextStyle(
                                      color: t.bad,
                                      height: 1,
                                      fontSize: 14,
                                      fontWeight: FontWeight.w600,
                                    ),
                                  ),
                                ),
                              ],
                            ),
                          ),
                        ),
                      ),
                    ),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}
