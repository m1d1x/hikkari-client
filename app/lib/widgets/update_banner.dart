import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:window_manager/window_manager.dart';

import '../state/app_state.dart';
import '../theme/tokens.dart';

/// «Доступна версия X — Перезапустить». Shown only once an update is fully
/// downloaded and verified by HikkariService; downloading happens silently.
///
/// Restarting hands over to the launcher and closes the window through the
/// normal close path (main.dart → onWindowClose), which shuts the engine
/// down; the launcher brings the tunnel back on the new version if it was up.
class UpdateBanner extends StatefulWidget {
  const UpdateBanner({super.key});

  @override
  State<UpdateBanner> createState() => _UpdateBannerState();
}

class _UpdateBannerState extends State<UpdateBanner> {
  bool _restarting = false;

  Future<void> _restart(AppState state) async {
    setState(() => _restarting = true);
    if (await state.restartToUpdate()) {
      await windowManager.close();
      return;
    }
    if (mounted) setState(() => _restarting = false);
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final state = context.watch<AppState>();
    final u = state.updateStatus;
    if (!u.isReady) return const SizedBox.shrink();

    final accent = u.mandatory ? t.warn : t.primary;
    final title = u.mandatory
        ? 'Нужно обновиться до ${u.version}'
        : 'Доступна версия ${u.version}';
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
      child: Container(
        padding: const EdgeInsets.fromLTRB(14, 10, 8, 10),
        decoration: BoxDecoration(
          color: t.surface,
          border: Border.all(color: accent.withValues(alpha: 0.5)),
          borderRadius: BorderRadius.circular(t.radius),
        ),
        child: Row(
          children: [
            Icon(Icons.system_update_alt, size: 18, color: accent),
            const SizedBox(width: 10),
            Expanded(
              child: Text(
                title,
                style: TextStyle(
                  color: t.text,
                  fontSize: 13,
                  fontWeight: FontWeight.w500,
                ),
              ),
            ),
            TextButton(
              onPressed: _restarting ? null : () => _restart(state),
              style: TextButton.styleFrom(foregroundColor: accent),
              child: Text(_restarting ? 'Перезапуск…' : 'Перезапустить'),
            ),
          ],
        ),
      ),
    );
  }
}
