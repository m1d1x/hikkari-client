import 'package:flutter/material.dart';

import '../theme/tokens.dart';

/// One row of a [NekkoContextMenu].
class NekkoMenuItem {
  final String label;
  final VoidCallback onTap;
  final bool destructive;

  const NekkoMenuItem({
    required this.label,
    required this.onTap,
    this.destructive = false,
  });
}

/// Right-click menu in the project's style: surface fill, hairline border,
/// mono-free 12px labels, destructive rows in the `bad` colour.
class NekkoContextMenu {
  static Future<void> show(
    BuildContext context,
    Offset position,
    List<NekkoMenuItem> items,
  ) async {
    final t = context.tokens;
    final overlay =
        Overlay.of(context).context.findRenderObject()! as RenderBox;
    final picked = await showMenu<NekkoMenuItem>(
      context: context,
      position: RelativeRect.fromRect(
        position & const Size(1, 1),
        Offset.zero & overlay.size,
      ),
      color: t.surface2,
      elevation: 0,
      menuPadding: const EdgeInsets.symmetric(vertical: 4),
      constraints: const BoxConstraints(minWidth: 140),
      shape: RoundedRectangleBorder(
        side: BorderSide(color: t.borderStrong),
        borderRadius: BorderRadius.circular(t.radius),
      ),
      items: [
        for (final item in items)
          PopupMenuItem<NekkoMenuItem>(
            value: item,
            height: 34,
            mouseCursor: SystemMouseCursors.click,
            child: Text(
              item.label,
              style: TextStyle(
                color: item.destructive ? t.bad : t.text,
                fontSize: 12,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
      ],
    );
    picked?.onTap();
  }
}
