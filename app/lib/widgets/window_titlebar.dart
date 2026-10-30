import 'package:flutter/material.dart';
import 'package:window_manager/window_manager.dart';

import '../theme/tokens.dart';

/// Custom Windows titlebar — replaces the native frame so the controls
/// match the rest of the UI instead of the OS default. 32px tall, drag
/// area on the left with logo + label, three buttons on the right.
class WindowTitlebar extends StatefulWidget {
  const WindowTitlebar({super.key});

  static const double height = 32;

  @override
  State<WindowTitlebar> createState() => _WindowTitlebarState();
}

class _WindowTitlebarState extends State<WindowTitlebar> with WindowListener {
  bool _maximized = false;

  @override
  void initState() {
    super.initState();
    windowManager.addListener(this);
    windowManager.isMaximized().then((v) {
      if (mounted) setState(() => _maximized = v);
    });
  }

  @override
  void dispose() {
    windowManager.removeListener(this);
    super.dispose();
  }

  @override
  void onWindowMaximize() => setState(() => _maximized = true);

  @override
  void onWindowUnmaximize() => setState(() => _maximized = false);

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return Container(
      height: WindowTitlebar.height,
      color: t.bg,
      child: Row(
        children: [
          Expanded(
            child: GestureDetector(
              behavior: HitTestBehavior.translucent,
              onPanStart: (_) => windowManager.startDragging(),
              onDoubleTap: () async {
                if (await windowManager.isMaximized()) {
                  await windowManager.unmaximize();
                } else {
                  await windowManager.maximize();
                }
              },
              child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: 12),
                child: Row(
                  children: [
                    Container(
                      width: 12,
                      height: 12,
                      decoration: BoxDecoration(
                        color: t.primary,
                        borderRadius: BorderRadius.circular(3),
                      ),
                    ),
                    const SizedBox(width: 8),
                    Text(
                      'nekkobox',
                      style: TextStyle(
                        color: t.textMuted,
                        fontSize: 11,
                        fontWeight: FontWeight.w500,
                        letterSpacing: 0.2,
                      ),
                    ),
                  ],
                ),
              ),
            ),
          ),
          _WinButton(
            icon: _WinIcon.minimize,
            onTap: () => windowManager.minimize(),
          ),
          _WinButton(
            icon: _maximized ? _WinIcon.restore : _WinIcon.maximize,
            onTap: () async {
              if (await windowManager.isMaximized()) {
                await windowManager.unmaximize();
              } else {
                await windowManager.maximize();
              }
            },
          ),
          _WinButton(
            icon: _WinIcon.close,
            isClose: true,
            onTap: () => windowManager.close(),
          ),
        ],
      ),
    );
  }
}

enum _WinIcon { minimize, maximize, restore, close }

class _WinButton extends StatefulWidget {
  final _WinIcon icon;
  final VoidCallback onTap;
  final bool isClose;

  const _WinButton({
    required this.icon,
    required this.onTap,
    this.isClose = false,
  });

  @override
  State<_WinButton> createState() => _WinButtonState();
}

class _WinButtonState extends State<_WinButton> {
  bool _hover = false;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final bg = !_hover
        ? Colors.transparent
        : widget.isClose
        ? t.bad
        : t.surface2;
    final fg = (_hover && widget.isClose) ? Colors.white : t.textMuted;

    return MouseRegion(
      onEnter: (_) => setState(() => _hover = true),
      onExit: (_) => setState(() => _hover = false),
      child: GestureDetector(
        onTap: widget.onTap,
        behavior: HitTestBehavior.opaque,
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 100),
          width: 46,
          height: WindowTitlebar.height,
          color: bg,
          child: Center(
            child: CustomPaint(
              size: const Size(10, 10),
              painter: _WinIconPainter(icon: widget.icon, color: fg),
            ),
          ),
        ),
      ),
    );
  }
}

class _WinIconPainter extends CustomPainter {
  final _WinIcon icon;
  final Color color;

  _WinIconPainter({required this.icon, required this.color});

  @override
  void paint(Canvas canvas, Size size) {
    final p = Paint()
      ..color = color
      ..style = PaintingStyle.stroke
      ..strokeWidth = 1
      ..isAntiAlias = true;

    final w = size.width;
    final h = size.height;

    switch (icon) {
      case _WinIcon.minimize:
        canvas.drawLine(Offset(0, h / 2), Offset(w, h / 2), p);
        break;
      case _WinIcon.maximize:
        canvas.drawRect(Rect.fromLTWH(0.5, 0.5, w - 1, h - 1), p);
        break;
      case _WinIcon.restore:
        canvas.drawRect(Rect.fromLTWH(2.5, 0.5, w - 3, h - 3), p);
        canvas.drawRect(Rect.fromLTWH(0.5, 2.5, w - 3, h - 3), p);
        break;
      case _WinIcon.close:
        canvas.drawLine(const Offset(0, 0), Offset(w, h), p);
        canvas.drawLine(Offset(w, 0), Offset(0, h), p);
        break;
    }
  }

  @override
  bool shouldRepaint(covariant _WinIconPainter old) =>
      old.icon != icon || old.color != color;
}
