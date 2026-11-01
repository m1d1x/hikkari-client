import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';

import '../state/app_state.dart';
import '../theme/tokens.dart';
import '../ui/nekko_toast.dart';

/// Engine log overlay. Polls `AppState.logTail()` once a second while it's
/// on screen, strips ANSI colour codes, and paints lines in a fixed-height
/// scroller. Has an "errors only" filter and a copy-to-clipboard button so
/// the user can grab the last few hundred lines without digging into the
/// log file on disk.
class LogOverlay extends StatefulWidget {
  final VoidCallback onClose;

  const LogOverlay({super.key, required this.onClose});

  @override
  State<LogOverlay> createState() => _LogOverlayState();
}

class _LogOverlayState extends State<LogOverlay>
    with SingleTickerProviderStateMixin {
  static final _ansi = RegExp(r'\x1B\[[0-9;]*[A-Za-z]');
  static final _reConnId = RegExp(r'\[\d+\s+[^\]]+\]');
  static final _reTs = RegExp(
    r'^\+?\d{4}[-/]\d{2}[-/]\d{2}[\sT]\d{2}:\d{2}:\d{2}(\.\d+)?\s*',
  );
  static final _reIpPort = RegExp(
    r'\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}:\d{4,5}\b',
  );
  static const _errorMarkers = [
    'FATAL',
    'ERROR',
    'Access is denied',
    'failed to',
    'panic',
  ];

  static String _normalize(String s) {
    var res = s.replaceAll(_ansi, '');
    res = res.replaceAll(_reTs, '');
    res = res.replaceAll(_reConnId, '[id]');
    res = res.replaceAll(_reIpPort, '[ip:port]');
    return res.trim();
  }

  late final AnimationController _anim;
  late final Animation<double> _fade;
  late final Animation<Offset> _slide;

  Timer? _timer;
  final _scroll = ScrollController();
  List<String> _lines = const [];
  bool _onlyErrors = false;
  bool _autoscroll = true;
  bool _sending = false;

  @override
  void initState() {
    super.initState();
    _anim = AnimationController(
      vsync: this,
      duration: AppDurations.expand,
    )..forward();
    _fade = CurvedAnimation(parent: _anim, curve: AppCurves.fastInSlowOut);
    _slide = Tween<Offset>(
      begin: const Offset(0, -0.05),
      end: Offset.zero,
    ).animate(CurvedAnimation(parent: _anim, curve: AppCurves.fastInSlowOut));
    _refresh();
    _timer = Timer.periodic(const Duration(seconds: 1), (_) => _refresh());
    _scroll.addListener(_onScroll);
  }

  @override
  void dispose() {
    _anim.dispose();
    _timer?.cancel();
    _scroll.removeListener(_onScroll);
    _scroll.dispose();
    super.dispose();
  }

  void _onScroll() {
    if (!_scroll.hasClients) return;
    final pos = _scroll.position;
    final atBottom = pos.pixels >= pos.maxScrollExtent - 8;
    if (atBottom != _autoscroll) {
      setState(() => _autoscroll = atBottom);
    }
  }

  Future<void> _refresh() async {
    if (!mounted) return;
    final tail = await context.read<AppState>().logTail();
    if (!mounted) return;
    final rawLines = tail
        .split('\n')
        .map((l) => l.replaceAll(_ansi, '').trimRight())
        .where((l) => l.isNotEmpty)
        .toList();

    final collapsed = <String>[];
    String? lastNorm;
    int repeats = 1;

    for (final l in rawLines) {
      final norm = _normalize(l);
      if (norm == lastNorm && norm.isNotEmpty) {
        repeats++;
      } else {
        if (lastNorm != null && repeats > 1) {
          collapsed[collapsed.length - 1] += ' ($repeats×)';
        }
        collapsed.add(l);
        lastNorm = norm;
        repeats = 1;
      }
    }
    if (lastNorm != null && repeats > 1) {
      collapsed[collapsed.length - 1] += ' ($repeats×)';
    }

    if (mounted) {
      setState(() => _lines = collapsed);
      if (_autoscroll) {
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (_scroll.hasClients) {
            _scroll.jumpTo(_scroll.position.maxScrollExtent);
          }
        });
      }
    }
  }

  bool _isError(String line) {
    if (line.contains(' INFO ') ||
        line.contains(' DEBUG ') ||
        line.contains(' TRACE ')) {
      return false;
    }
    for (final m in _errorMarkers) {
      if (line.contains(m)) return true;
    }
    return false;
  }

  Future<void> _copy() async {
    await Clipboard.setData(ClipboardData(text: _lines.join('\n')));
    if (!mounted) return;
    NekkoToast.show(
      context,
      'лог скопирован',
      icon: Icons.check_rounded,
      hold: NekkoToast.glanceHold,
    );
  }

  Future<void> _sendReport() async {
    if (_sending) return;
    setState(() => _sending = true);
    try {
      final appState = context.read<AppState>();
      await appState.submitSupportReport('Отправка журнала логов из приложения');
      if (!mounted) return;
      NekkoToast.show(
        context,
        'лог отправлен в поддержку',
        icon: Icons.check_rounded,
        hold: NekkoToast.glanceHold,
      );
    } catch (_) {
      if (!mounted) return;
      NekkoToast.show(
        context,
        'не удалось отправить лог',
        icon: Icons.error_outline_rounded,
        hold: NekkoToast.glanceHold,
      );
    } finally {
      if (mounted) {
        setState(() => _sending = false);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final visible = _onlyErrors ? _lines.where(_isError).toList() : _lines;

    return FadeTransition(
      opacity: _fade,
      child: SlideTransition(
        position: _slide,
        child: Container(
          margin: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
          decoration: BoxDecoration(
            color: t.surface,
            border: Border.all(color: t.border),
            borderRadius: BorderRadius.circular(t.radius),
          ),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              _toolbar(t, visible.length, _lines.length),
              Container(
                height: 220,
                padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
                decoration: BoxDecoration(
                  color: t.bg,
                  border: Border(
                    top: BorderSide(color: t.border),
                    bottom: BorderSide(color: t.border),
                  ),
                ),
                child: visible.isEmpty
                    ? Center(
                        child: Text(
                          _lines.isEmpty ? 'журнал пуст' : 'нет строк по фильтру',
                          style: TextStyle(color: t.textFaint, fontSize: 11),
                        ),
                      )
                    : ListView.builder(
                        controller: _scroll,
                        itemCount: visible.length,
                        itemExtent: 16,
                        itemBuilder: (_, i) {
                          final line = visible[i];
                          final isErr = _isError(line);
                          return Text(
                            line,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            style: TextStyle(
                              color: isErr ? t.bad : t.textMuted,
                              fontFamily: t.monoFamily,
                              fontSize: 11,
                              height: 1.4,
                            ),
                          );
                        },
                      ),
              ),
              _actionsBar(t),
            ],
          ),
        ),
      ),
    );
  }

  Widget _actionsBar(NekkoTokens t) => Container(
    padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
    decoration: BoxDecoration(
      color: t.surface,
      borderRadius: BorderRadius.vertical(
        bottom: Radius.circular(t.radius),
      ),
    ),
    child: Row(
      children: [
        Expanded(
          child: _OverlayBtn(
            icon: Icons.copy_rounded,
            label: 'скопировать',
            onTap: _lines.isEmpty ? null : _copy,
          ),
        ),
        const SizedBox(width: 8),
        Expanded(
          child: _OverlayBtn(
            icon: _sending ? Icons.sync_rounded : Icons.send_rounded,
            label: _sending ? 'отправка...' : 'отправить поддержке',
            primary: true,
            busy: _sending,
            onTap: (_lines.isEmpty || _sending) ? null : _sendReport,
          ),
        ),
      ],
    ),
  );

  Widget _toolbar(NekkoTokens t, int shown, int total) => Padding(
    padding: const EdgeInsets.fromLTRB(12, 8, 8, 8),
    child: Row(
      children: [
        Text(
          'журнал',
          style: TextStyle(
            color: t.text,
            fontSize: 13,
            fontWeight: FontWeight.w600,
          ),
        ),
        const SizedBox(width: 8),
        Text(
          total == shown ? '$total' : '$shown / $total',
          style: TextStyle(
            color: t.textFaint,
            fontSize: 11,
            fontFamily: t.monoFamily,
          ),
        ),
        const Spacer(),
        _ChipBtn(
          icon: Icons.error_outline,
          label: 'ошибки',
          active: _onlyErrors,
          onTap: () => setState(() => _onlyErrors = !_onlyErrors),
        ),
        const SizedBox(width: 6),
        _CloseBtn(onTap: widget.onClose),
      ],
    ),
  );
}

