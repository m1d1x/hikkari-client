import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../state/app_state.dart';
import '../theme/tokens.dart';

/// "Изменения не применены — применить" banner. Only renders when whitelist
/// has uncommitted edits. Tapping the button restarts the core to apply.
class ApplyBar extends StatelessWidget {
  const ApplyBar({super.key});

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final s = context.watch<AppState>();
    if (!s.whitelistDirty) return const SizedBox.shrink();

    return Container(
      margin: const EdgeInsets.fromLTRB(16, 4, 16, 12),
      padding: const EdgeInsets.fromLTRB(14, 10, 6, 10),
      decoration: BoxDecoration(
        color: t.surface,
        border: Border.all(color: t.warn.withValues(alpha: 0.6)),
        borderRadius: BorderRadius.circular(t.radius),
      ),
      child: Row(
        children: [
          Icon(Icons.info_outline, color: t.warn, size: 16),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              'изменения не применены',
              style: TextStyle(color: t.text, fontSize: 12),
            ),
          ),
          FilledButton(
            onPressed: s.busy ? null : () => s.applyRules(),
            style: FilledButton.styleFrom(
              backgroundColor: t.warn,
              foregroundColor: Colors.black,
              overlayColor: Colors.transparent,
              padding:
                  const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(10),
              ),
            ),
            child: const Text('применить'),
          ),
        ],
      ),
    );
  }
}
