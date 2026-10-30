import 'dart:async';

import 'package:flutter/material.dart';

import '../theme/tokens.dart';

/// A compact bottom notice, matching Nekko's «скопировано» pill.
abstract final class NekkoToast {
  static const readHold = Duration(milliseconds: 2200);
  static const glanceHold = Duration(milliseconds: 1200);
  static final _entries = Expando<OverlayEntry>();

  /// Capture before awaiting work that may close the originating dialog.
  static ToastAnchor anchor(BuildContext context) =>
      ToastAnchor._(Overlay.maybeOf(context, rootOverlay: true));

  static void show(
    BuildContext context,
    String text, {
    IconData icon = Icons.info_outline_rounded,
    Duration hold = readHold,
  }) => anchor(context).show(text, icon: icon, hold: hold);
}

class ToastAnchor {
  const ToastAnchor._(this._overlay);

  final OverlayState? _overlay;

  void show(
    String text, {
    IconData icon = Icons.info_outline_rounded,
    Duration hold = NekkoToast.readHold,
  }) {
    final overlay = _overlay;
    if (overlay == null || !overlay.mounted) return;
    final previous = NekkoToast._entries[overlay];
    previous?.remove();
    previous?.dispose();
    late final OverlayEntry entry;
    entry = OverlayEntry(
      builder: (_) => _Toast(
        text: text,
        icon: icon,
        hold: hold,
        onDone: () {
          if (NekkoToast._entries[overlay] != entry) return;
          NekkoToast._entries[overlay] = null;
          entry.remove();
          entry.dispose();
        },
      ),
    );
    NekkoToast._entries[overlay] = entry;
    overlay.insert(entry);
  }
}

class _Toast extends StatefulWidget {
  const _Toast({
    required this.text,
    required this.icon,
    required this.hold,
    required this.onDone,
  });

  final String text;
  final IconData icon;
  final Duration hold;
  final VoidCallback onDone;

  @override
  State<_Toast> createState() => _ToastState();
}

class _ToastState extends State<_Toast> with SingleTickerProviderStateMixin {
  late final _animation = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 180),
  );
  Timer? _timer;

  @override
  void initState() {
    super.initState();
    unawaited(_animation.forward());
    _timer = Timer(widget.hold, () async {
      if (!mounted) return;
      await _animation.reverse();
      if (mounted) widget.onDone();
    });
  }

  @override
  void dispose() {
    _timer?.cancel();
    _animation.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return Positioned.fill(
      child: IgnorePointer(
        child: SafeArea(
          child: Padding(
            padding: EdgeInsets.only(
              bottom: 96 + MediaQuery.viewInsetsOf(context).bottom,
              left: 24,
              right: 24,
            ),
            child: Align(
              alignment: Alignment.bottomCenter,
              child: FadeTransition(
                opacity: _animation.drive(CurveTween(curve: Curves.easeOut)),
                child: Material(
                  type: MaterialType.transparency,
                  child: Semantics(
                    liveRegion: true,
                    child: Container(
                      padding: const EdgeInsets.symmetric(
                        horizontal: 16,
                        vertical: 9,
                      ),
                      decoration: BoxDecoration(
                        color: t.surface2,
                        borderRadius: BorderRadius.circular(20),
                        border: Border.all(color: t.border),
                      ),
                      child: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          Icon(widget.icon, size: 15, color: t.primary),
                          const SizedBox(width: 8),
                          Flexible(
                            child: Text(
                              widget.text,
                              style: TextStyle(
                                fontFamily: t.monoFamily,
                                fontSize: 13,
                                fontWeight: FontWeight.w500,
                                color: t.text,
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
          ),
        ),
      ),
    );
  }
}
