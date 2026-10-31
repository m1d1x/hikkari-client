import 'dart:async';
import 'dart:math' show pi;

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';

import '../state/app_state.dart';
import '../theme/tokens.dart';
import '../ui/copy_flash.dart';
import '../ui/nekko_dialog.dart';
import '../ui/nekko_dialog_action.dart';
import '../ui/nekko_dialog_field.dart';

/// Imports server links or HTTP(S) subscriptions containing multiple servers.
/// Delegates to AppState.addProfileFromUri and closes
/// itself on success. Failures use the shared Nekko-style toast; diagnostic
/// details remain available to copy without taking over the dialog.
class ImportProfileDialog extends StatefulWidget {
  const ImportProfileDialog({super.key});

  static Future<void> show(BuildContext context) {
    final state = context.read<AppState>();
    return showDialog<void>(
      context: context,
      barrierColor: Colors.black54,
      builder: (_) => ChangeNotifierProvider<AppState>.value(
        value: state,
        child: const ImportProfileDialog(),
      ),
    );
  }

  @override
  State<ImportProfileDialog> createState() => _ImportProfileDialogState();
}

class _ImportProfileDialogState extends State<ImportProfileDialog> {
  final _ctrl = TextEditingController();
  String? _error;
  String? _errorSummary;
  String _errorTitle = 'Не удалось импортировать профиль';
  bool _submitting = false;
  int? _added;
  String _lastObservedText = '';

  @override
  void dispose() {
    _ctrl.dispose();
    super.dispose();
  }

  Future<void> _pasteFromClipboard() async {
    final data = await Clipboard.getData(Clipboard.kTextPlain);
    final text = data?.text?.trim();
    if (!mounted) return;
    if (text != null && text.isNotEmpty) {
      setState(() {
        _ctrl.text = text;
        _lastObservedText = text;
        _error = null;
        _errorSummary = null;
        _errorTitle = 'Не удалось импортировать профиль';
      });
      await _submit();
    }
  }

  void _onChanged(String value) {
    final previous = _lastObservedText;
    _lastObservedText = value;
    if (_error != null) {
      setState(() {
        _error = null;
        _errorSummary = null;
        _errorTitle = 'Не удалось импортировать профиль';
      });
    }
    final insertedText = value.length - previous.length;
    // Keyboard paste inserts a full URI at once; don't auto-submit ordinary
    // typing or edits to an existing value.
    if (insertedText >= 8 && _isSupportedLink(value.trim())) {
      _submit();
    }
  }

  bool _isSupportedLink(String value) =>
      value.startsWith('https://') ||
      value.startsWith('http://') ||
      value.startsWith('vless://') ||
      value.startsWith('trojan://') ||
      value.startsWith('ss://') ||
      value.startsWith('hysteria2://');

