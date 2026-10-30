import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:window_manager/window_manager.dart';

import '../state/app_state.dart';
import '../theme/tokens.dart';
import '../widgets/window_titlebar.dart';

/// Shown instead of the app while the installed version is below the
/// channel's `minimum_version` ([UpdateStatus.blocks]): «нужно обновиться»,
/// the versions, the download's progress and, once the update is staged, one
/// action — «перезапустить».
///
/// The tunnel stays reachable from here: the update is fetched through it
/// when it is up, and the update domain may be blocked without it.
///
/// Built like [UpdateBanner]: a `surface` card with a hairline border, a
/// lowercase title, IBM Plex Mono throughout, `warn` for the mandatory state
/// and the accent only on hover. What fits is measured, not guessed.
class MandatoryUpdateScreen extends StatelessWidget {
  const MandatoryUpdateScreen({super.key});

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return Scaffold(
      backgroundColor: t.bg,
      body: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          const WindowTitlebar(),
          Expanded(
            child: Center(
              child: SingleChildScrollView(
                padding: const EdgeInsets.all(16),
                child: ConstrainedBox(
                  constraints: const BoxConstraints(maxWidth: 380),
                  child: const Column(
                    mainAxisSize: MainAxisSize.min,
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      _UpdateCard(),
                      SizedBox(height: 12),
                      _TunnelRow(),
                    ],
                  ),
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _UpdateCard extends StatelessWidget {
  const _UpdateCard();

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final u = context.watch<AppState>().update;
    final versions = [
      if (u.installed.isNotEmpty) u.installed,
      if (u.version.isNotEmpty) u.version,
    ].join(' → ');

    return Container(
      padding: const EdgeInsets.fromLTRB(20, 18, 20, 20),
      decoration: BoxDecoration(
        color: t.surface,
        border: Border.all(color: t.warn.withValues(alpha: 0.32)),
        borderRadius: BorderRadius.circular(t.radiusLg),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          LayoutBuilder(
            builder: (context, box) {
              // The title is whole: the long form when it fits beside the
              // versions' room, else the short one.
              const full = 'нужно обновиться';
              const short = 'обновиться';
              final title = _fits(context, full, _titleStyle, box.maxWidth)
                  ? full
                  : short;
              return Text(
                title,
                maxLines: 1,
                softWrap: false,
                overflow: TextOverflow.ellipsis,
                style: _titleStyle.copyWith(color: t.warn),
              );
            },
          ),
          const SizedBox(height: 8),
          Text(
            u.isReady
                ? 'эта версия больше не поддерживается. '
                      'новая уже скачана — перезапустите приложение.'
                : 'эта версия больше не поддерживается. '
                      'новая скачивается сама, затем приложение '
                      'перезапустится.',
            style: TextStyle(
              fontSize: NekkoTokens.sizeBody,
              height: 1.45,
              color: t.textMuted,
            ),
          ),
          if (versions.isNotEmpty) ...[
            const SizedBox(height: 14),
            Text(
              versions,
              maxLines: 1,
              softWrap: false,
              overflow: TextOverflow.fade,
              style: TextStyle(
                fontSize: NekkoTokens.sizeLabel,
                fontWeight: FontWeight.w600,
                color: t.textSecondary,
              ),
            ),
          ],
          const SizedBox(height: 16),
          u.isReady ? const _RestartButton() : const _Progress(),
        ],
      ),
    );
  }

  static const _titleStyle = TextStyle(
    fontSize: NekkoTokens.sizeSectionTitle,
    fontWeight: FontWeight.w700,
    height: 1.1,
  );
}

/// Download progress, or what went wrong (the service retries by itself).
class _Progress extends StatelessWidget {
  const _Progress();

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final u = context.watch<AppState>().update;
    final failed = u.state == 'error';
    final fraction = u.bytesTotal > 0
        ? (u.bytesDone / u.bytesTotal).clamp(0.0, 1.0)
        : null;
    final caption = failed
        ? 'не удалось скачать — попробуем снова. '
              'если сайт обновлений недоступен, включите vpn.'
        : u.bytesTotal > 0
        ? 'скачивается · ${_mb(u.bytesDone)} из ${_mb(u.bytesTotal)}'
        : 'скачивается…';

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      mainAxisSize: MainAxisSize.min,
      children: [
        ClipRRect(
          borderRadius: BorderRadius.circular(2),
          child: LinearProgressIndicator(
            minHeight: 3,
            value: failed ? 0 : fraction,
            backgroundColor: t.border,
            color: t.warn,
          ),
        ),
        const SizedBox(height: 8),
        Text(
          caption,
          style: TextStyle(
            fontSize: NekkoTokens.sizeCaption,
            height: 1.4,
            color: failed ? t.warn : t.textMuted,
          ),
        ),
      ],
    );
  }

  static String _mb(int bytes) =>
      '${(bytes / (1 << 20)).toStringAsFixed(1)} мб';
}

/// «перезапустить →»: the whole row is the button, `warn` at rest, the
/// accent on hover. Restarting closes the window the normal way; the new
/// version opens by itself (see [UpdateBanner]).
class _RestartButton extends StatefulWidget {
  const _RestartButton();

