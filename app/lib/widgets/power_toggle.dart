import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../state/app_state.dart';
import '../theme/tokens.dart';

/// The hero on the home screen — a large round Start/Stop button.
///
/// The Svelte version uses an SVG cat that's "grey-out when off, colour
/// when on". The original markup even has a `TODO: заменить на сплайн-
/// анимацию кота-маскота` — that's a separate redesign pass with a real
/// asset/Lottie/Rive animation. For now we ship a placeholder built from
/// gradient + power icon so the layout works end-to-end; the swap is just
/// this widget when the asset is ready.
class PowerToggle extends StatelessWidget {
  const PowerToggle({super.key});

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final s = context.watch<AppState>();
    final running = s.isRunning;
    final busy = s.busy;
    final err = s.lastError;

    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 24),
      child: Column(
        children: [
          SizedBox(
            width: 168,
            height: 168,
            child: Material(
              color: Colors.transparent,
              shape: const CircleBorder(),
              clipBehavior: Clip.antiAlias,
              child: InkWell(
                onTap: busy ? null : () => s.toggleVpn(),
                mouseCursor: busy
                    ? SystemMouseCursors.basic
                    : SystemMouseCursors.click,
                child: AnimatedContainer(
                  duration: const Duration(milliseconds: 300),
                  curve: Curves.easeOut,
                  decoration: BoxDecoration(
                    shape: BoxShape.circle,
                    color: Colors.transparent,
                    border: Border.all(
                      color: running
                          ? t.primary.withValues(alpha: 0.55)
                          : t.border,
                      width: running ? 3 : 1.5,
                    ),
                  ),
                  child: Center(
                    child: Icon(
                      Icons.power_settings_new,
                      size: 64,
                      color: running ? t.primary : t.textMuted,
                    ),
                  ),
                ),
              ),
            ),
          ),
          if (err != null) ...[
            const SizedBox(height: 16),
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 24),
              child: SelectableText(
                err,
                textAlign: TextAlign.center,
                style: TextStyle(
                  color: t.bad,
                  fontFamily: t.monoFamily,
                  fontSize: 11,
                  height: 1.4,
                ),
              ),
            ),
          ],
        ],
      ),
    );
  }
}
