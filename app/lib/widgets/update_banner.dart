import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:window_manager/window_manager.dart';

import '../state/app_state.dart';
import '../theme/tokens.dart';

/// «обновление готово · 1.0.3 → 1.0.4 · перезапустить». Shown only once an
/// update is fully downloaded and verified by HikkariService; downloading
/// happens silently.
///
/// Built like the other home-screen cards ([CurrentProfileCard],
/// [RouteSection]): a `surface` card with a hairline border, a lowercase
/// title, and the accent kept for hover — the whole card is the button. A
/// mandatory update ([UpdateStatus.mandatory]) is the one state that is
/// coloured at rest, in `warn`.
///
/// Restarting has the service swap the new version in and closes the window
/// through the normal close path (main.dart → onWindowClose); the new
/// version opens by itself and brings the tunnel back if it was up.
class UpdateBanner extends StatefulWidget {
  const UpdateBanner({super.key});

  @override
  State<UpdateBanner> createState() => _UpdateBannerState();
}

class _UpdateBannerState extends State<UpdateBanner> {
  bool _hover = false;
  bool _restarting = false;

  Future<void> _restart(AppState state) async {
    setState(() => _restarting = true);
    if (await state.restartToUpdate()) {
      await windowManager.close();
      return;
    }
    if (mounted) setState(() => _restarting = false);
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final state = context.watch<AppState>();
    final u = state.update;

    return AnimatedSize(
      duration: const Duration(milliseconds: 200),
      curve: Curves.easeOut,
      alignment: Alignment.topCenter,
      child: u.isReady
          ? _card(t, state)
          : const SizedBox(width: double.infinity),
    );
  }

  Widget _card(NekkoTokens t, AppState state) {
    final u = state.update;
    final mandatory = u.mandatory;
    final active = _hover && !_restarting;
    final accent = mandatory ? t.warn : t.primary;
    final titleColor = mandatory ? t.warn : (active ? t.primary : t.text);
    final actionColor = active || mandatory ? accent : t.textMuted;
    final borderColor = mandatory
        ? t.warn.withValues(alpha: 0.32)
        : (active ? t.primary.withValues(alpha: 0.32) : t.border);
    final from = u.installed.isEmpty ? '' : '${u.installed} → ';

    return Padding(
      // Inline with the settings icon (BrandHeader), which centres it.
      padding: EdgeInsets.zero,
      child: MouseRegion(
        cursor: _restarting
            ? SystemMouseCursors.basic
            : SystemMouseCursors.click,
        onEnter: (_) => setState(() => _hover = true),
        onExit: (_) => setState(() => _hover = false),
        child: GestureDetector(
          behavior: HitTestBehavior.opaque,
          onTap: _restarting ? null : () => _restart(state),
          child: AnimatedContainer(
            duration: const Duration(milliseconds: 150),
            // The header row's height (52, the settings icon's box), the
            // content centred in it, the same inset on both sides.
            height: 52,
            alignment: Alignment.center,
            padding: const EdgeInsets.symmetric(horizontal: 18),
            decoration: BoxDecoration(
              color: t.surface,
              border: Border.all(color: borderColor),
              borderRadius: BorderRadius.circular(t.radiusLg),
            ),
            // What fits is measured, not guessed. The arrow always shows
            // (the card is a button); then «перезапустить»; the title is
            // whole — «обновление готово», or just «обновление» when that
            // leaves no room; the versions come last.
            child: LayoutBuilder(
              builder: (context, box) {
                final base = DefaultTextStyle.of(context).style;
                final scaler = MediaQuery.textScalerOf(context);
                double width(String text, TextStyle style) {
                  final p = TextPainter(
                    text: TextSpan(text: text, style: base.merge(style)),
                    textDirection: TextDirection.ltr,
                    textScaler: scaler,
                    maxLines: 1,
                  )..layout();
                  return p.width;
                }

                final full = mandatory
                    ? 'нужно обновиться'
                    : 'обновление готово';
                final short = mandatory ? 'обновиться' : 'обновление';
                final versions = '$from${u.version}';
                const arrow = 15.0, gap = 12.0;
                // Free width each optional part must leave behind: parts
                // go well before the card gets crowded, not at the last
                // pixel.
                const air = 56.0;
                final word =
                    width(
                      _restarting ? _restartingWord : _actionWord,
                      _actionStyle,
                    ) +
                    6;
                var left = box.maxWidth - gap - arrow;
                final title = width(full, _titleStyle) + word + air <= left
                    ? full
                    : short;
                left -= width(title, _titleStyle);
                final showWord = left >= word + air;
                if (showWord) left -= word;
                final showVersions =
                    left >= width(versions, _versionStyle) + 10 + air;

                final titleText = AnimatedDefaultTextStyle(
                  duration: const Duration(milliseconds: 150),
                  style: _titleStyle.copyWith(color: titleColor),
                  child: Text(
                    title,
                    maxLines: 1,
                    softWrap: false,
                    overflow: TextOverflow.ellipsis,
                  ),
                );
                return Row(
                  children: [
                    // Measured to fit, so it takes its own width; only a
                    // window too narrow for the short title with the arrow
                    // lets it shrink (and clip).
                    if (left >= 0) titleText else Flexible(child: titleText),
                    if (showVersions) const SizedBox(width: 10),
                    if (showVersions)
                      Text(
                        versions,
                        maxLines: 1,
                        softWrap: false,
                        style: _versionStyle.copyWith(color: t.textMuted),
                      ),
                    const Spacer(),
                    const SizedBox(width: gap),
                    _action(t, actionColor, showWord),
                  ],
                );
              },
            ),
          ),
        ),
      ),
    );
  }

  static const _actionWord = 'перезапустить';
  static const _restartingWord = 'перезапуск…';
  static const _titleStyle = TextStyle(
    fontSize: 14,
    fontWeight: FontWeight.w700,
    height: 1.1,
  );
  static const _versionStyle = TextStyle(
    fontSize: NekkoTokens.sizeLabel,
    fontWeight: FontWeight.w600,
    height: 1.1,
  );
  static const _actionStyle = TextStyle(
    fontSize: NekkoTokens.sizeLabel,
    fontWeight: FontWeight.w500,
  );

  Widget _action(NekkoTokens t, Color color, bool showWord) {
    if (_restarting) {
      return Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          SizedBox(
            width: 12,
            height: 12,
            child: CircularProgressIndicator(
              strokeWidth: 1.5,
              color: t.textMuted,
            ),
          ),
          if (showWord) const SizedBox(width: 8),
          if (showWord)
            Text(
              _restartingWord,
              style: _actionStyle.copyWith(color: t.textMuted),
            ),
        ],
      );
    }
    return TweenAnimationBuilder<Color?>(
      tween: ColorTween(end: color),
      duration: const Duration(milliseconds: 150),
      builder: (_, c, _) => Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (showWord)
            Text(_actionWord, style: _actionStyle.copyWith(color: c)),
          if (showWord) const SizedBox(width: 6),
          Icon(Icons.arrow_forward, size: 15, color: c),
        ],
      ),
    );
  }
}