  Future<void> _submit() async {
    if (_submitting) return;
    final uri = _ctrl.text.trim();
    if (uri.isEmpty) {
      setState(() {
        _errorTitle = 'Нужна ссылка';
        _error = 'Вставьте ссылку для импорта.';
        _errorSummary = 'Вставьте ссылку для импорта.';
      });
      return;
    }
    setState(() {
      _submitting = true;
      _error = null;
      _errorSummary = null;
      _errorTitle = 'Не удалось импортировать профиль';
    });
    final state = context.read<AppState>();
    final countBefore = state.profiles.length;
    final name = await state.addProfileFromUri(uri);
    if (!mounted) return;
    if (name != null) {
      final added = state.profiles.length - countBefore;
      setState(() => _added = added);
      await Future<void>.delayed(const Duration(milliseconds: 1800));
      if (!mounted) return;
      Navigator.of(context).pop();
    } else {
      setState(() {
        _submitting = false;
        _error = state.lastError ?? 'не удалось добавить';
        _errorSummary = _importErrorSummary(_error!);
        _errorTitle = 'Не удалось импортировать профиль';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return NekkoDialog(
      title: 'импорт по ссылке',
      content: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        mainAxisSize: MainAxisSize.min,
        children: [
          _input(t),
          if (_error != null) ...[
            const SizedBox(height: 12),
            _ImportError(
              summary: _errorSummary ?? _error!,
              title: _errorTitle,
              onCopy: _copyError,
              tokens: t,
            ),
          ],
          if (kDebugMode && _error == null && _added == null) ...[
            const SizedBox(height: 4),
            Align(
              alignment: Alignment.centerRight,
              child: TextButton.icon(
                onPressed: _submitting ? null : _showSyntheticError,
                icon: const Icon(Icons.science_outlined, size: 14),
                label: const Text('показать пример ошибки'),
              ),
            ),
          ],
        ],
      ),
      actions: _actions(t),
      outcome: _added == null ? null : _ImportOutcome(added: _added!),
    );
  }

  void _showSyntheticError() {
    setState(() {
      _errorTitle = 'Не удалось импортировать профиль';
      _error = _syntheticImportError;
      _errorSummary = 'Сервер подписки вернул HTTP 502. Попробуйте позже.';
    });
  }

  Future<void> _copyError() async {
    final error = _error;
    if (error == null) return;
    await Clipboard.setData(ClipboardData(text: error));
  }

  Widget _input(NekkoTokens t) => NekkoDialogField(
    controller: _ctrl,
    enabled: !_submitting,
    maxLines: 4,
    minLines: 3,
    hint:
        'вставьте HTTPS-подписку = каждый сервер добавится отдельно,\nили ключ vless://, trojan://, ss://, hysteria2://',
    onChanged: _onChanged,
  );

  Widget _actions(NekkoTokens t) {
    final paste = NekkoDialogAction(
      icon: Icons.content_paste,
      label: 'вставить',
      enabled: !_submitting,
      onPressed: _pasteFromClipboard,
    );
    final submit = Wrap(
      alignment: WrapAlignment.end,
      spacing: 6,
      runSpacing: 6,
      children: [
        NekkoDialogAction(
          label: 'отмена',
          enabled: !_submitting,
          onPressed: () => Navigator.of(context).maybePop(),
        ),
        NekkoDialogAction(
          label: 'добавить',
          enabled: !_submitting,
          busy: _submitting,
          primary: true,
          onPressed: _submit,
        ),
      ],
    );
    return LayoutBuilder(
      builder: (context, constraints) {
        if (constraints.maxWidth < 320) {
          return Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Align(alignment: Alignment.centerLeft, child: paste),
              const SizedBox(height: 8),
              Align(alignment: Alignment.centerRight, child: submit),
            ],
          );
        }
        return Row(
          children: [
            paste,
            const SizedBox(width: 8),
            Expanded(
              child: Align(alignment: Alignment.centerRight, child: submit),
            ),
          ],
        );
      },
    );
  }
}

const _syntheticImportError = '''Источник: https://sub.example.net/profile
Причина: HTTP 502 Bad Gateway
Этап: загрузка подписки
Подробности: сервер подписки временно недоступен. Повторите попытку позже.
Код: subscription_fetch_failed''';

String _importErrorSummary(String details) {
  final httpStatus = RegExp(
    r'HTTP\s+(\d{3})',
    caseSensitive: false,
  ).firstMatch(details)?.group(1);
  if (httpStatus != null) {
    return 'Сервер подписки вернул HTTP $httpStatus. Проверьте ссылку или попробуйте позже.';
  }

  final firstLine = details
      .replaceFirst(RegExp(r'^(?:NekkoboxError|Exception):\s*'), '')
      .trim()
      .split('\n')
      .first
      .trim();
  if (firstLine.length <= 120) return firstLine;
  return '${firstLine.substring(0, 117).trimRight()}…';
}

class _ImportError extends StatefulWidget {
  const _ImportError({
    required this.summary,
    required this.title,
    required this.onCopy,
    required this.tokens,
  });

  final String summary;
  final String title;
  final Future<void> Function() onCopy;
  final NekkoTokens tokens;

  @override
  State<_ImportError> createState() => _ImportErrorState();
}

