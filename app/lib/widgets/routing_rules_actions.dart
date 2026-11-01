import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';

import '../state/app_state.dart';
import '../theme/tokens.dart';
import '../ui/nekko_dialog.dart';
import '../ui/nekko_toast.dart';
import 'import_routing_dialog.dart';

/// Actions to export, import, or reset routing rules back to defaults.json.
class RoutingRulesActions extends StatelessWidget {
  final ValueChanged<String?>? onHintChanged;

  const RoutingRulesActions({super.key, this.onHintChanged});

  Future<void> _export(BuildContext context) async {
    try {
      final state = context.read<AppState>();
      final data = await state.exportRoutingSnapshot();
      await Clipboard.setData(ClipboardData(text: data));
      if (!context.mounted) return;
      NekkoToast.show(context, 'Правила скопированы');
    } catch (e) {
      if (!context.mounted) return;
      NekkoToast.show(
        context,
        'не удалось экспортировать правила: $e',
        icon: Icons.error_outline_rounded,
      );
    }
  }

  Future<void> _import(BuildContext context) async {
    await ImportRoutingDialog.show(context);
  }

  Future<void> _reset(BuildContext context) async {
    final confirmed = await NekkoDialog.confirm(
      context,
      title: 'Сброс маршрутизации',
      message: 'Сбросить все правила к стандартным?',
      confirmLabel: 'сбросить',
    );
    if (!confirmed || !context.mounted) return;

    try {
      final state = context.read<AppState>();
      await state.resetToDefaults();
      if (!context.mounted) return;
      NekkoToast.show(context, 'Правила сброшены к стандартным');
    } catch (e) {
      if (!context.mounted) return;
      NekkoToast.show(
        context,
        'не удалось сбросить правила: $e',
        icon: Icons.error_outline_rounded,
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      mainAxisSize: MainAxisSize.min,
      children: [
        Row(
          children: [
            Expanded(
              child: _ActionButton(
                key: const ValueKey('export_routing_button'),
                icon: Icons.upload_rounded,
                label: 'Экспорт правил',
                onPressed: () => _export(context),
                onHoverHint: 'скопировать снимок правил в буфер обмена',
                onHintChanged: onHintChanged,
              ),
            ),
            const SizedBox(width: 8),
            Expanded(
              child: _ActionButton(
                key: const ValueKey('import_routing_button'),
                icon: Icons.download_rounded,
                label: 'Импорт правил',
                onPressed: () => _import(context),
                onHoverHint: 'вставить и применить JSON-снимок правил',
                onHintChanged: onHintChanged,
              ),
            ),
          ],
        ),
        const SizedBox(height: 8),
        _ActionButton(
          key: const ValueKey('reset_routing_button'),
          icon: Icons.restart_alt_rounded,
          label: 'Сбросить настройки маршрутизации',
          isDestructive: true,
          onPressed: () => _reset(context),
          onHoverHint: 'сбросить все правила и группы к стандартным из defaults.json',
          onHintChanged: onHintChanged,
        ),
      ],
    );
  }
}

class _ActionButton extends StatefulWidget {
  final IconData icon;
  final String label;
  final VoidCallback onPressed;
  final bool isDestructive;
  final String? onHoverHint;
  final ValueChanged<String?>? onHintChanged;

  const _ActionButton({
    super.key,
    required this.icon,
    required this.label,
    required this.onPressed,
    this.isDestructive = false,
    this.onHoverHint,
    this.onHintChanged,
  });

  @override
  State<_ActionButton> createState() => _ActionButtonState();
}

class _ActionButtonState extends State<_ActionButton> {
  bool _hover = false;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final hoverColor = widget.isDestructive
        ? t.bad.withValues(alpha: 0.12)
        : t.surface2;
    final borderColor = widget.isDestructive
        ? (_hover ? t.bad.withValues(alpha: 0.6) : t.borderStrong)
        : (_hover ? t.primary.withValues(alpha: 0.55) : t.borderStrong);
    final textColor = widget.isDestructive
        ? (_hover ? t.bad : t.textMuted)
        : (_hover ? t.text : t.textMuted);
    final iconColor = widget.isDestructive
        ? (_hover ? t.bad : t.textMuted)
        : (_hover ? t.primary : t.textMuted);

    return MouseRegion(
      cursor: SystemMouseCursors.click,
      onEnter: (_) {
        setState(() => _hover = true);
        if (widget.onHoverHint != null) {
          widget.onHintChanged?.call(widget.onHoverHint);
        }
      },
      onExit: (_) {
        setState(() => _hover = false);
        widget.onHintChanged?.call(null);
      },
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: widget.onPressed,
        child: AnimatedContainer(
          duration: AppDurations.fast,
          curve: AppCurves.fastInSlowOut,
          padding: const EdgeInsets.symmetric(vertical: 9, horizontal: 12),
          decoration: BoxDecoration(
            color: _hover ? hoverColor : Colors.transparent,
            borderRadius: BorderRadius.circular(t.radius),
            border: Border.all(color: borderColor),
          ),
          child: Row(
            mainAxisAlignment: MainAxisAlignment.center,
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(widget.icon, size: 15, color: iconColor),
              const SizedBox(width: 6),
              Flexible(
                child: Text(
                  widget.label,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(
                    color: textColor,
                    fontSize: 12,
                    fontWeight: FontWeight.w500,
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