class _OverlayBtn extends StatefulWidget {
  final IconData icon;
  final String label;
  final VoidCallback? onTap;
  final bool primary;
  final bool busy;

  const _OverlayBtn({
    required this.icon,
    required this.label,
    required this.onTap,
    this.primary = false,
    this.busy = false,
  });

  @override
  State<_OverlayBtn> createState() => _OverlayBtnState();
}

class _OverlayBtnState extends State<_OverlayBtn> {
  bool _hovered = false;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final enabled = widget.onTap != null;

    Color bg;
    Border? border;
    Color fg;

    if (!enabled) {
      bg = t.surface2.withValues(alpha: 0.4);
      border = Border.all(color: t.border);
      fg = t.textFaint;
    } else if (widget.primary) {
      bg = _hovered
          ? Color.alphaBlend(Colors.white.withValues(alpha: 0.14), t.primary)
          : t.primary;
      border = null;
      fg = Colors.white;
    } else {
      bg = _hovered ? t.surface2 : t.surface;
      border = Border.all(color: _hovered ? t.borderStrong : t.border);
      fg = _hovered ? t.text : t.textMuted;
    }

    return MouseRegion(
      cursor: enabled ? SystemMouseCursors.click : SystemMouseCursors.basic,
      onEnter: (_) => setState(() => _hovered = true),
      onExit: (_) => setState(() => _hovered = false),
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: widget.onTap,
        child: AnimatedContainer(
          duration: AppDurations.fast,
          curve: AppCurves.fastInSlowOut,
          padding: const EdgeInsets.symmetric(vertical: 8),
          decoration: BoxDecoration(
            color: bg,
            border: border,
            borderRadius: BorderRadius.circular(t.radius),
          ),
          child: Row(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              Icon(widget.icon, size: 14, color: fg),
              const SizedBox(width: 6),
              Text(
                widget.label,
                style: TextStyle(
                  color: fg,
                  fontSize: 12,
                  fontWeight: widget.primary ? FontWeight.w600 : FontWeight.w500,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _ChipBtn extends StatefulWidget {
  final IconData icon;
  final String label;
  final bool active;
  final VoidCallback onTap;

  const _ChipBtn({
    required this.icon,
    required this.label,
    required this.active,
    required this.onTap,
  });

  @override
  State<_ChipBtn> createState() => _ChipBtnState();
}

class _ChipBtnState extends State<_ChipBtn> {
  bool _hovered = false;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final active = widget.active;
    return MouseRegion(
      cursor: SystemMouseCursors.click,
      onEnter: (_) => setState(() => _hovered = true),
      onExit: (_) => setState(() => _hovered = false),
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: widget.onTap,
        child: AnimatedContainer(
          duration: AppDurations.fast,
          curve: AppCurves.fastInSlowOut,
          padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
          decoration: BoxDecoration(
            color: active
                ? (_hovered ? t.surface2 : t.surface2.withValues(alpha: 0.8))
                : (_hovered ? t.surface2.withValues(alpha: 0.5) : Colors.transparent),
            border: Border.all(
              color: active
                  ? (_hovered ? t.bad : t.borderStrong)
                  : (_hovered ? t.borderStrong : t.border),
            ),
            borderRadius: BorderRadius.circular(t.radius),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(
                widget.icon,
                size: 12,
                color: active ? t.bad : (_hovered ? t.text : t.textMuted),
              ),
              const SizedBox(width: 4),
              Text(
                widget.label,
                style: TextStyle(
                  color: active ? t.text : (_hovered ? t.text : t.textMuted),
                  fontSize: 11,
                  fontWeight: active ? FontWeight.w600 : FontWeight.w500,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _CloseBtn extends StatefulWidget {
  final VoidCallback onTap;

  const _CloseBtn({required this.onTap});

  @override
  State<_CloseBtn> createState() => _CloseBtnState();
}

class _CloseBtnState extends State<_CloseBtn> {
  bool _hovered = false;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return MouseRegion(
      cursor: SystemMouseCursors.click,
      onEnter: (_) => setState(() => _hovered = true),
      onExit: (_) => setState(() => _hovered = false),
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: widget.onTap,
        child: AnimatedContainer(
          duration: AppDurations.fast,
          curve: AppCurves.fastInSlowOut,
          width: 26,
          height: 26,
          decoration: BoxDecoration(
            color: _hovered ? t.surface2 : Colors.transparent,
            borderRadius: BorderRadius.circular(t.radius),
          ),
          alignment: Alignment.center,
          child: Icon(
            Icons.close,
            size: 16,
            color: _hovered ? t.text : t.textMuted,
          ),
        ),
      ),
    );
  }
}
