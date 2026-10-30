import 'package:flutter/material.dart';

import '../theme/tokens.dart';
import 'nekko_dialog_action.dart';

/// Shared shell extracted from the profile import dialog.
class NekkoDialog extends StatelessWidget {
  final String title;
  final Widget content;
  final Widget actions;
  final Widget? outcome;

  const NekkoDialog({
    super.key,
    required this.title,
    required this.content,
    required this.actions,
    this.outcome,
  });

  static Future<bool> confirm(
    BuildContext context, {
    required String title,
    required String message,
    required String confirmLabel,
  }) async {
    return await showDialog<bool>(
          context: context,
          barrierColor: Colors.black54,
          builder: (ctx) => NekkoDialog(
            title: title,
            content: Text(
              message,
              style: TextStyle(
                color: ctx.tokens.textMuted,
                fontSize: 12,
                height: 1.5,
              ),
            ),
            actions: Wrap(
              alignment: WrapAlignment.end,
              spacing: 6,
              runSpacing: 6,
              children: [
                NekkoDialogAction(
                  label: 'отмена',
                  enabled: true,
                  onPressed: () => Navigator.pop(ctx, false),
                ),
                NekkoDialogAction(
                  label: confirmLabel,
                  enabled: true,
                  primary: true,
                  destructive: true,
                  onPressed: () => Navigator.pop(ctx, true),
                ),
              ],
            ),
          ),
        ) ??
        false;
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return Dialog(
      backgroundColor: t.surface,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(t.radiusLg),
      ),
      insetPadding: const EdgeInsets.symmetric(horizontal: 24, vertical: 24),
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 480),
        child: SingleChildScrollView(
          child: _DialogFold(
            outcome: outcome,
            child: Padding(
              padding: const EdgeInsets.fromLTRB(24, 20, 24, 20),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Text(
                    title,
                    style: TextStyle(
                      color: t.text,
                      fontSize: 14,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  const SizedBox(height: 16),
                  content,
                  const SizedBox(height: 20),
                  actions,
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}

// Nekko's fold and stamp overlap so the frame responds on the first frame.
class _DialogFold extends StatelessWidget {
  const _DialogFold({required this.outcome, required this.child});

  final Widget? outcome;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    final folding = outcome != null;
    return AnimatedCrossFade(
      duration: const Duration(milliseconds: 450),
      alignment: Alignment.center,
      // AnimatedCrossFade's default Stack aligns its top child to the left.
      // A compact outcome can be narrower than Dialog's minimum width.
      layoutBuilder: (topChild, topKey, bottomChild, bottomKey) => Stack(
        alignment: Alignment.topCenter,
        clipBehavior: Clip.none,
        children: [
          Positioned(
            key: bottomKey,
            left: 0,
            top: 0,
            right: 0,
            child: bottomChild,
          ),
          Positioned(key: topKey, child: topChild),
        ],
      ),
      sizeCurve: const Cubic(0.2, 0.8, 0.2, 1),
      firstCurve: const Interval(0, 0.49, curve: Curves.easeIn),
      secondCurve: const Interval(0, 0.8, curve: Curves.easeOut),
      crossFadeState: folding
          ? CrossFadeState.showSecond
          : CrossFadeState.showFirst,
      firstChild: IgnorePointer(
        ignoring: folding,
        child: AnimatedScale(
          scale: folding ? 0.94 : 1,
          duration: const Duration(milliseconds: 220),
          curve: Curves.easeOut,
          child: child,
        ),
      ),
      secondChild: outcome ?? const SizedBox.shrink(),
    );
  }
}