  @override
  State<_RestartButton> createState() => _RestartButtonState();
}

class _RestartButtonState extends State<_RestartButton> {
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
    final state = context.read<AppState>();
    final active = _hover && !_restarting;
    final color = _restarting ? t.textMuted : (active ? t.primary : t.warn);

    return MouseRegion(
      cursor: _restarting ? SystemMouseCursors.basic : SystemMouseCursors.click,
      onEnter: (_) => setState(() => _hover = true),
      onExit: (_) => setState(() => _hover = false),
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: _restarting ? null : () => _restart(state),
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 150),
          height: 44,
          padding: const EdgeInsets.symmetric(horizontal: 16),
          decoration: BoxDecoration(
            color: t.surface2,
            border: Border.all(
              color: (active ? t.primary : t.warn).withValues(alpha: 0.32),
            ),
            borderRadius: BorderRadius.circular(t.radius),
          ),
          child: LayoutBuilder(
            builder: (context, box) {
              final word = _restarting ? 'перезапуск…' : 'перезапустить';
              // The icon always shows; the word only when it fits whole.
              final showWord = _fits(
                context,
                word,
                _actionStyle,
                box.maxWidth - 15 - 8,
              );
              return Row(
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  if (_restarting)
                    SizedBox(
                      width: 12,
                      height: 12,
                      child: CircularProgressIndicator(
                        strokeWidth: 1.5,
                        color: t.textMuted,
                      ),
                    ),
                  if (_restarting && showWord) const SizedBox(width: 8),
                  if (showWord)
                    Text(word, style: _actionStyle.copyWith(color: color)),
                  if (!_restarting) ...[
                    if (showWord) const SizedBox(width: 8),
                    Icon(Icons.arrow_forward, size: 15, color: color),
                  ],
                ],
              );
            },
          ),
        ),
      ),
    );
  }

  static const _actionStyle = TextStyle(
    fontSize: NekkoTokens.sizeLabel,
    fontWeight: FontWeight.w600,
  );
}

/// «vpn · выключен · включить»: the tunnel stays usable while the app is
/// blocked, so the update can come through it.
class _TunnelRow extends StatefulWidget {
  const _TunnelRow();

  @override
  State<_TunnelRow> createState() => _TunnelRowState();
}

class _TunnelRowState extends State<_TunnelRow> {
  bool _hover = false;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final s = context.watch<AppState>();
    final running = s.isRunning;
    final busy = s.busy;
    final action = busy ? '…' : (running ? 'выключить' : 'включить');
    final actionColor = busy ? t.textFaint : (_hover ? t.primary : t.textMuted);

    return MouseRegion(
      cursor: busy ? SystemMouseCursors.basic : SystemMouseCursors.click,
      onEnter: (_) => setState(() => _hover = true),
      onExit: (_) => setState(() => _hover = false),
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: busy ? null : s.toggleVpn,
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 150),
          height: 44,
          padding: const EdgeInsets.symmetric(horizontal: 16),
          decoration: BoxDecoration(
            color: t.surface,
            border: Border.all(
              color: _hover && !busy
                  ? t.primary.withValues(alpha: 0.32)
                  : t.border,
            ),
            borderRadius: BorderRadius.circular(t.radiusLg),
          ),
          child: Row(
            children: [
              Container(
                width: 6,
                height: 6,
                decoration: BoxDecoration(
                  shape: BoxShape.circle,
                  color: running ? t.ok : t.textFaint,
                ),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Text(
                  running ? 'vpn включён' : 'vpn выключен',
                  maxLines: 1,
                  softWrap: false,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(
                    fontSize: NekkoTokens.sizeLabel,
                    fontWeight: FontWeight.w600,
                    color: t.text,
                  ),
                ),
              ),
              const SizedBox(width: 12),
              Text(
                action,
                style: TextStyle(
                  fontSize: NekkoTokens.sizeLabel,
                  fontWeight: FontWeight.w500,
                  color: actionColor,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// Whether [text] fits in [width] on one line, in the ambient style.
bool _fits(BuildContext context, String text, TextStyle style, double width) {
  final p = TextPainter(
    text: TextSpan(
      text: text,
      style: DefaultTextStyle.of(context).style.merge(style),
    ),
    textDirection: TextDirection.ltr,
    textScaler: MediaQuery.textScalerOf(context),
    maxLines: 1,
  )..layout();
  return p.width <= width;
}
