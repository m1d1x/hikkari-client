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

class _LogOverlayState extends State<LogOverlay> {
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

  Timer? _timer;
  final _scroll = ScrollController();
  List<String> _lines = const [];
  bool _onlyErrors = false;
  bool _autoscroll = true;

  @override
  void initState() {
    super.initState();
    _refresh();
    _timer = Timer.periodic(const Duration(seconds: 1), (_) => _refresh());
    _scroll.addListener(_onScroll);
  }

  @override
  void dispose() {
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

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final visible = _onlyErrors ? _lines.where(_isError).toList() : _lines;

    return Container(
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
              border: Border(top: BorderSide(color: t.border)),
              borderRadius: BorderRadius.vertical(
                bottom: Radius.circular(t.radius),
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
        ],
      ),
    );
  }

  Widget _toolbar(NekkoTokens t, int shown, int total) => Padding(
    padding: const EdgeInsets.fromLTRB(12, 8, 6, 8),
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
        _chip(
          t,
          icon: Icons.error_outline,
          label: 'ошибки',
          active: _onlyErrors,
          onTap: () => setState(() => _onlyErrors = !_onlyErrors),
        ),
        const SizedBox(width: 6),
        IconButton(
          tooltip: 'копировать',
          iconSize: 14,
          padding: EdgeInsets.zero,
          constraints: const BoxConstraints(minWidth: 28, minHeight: 28),
          onPressed: _lines.isEmpty ? null : _copy,
          icon: Icon(Icons.copy, color: t.textMuted),
        ),
        IconButton(
          tooltip: 'закрыть',
          iconSize: 16,
          padding: EdgeInsets.zero,
          constraints: const BoxConstraints(minWidth: 28, minHeight: 28),
          onPressed: widget.onClose,
          icon: Icon(Icons.close, color: t.textMuted),
        ),
      ],
    ),
  );

  Widget _chip(
    NekkoTokens t, {
    required IconData icon,
    required String label,
    required bool active,
    required VoidCallback onTap,
  }) => InkWell(
    onTap: onTap,
    borderRadius: BorderRadius.circular(t.radius),
    child: Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      decoration: BoxDecoration(
        color: active ? t.surface2 : Colors.transparent,
        border: Border.all(color: active ? t.borderStrong : t.border),
        borderRadius: BorderRadius.circular(t.radius),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 12, color: active ? t.bad : t.textMuted),
          const SizedBox(width: 4),
          Text(
            label,
            style: TextStyle(
              color: active ? t.text : t.textMuted,
              fontSize: 11,
            ),
          ),
        ],
      ),
    ),
  );
}
