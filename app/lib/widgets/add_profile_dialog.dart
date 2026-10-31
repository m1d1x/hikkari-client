import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:url_launcher/url_launcher.dart';

import '../state/app_state.dart';
import '../theme/tokens.dart';
import '../ui/nekko_dialog.dart';
import '../ui/nekko_dialog_action.dart';
import 'import_profile_dialog.dart';

/// Fork dialog shown when the user clicks «добавить профиль» or «+ добавить»:
/// lets them choose between purchasing a Hikkari subscription (with ready
/// servers and protocol engine) or importing their own server/subscription.
class AddProfileDialog extends StatelessWidget {
  static final Uri payUri = Uri.parse('https://hikkari.net/pay');

  final Future<bool> Function(Uri uri)? onLaunchUrl;

  const AddProfileDialog({super.key, this.onLaunchUrl});

  static Future<void> show(
    BuildContext context, {
    Future<bool> Function(Uri uri)? onLaunchUrl,
  }) {
    final state = context.read<AppState>();
    return showDialog<void>(
      context: context,
      barrierColor: Colors.black54,
      builder: (_) => ChangeNotifierProvider<AppState>.value(
        value: state,
        child: AddProfileDialog(onLaunchUrl: onLaunchUrl),
      ),
    );
  }

  Future<void> _openSubscriptionPurchase(BuildContext context) async {
    final navigator = Navigator.of(context);
    if (navigator.canPop()) {
      navigator.pop();
    }
    final launcher = onLaunchUrl;
    if (launcher != null) {
      await launcher(payUri);
    } else {
      await launchUrl(payUri, mode: LaunchMode.externalApplication);
    }
  }

  Future<void> _openCustomImport(BuildContext context) async {
    final state = context.read<AppState>();
    final navigator = Navigator.of(context);
    if (navigator.canPop()) {
      navigator.pop();
    }
    await showDialog<void>(
      context: navigator.context,
      barrierColor: Colors.black54,
      builder: (_) => ChangeNotifierProvider<AppState>.value(
        value: state,
        child: const ImportProfileDialog(),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    return NekkoDialog(
      title: 'добавить профиль',
      content: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          _AddOptionCard(
            primary: true,
            icon: Icons.bolt_rounded,
            trailingIcon: Icons.open_in_new_rounded,
            title: 'подключить подписку hikkari',
            subtitle: 'готовые серверы, автообход и движок протоколов',
            onTap: () => _openSubscriptionPurchase(context),
          ),
          const SizedBox(height: 10),
          _AddOptionCard(
            primary: false,
            icon: Icons.link_rounded,
            trailingIcon: Icons.chevron_right_rounded,
            title: 'импортировать свой профиль',
            subtitle:
                'вставить ссылку vless://, vpn:// (Amnezia), trojan://, ss://, hysteria2://, конфиг wireguard/amneziawg или чужую https-подписку',
            onTap: () => _openCustomImport(context),
          ),
        ],
      ),
      actions: Align(
        alignment: Alignment.centerRight,
        child: NekkoDialogAction(
          label: 'отмена',
          enabled: true,
          onPressed: () => Navigator.of(context).maybePop(),
        ),
      ),
    );
  }
}

class _AddOptionCard extends StatefulWidget {
  final bool primary;
  final IconData icon;
  final IconData trailingIcon;
  final String title;
  final String subtitle;
  final VoidCallback onTap;

  const _AddOptionCard({
    required this.primary,
    required this.icon,
    required this.trailingIcon,
    required this.title,
    required this.subtitle,
    required this.onTap,
  });

  @override
  State<_AddOptionCard> createState() => _AddOptionCardState();
}

class _AddOptionCardState extends State<_AddOptionCard> {
  bool _hovered = false;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final primary = widget.primary;
    final bg = primary
        ? (_hovered
              ? Color.alphaBlend(
                  t.primary.withValues(alpha: 0.14),
                  const Color(0xFF1A1014),
                )
              : const Color(0xFF1A1014))
        : (_hovered
              ? Color.alphaBlend(t.primary.withValues(alpha: 0.06), t.surface2)
              : t.surface2);
    final borderColor = primary
        ? t.primary.withValues(alpha: _hovered ? 0.62 : 0.36)
        : (_hovered ? t.primary.withValues(alpha: 0.42) : t.borderStrong);

    return MouseRegion(
      cursor: SystemMouseCursors.click,
      onEnter: (_) => setState(() => _hovered = true),
      onExit: (_) => setState(() => _hovered = false),
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: widget.onTap,
        child: AnimatedContainer(
          duration: AppDurations.fast,
          curve: AppCurves.fastInSlowOut,
          padding: const EdgeInsets.fromLTRB(14, 13, 14, 13),
          decoration: BoxDecoration(
            color: bg,
            borderRadius: BorderRadius.circular(t.radius),
            border: Border.all(color: borderColor),
          ),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Container(
                width: 34,
                height: 34,
                alignment: Alignment.center,
                decoration: BoxDecoration(
                  color: primary
                      ? t.primary.withValues(alpha: 0.15)
                      : t.surface,
                  borderRadius: BorderRadius.circular(10),
                  border: primary ? null : Border.all(color: t.border),
                ),
                child: Icon(
                  widget.icon,
                  size: 18,
                  color: primary
                      ? t.primary
                      : (_hovered ? t.primary : t.textSecondary),
                ),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Row(
                      children: [
                        Expanded(
                          child: Text(
                            widget.title,
                            style: TextStyle(
                              color: primary ? t.primary : t.text,
                              fontSize: 13,
                              fontWeight: FontWeight.w700,
                              height: 1.25,
                            ),
                          ),
                        ),
                        const SizedBox(width: 8),
                        Icon(
                          widget.trailingIcon,
                          size: 16,
                          color: primary
                              ? t.primary.withValues(
                                  alpha: _hovered ? 1.0 : 0.75,
                                )
                              : (_hovered ? t.primary : t.textMuted),
                        ),
                      ],
                    ),
                    const SizedBox(height: 5),
                    Text(
                      widget.subtitle,
                      style: TextStyle(
                        color: t.textMuted,
                        fontSize: 11.5,
                        fontWeight: FontWeight.w500,
                        height: 1.4,
                      ),
                    ),
                  ],
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
