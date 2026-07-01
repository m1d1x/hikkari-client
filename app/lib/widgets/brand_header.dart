import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../state/app_state.dart';
import '../theme/tokens.dart';
import 'route_section.dart';

/// Compact top-right action. The old brand/IP header was intentionally removed
/// so the home screen starts with the product itself, not chrome.
class BrandHeader extends StatelessWidget {
  final VoidCallback onOpenLogs;
  const BrandHeader({super.key, required this.onOpenLogs});

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final h = context.watch<AppState>().health;
    final state = context.read<AppState>();
    final status = h?.status ?? 'off';
    final ip = (h?.ip as String?) ?? '';
    final msg = (h?.message as String?) ?? '';
    final details = [
      'настройки',
      if (ip.isNotEmpty) ip,
      if (msg.isNotEmpty) msg,
    ].join('\n');

    final baseColor = _statusColor(t, status);
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 14, 14, 0),
      child: Align(
        alignment: Alignment.centerRight,
        child: _HoverIcon(
          tooltip: details,
          onPressed: () => _showSettings(context, state),
          icon: Icons.settings_outlined,
          size: 24,
          color: baseColor,
          hoverColor: t.text,
          padding: const EdgeInsets.all(14),
        ),
      ),
    );
  }

  Color _statusColor(NekkoTokens t, String status) => switch (status) {
        'connecting' => t.warn,
        'broken' => t.bad,
        _ => t.textMuted,
      };

  Future<void> _showSettings(BuildContext context, AppState state) {
    final t = context.tokens;
    return showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      backgroundColor: t.surface,
      barrierColor: Colors.black54,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(t.radiusLg)),
      ),
      builder: (_) => ChangeNotifierProvider<AppState>.value(
        value: state,
        child: _SettingsSheet(onOpenLogs: onOpenLogs),
      ),
    );
  }
}

class _SettingsSheet extends StatelessWidget {
  final VoidCallback onOpenLogs;
  const _SettingsSheet({required this.onOpenLogs});

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final h = context.watch<AppState>().health;
    final status = h?.status ?? 'off';
    final ip = (h?.ip as String?) ?? '';
    final msg = (h?.message as String?) ?? '';
    final statusLabel = switch (status) {
      'ok' => 'подключено',
      'connecting' => 'подключаюсь',
      'broken' => 'ошибка туннеля',
      _ => 'отключено',
    };

    return SafeArea(
      top: false,
      child: SingleChildScrollView(
        child: Padding(
          padding: const EdgeInsets.fromLTRB(20, 12, 20, 20),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Center(
                child: Container(
                  width: 36,
                  height: 4,
                  decoration: BoxDecoration(
                    color: t.borderStrong,
                    borderRadius: BorderRadius.circular(2),
                  ),
                ),
              ),
              const SizedBox(height: 18),
              Text(
                'настройки',
                style: TextStyle(
                  color: t.text,
                  fontSize: 15,
                  fontWeight: FontWeight.w700,
                ),
              ),
              const SizedBox(height: 14),
              _InfoRow(label: 'статус', value: statusLabel),
              _InfoRow(label: 'ip', value: ip.isEmpty ? '-' : ip),
              if (msg.isNotEmpty) _InfoRow(label: 'сообщение', value: msg),
              const SizedBox(height: 16),
              Divider(height: 1, color: t.border),
              const SizedBox(height: 16),
              const RouteSettingsSection(),
              const SizedBox(height: 12),
              OutlinedButton.icon(
                onPressed: () {
                  Navigator.of(context).maybePop();
                  onOpenLogs();
                },
                icon: const Icon(Icons.terminal, size: 16),
                label: const Text('логи'),
                style: OutlinedButton.styleFrom(
                  foregroundColor: t.text,
                  side: BorderSide(color: t.borderStrong),
                  shape: RoundedRectangleBorder(
                    borderRadius: BorderRadius.circular(t.radius),
                  ),
                  padding: const EdgeInsets.symmetric(vertical: 12),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _InfoRow extends StatelessWidget {
  final String label;
  final String value;

  const _InfoRow({required this.label, required this.value});

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 88,
            child: Text(
              label,
              style: TextStyle(color: t.textMuted, fontSize: 12),
            ),
          ),
          Expanded(
            child: SelectableText(
              value,
              style: TextStyle(
                color: t.text,
                fontSize: 12,
                fontFamily: t.monoFamily,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _HoverIcon extends StatefulWidget {
  final String tooltip;
  final VoidCallback onPressed;
  final IconData icon;
  final double size;
  final Color color;
  final Color hoverColor;
  final EdgeInsets padding;

  const _HoverIcon({
    required this.tooltip,
    required this.onPressed,
    required this.icon,
    required this.size,
    required this.color,
    required this.hoverColor,
    required this.padding,
  });

  @override
  State<_HoverIcon> createState() => _HoverIconState();
}

class _HoverIconState extends State<_HoverIcon> {
  bool _hover = false;

  @override
  Widget build(BuildContext context) {
    return Tooltip(
      message: widget.tooltip,
      waitDuration: const Duration(milliseconds: 400),
      child: MouseRegion(
        cursor: SystemMouseCursors.click,
        onEnter: (_) => setState(() => _hover = true),
        onExit: (_) => setState(() => _hover = false),
        child: GestureDetector(
          onTap: widget.onPressed,
          behavior: HitTestBehavior.opaque,
          child: Padding(
            padding: widget.padding,
            child: AnimatedSwitcher(
              duration: const Duration(milliseconds: 150),
              child: Icon(
                widget.icon,
                key: ValueKey(_hover),
                size: widget.size,
                color: _hover ? widget.hoverColor : widget.color,
              ),
            ),
          ),
        ),
      ),
    );
  }
}