class _ImportErrorState extends State<_ImportError> {
  final _flash = GlobalKey<CopyFlashState>();
  bool _hovered = false;
  bool _flashing = false;

  Future<void> _copy() async {
    // One confirmation at a time: taps while it plays are ignored instead of
    // restarting it mid-flight.
    if (_flashing) return;
    setState(() => _flashing = true);
    await widget.onCopy();
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

  void _onFlashStatus(AnimationStatus status) {
    if (status == AnimationStatus.dismissed && mounted) {
      setState(() => _flashing = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = widget.tokens;
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
                          child: Row(
                            mainAxisAlignment: MainAxisAlignment.center,
                            children: [
                              Icon(Icons.copy_rounded, size: 18, color: t.bad),
                              const SizedBox(width: 7),
                              // Lowercase sits low in its em box; lift it so
                              // its x-height centres on the icon.
                              Transform.translate(
                                offset: const Offset(0, -1.5),
                                child: Text(
                                  'скопировать',
                                  textHeightBehavior: const TextHeightBehavior(
                                    applyHeightToFirstAscent: false,
                                    applyHeightToLastDescent: false,
                                    leadingDistribution:
                                        TextLeadingDistribution.even,
                                  ),
                                  style: TextStyle(
                                    color: t.bad,
                                    height: 1,
                                    fontSize: 15,
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
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}

// Stamp and staggered entrance ported from Nekko’s seed phrase outcome.
class _ImportOutcome extends StatefulWidget {
  const _ImportOutcome({required this.added});

  final int added;

  @override
  State<_ImportOutcome> createState() => _ImportOutcomeState();
}

class _ImportOutcomeState extends State<_ImportOutcome>
    with SingleTickerProviderStateMixin {
  late final _in = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 550),
  )..forward();

  @override
  void dispose() {
    _in.dispose();
    super.dispose();
  }

  /// [from]..[to] of the entrance as its own 0..1.
  Animation<double> _part(double from, double to, Curve curve) =>
      CurvedAnimation(
        parent: _in,
        curve: Interval(from, to, curve: curve),
      );

  Widget _rise(Animation<double> t, Widget child) => FadeTransition(
    opacity: t,
    child: SlideTransition(
      position: Tween(begin: const Offset(0, 0.4), end: Offset.zero).animate(t),
      child: child,
    ),
  );

  @override
  Widget build(BuildContext context) {
    final tokens = context.tokens;
    // The stamp overshoots and settles, turning upright from -45°.
    final stamp = _part(0, 0.9, const Cubic(0.3, 1.6, 0.5, 1));
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 28, horizontal: 24),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          FadeTransition(
            opacity: _part(0, 0.3, Curves.easeOut),
            child: AnimatedBuilder(
              animation: stamp,
              builder: (context, child) => Transform.rotate(
                angle: (1 - stamp.value) * -pi / 4,
                child: Transform.scale(
                  scale: 0.3 + 0.7 * stamp.value,
                  child: child,
                ),
              ),
              child: Container(
                width: 44,
                height: 44,
                decoration: BoxDecoration(
                  color: tokens.ok.withValues(alpha: 0.15),
                  shape: BoxShape.circle,
                ),
                alignment: Alignment.center,
                child: Icon(Icons.check_rounded, size: 22, color: tokens.ok),
              ),
            ),
          ),
          const SizedBox(height: 10),
          _rise(
            _part(0.27, 0.82, Curves.easeOut),
            Text(
              widget.added == 1 ? 'профиль добавлен' : 'серверы добавлены',
              textAlign: TextAlign.center,
              style: TextStyle(
                fontSize: 14,
                fontWeight: FontWeight.w500,
                color: tokens.text,
              ),
            ),
          ),
          const SizedBox(height: 4),
          _rise(
            _part(0.45, 1, Curves.easeOut),
            Text(
              'добавлено серверов: ${widget.added}',
              textAlign: TextAlign.center,
              style: TextStyle(fontSize: 12, color: tokens.textMuted),
            ),
          ),
        ],
      ),
    );
  }
}
