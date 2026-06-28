import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../state/app_state.dart';
import '../theme/tokens.dart';

/// Top of the screen — brand on the left, current egress IP / status text
/// on the right. Mirrors the Svelte `<header>` block exactly.
///
/// The IP cell is tappable: opens the log overlay (callback-injected by
/// the parent screen so this widget stays presentational).
class BrandHeader extends StatelessWidget {
  final VoidCallback onTapIp;
  const BrandHeader({super.key, required this.onTapIp});

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final h = context.watch<AppState>().health;
    final status = h?.status ?? 'off';

    return Padding(
      padding: const EdgeInsets.fromLTRB(20, 18, 20, 14),
      child: Row(
        children: [
          _Dot(status: status),
          const SizedBox(width: 10),
          Text(
            'Nekko VPN',
            style: TextStyle(
              color: t.text,
              fontSize: 16,
              fontWeight: FontWeight.w600,
              letterSpacing: 0.2,
            ),
          ),
          const Spacer(),
          _IpCell(health: h, onTap: onTapIp),
        ],
      ),
    );
  }
}

class _Dot extends StatelessWidget {
  final String status;
  const _Dot({required this.status});

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final color = switch (status) {
      'ok' => t.ok,
      'connecting' => t.warn,
      'broken' => t.bad,
      _ => t.textFaint,
    };
    return AnimatedContainer(
      duration: const Duration(milliseconds: 250),
      width: 8,
      height: 8,
      decoration: BoxDecoration(
        color: color,
        shape: BoxShape.circle,
      ),
    );
  }
}

class _IpCell extends StatelessWidget {
  final dynamic health; // HealthInfo? — kept dynamic to avoid import here
  final VoidCallback onTap;
  const _IpCell({required this.health, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final status = health?.status ?? 'off';
    final ip = (health?.ip as String?) ?? '';
    final msg = (health?.message as String?) ?? '';

    final (label, color) = switch (status) {
      'off' => ('отключён', t.textMuted),
      'connecting' => ('подключаюсь…', t.warn),
      'broken' => ('⚠ туннель не работает${ip.isEmpty ? "" : " ($ip)"}', t.bad),
      _ => (ip.isEmpty ? '—' : ip, t.text),
    };

    return Tooltip(
      message: msg,
      child: InkWell(
        onTap: onTap,
        mouseCursor: SystemMouseCursors.click,
        borderRadius: BorderRadius.circular(t.radius),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
          decoration: BoxDecoration(
            color: t.surface,
            border: Border.all(color: t.border),
            borderRadius: BorderRadius.circular(t.radius),
          ),
          child: Text(
            label,
            style: TextStyle(
              color: color,
              fontFamily: t.monoFamily,
              fontSize: 13,
            ),
          ),
        ),
      ),
    );
  }
}
