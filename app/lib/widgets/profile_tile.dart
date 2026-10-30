import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../core/models.dart';
import '../state/app_state.dart';
import '../state/uptime_stats.dart';
import '../theme/tokens.dart';
import '../ui/nekko_toast.dart';
import '../ui/nekko_dialog.dart';
import '../ui/nekko_inline_edit_field.dart';
import 'profile_flag_badge.dart';
import 'profile_share_sheet.dart';
import 'profile_summary_card.dart';
import 'service_ping_sheet.dart';

/// One profile row. Collapsed and home-selected cards share the same visual
/// component; expanded rows add stats, rename, and delete controls.
class ProfileTile extends StatefulWidget {
  final ProfileInfo profile;
  final bool expanded;
  final VoidCallback onToggleExpand;

  const ProfileTile({
    super.key,
    required this.profile,
    required this.expanded,
    required this.onToggleExpand,
  });

  @override
  State<ProfileTile> createState() => _ProfileTileState();
}

class _ProfileTileState extends State<ProfileTile> {
  Future<void> _switchProfile() async {
    final s = context.read<AppState>();
    if (s.busy) return;
    if (widget.profile.active) return;
    await s.switchProfile(widget.profile.name);
    if (!mounted) return;
    final error = context.read<AppState>().lastError;
    if (error == null) return;
    NekkoToast.show(context, error);
  }

  Future<void> _rename(String v) async {
    await context.read<AppState>().renameProfile(widget.profile.name, v);
  }

  Future<void> _confirmDelete() async {
    final ok = await NekkoDialog.confirm(
      context,
      title: 'удалить профиль?',
      message: widget.profile.label,
      confirmLabel: 'удалить',
    );
    if (ok != true) return;
    if (!mounted) return;
    final state = context.read<AppState>();
    await state.deleteProfile(widget.profile.name);
    if (!mounted || state.lastError == null) return;
    NekkoToast.show(context, state.lastError!);
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final s = context.watch<AppState>();
    final hist = s.uptime[widget.profile.name] ?? const <PingSample>[];
    final countryCode = s.countryCodeFor(widget.profile.name);
    final countryLoading = s.isCountryLoading(widget.profile.name);

    // ПКМ по карточке — вместо технических подробностей открывает пинги
    // до популярных сервисов (YouTube, Telegram и т.д.), см. подробности
    // клика ЛКМ/стрелочки — ниже, в footer.
    return GestureDetector(
      onSecondaryTap: () =>
          ServicePingSheet.show(context, widget.profile.label),
      child: ProfileSummaryCard(
        title: widget.profile.label,
        leading: (countryCode == null && !countryLoading)
            ? null
            : ProfileFlagBadge(
                countryCode: countryCode,
                loading: countryLoading,
              ),
        samples: hist,
        selected: widget.profile.active,
        forceChecking: widget.profile.active && s.isWarmingUp,
        onTap: s.busy ? null : _switchProfile,
        // ms и стрелочка — единая зона нажатия на разворачивание подробнос-
        // тей (а не только сама стрелочка, чтобы было проще попасть пальцем).
        onTrailingTap: widget.onToggleExpand,
        margin: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
        padding: const EdgeInsets.fromLTRB(14, 11, 8, 11),
        titleFontSize: 14,
        chartHeight: 32,
        trailing: _ExpandButton(expanded: widget.expanded),
        // _ExpandButton — 34×34, иначе адаптивная раскладка карточки даёт
        // на trailing 18px и trailing вылезает за правый бордер.
        trailingWidth: 34,
        footer: _detail(context, t, hist),
        footerVisible: widget.expanded,
      ),
    );
  }

  Widget _detail(BuildContext context, NekkoTokens t, List<PingSample> hist) {
    final pct = UptimeStats.uptimePct(hist);
    final rtt = UptimeStats.avgRtt(hist);
    return Container(
      padding: const EdgeInsets.fromLTRB(16, 12, 16, 16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          _kv(
            context,
            t,
            'аптайм',
            pct < 0 ? '—' : '$pct%  ·  ${rtt < 0 ? "—" : "$rtt ms"}',
          ),
          _kv(
            context,
            t,
            'сервер',
            widget.profile.endpoint.isEmpty ? '—' : widget.profile.endpoint,
            mono: true,
          ),
          _kv(
            context,
            t,
            'протокол',
            widget.profile.protocol.isEmpty ? '—' : widget.profile.protocol,
          ),
          _labelRow(context, t),
          Padding(
            padding: const EdgeInsets.only(top: 18),
            child: Row(
              children: [
                Expanded(
                  child: _actionButton(
                    t,
                    icon: Icons.ios_share,
                    label: 'поделиться',
                    color: t.text,
                    hover: t.primary,
                    onTap: () =>
                        ProfileShareSheet.show(context, widget.profile),
                  ),
                ),
                const SizedBox(width: 10),
                Expanded(
                  child: _actionButton(
                    t,
                    icon: Icons.delete_outline,
                    label: 'удалить',
                    color: t.bad,
                    hover: t.bad,
                    onTap: _confirmDelete,
                  ),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }

  // Заполненная кнопка действия в развороте (поделиться / удалить). Тянется
  // на половину ширины; фон surface2 + hairline-бордер делают её заметной
  // «плашкой», а не дешёвой текстовой ссылкой. [color] красит иконку/текст,
  // [hover] — цвет подсветки при наведении.
  Widget _actionButton(
    NekkoTokens t, {
    required IconData icon,
    required String label,
    required Color color,
    required Color hover,
    required VoidCallback onTap,
  }) {
    return Material(
      color: t.surface2,
      borderRadius: BorderRadius.circular(t.radius),
      clipBehavior: Clip.antiAlias,
      child: InkWell(
        onTap: onTap,
        mouseCursor: SystemMouseCursors.click,
        hoverColor: hover.withValues(alpha: 0.12),
        child: Container(
          height: 44,
          alignment: Alignment.center,
          decoration: BoxDecoration(
            border: Border.all(color: t.border),
            borderRadius: BorderRadius.circular(t.radius),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(icon, size: 17, color: color),
              const SizedBox(width: 8),
              Text(
                label,
                style: TextStyle(
                  color: color,
                  fontSize: NekkoTokens.sizeBody,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  // Единая высота строки разворота — все строки (kv + «профиль») выровнены
  // по одному ритму, чтобы отступы читались симметрично.
  static const double _rowHeight = 32;

  Widget _kv(
    BuildContext context,
    NekkoTokens t,
    String k,
    String v, {
    bool mono = false,
  }) {
    return SizedBox(
      height: _rowHeight,
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [
          SizedBox(
            width: 80,
            child: Text(k, style: TextStyle(color: t.textMuted, fontSize: 12)),
          ),
          Expanded(
            child: Text(
              v,
              style: TextStyle(
                color: t.text,
                fontSize: 12,
                fontFamily: mono ? t.monoFamily : null,
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _labelRow(BuildContext context, NekkoTokens t) {
    return SizedBox(
      height: _rowHeight,
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [
          // 71 = 80 − (рамка 1 + левый padding 8 внутри поля): компенсирует
          // внутренний отступ инлайн-поля, чтобы имя встало по левому краю как
          // остальные значения в строках выше.
          SizedBox(
            width: 71,
            child: Text(
              'профиль',
              style: TextStyle(color: t.textMuted, fontSize: 12),
            ),
          ),
          Expanded(
            child: NekkoInlineEditField(
              value: widget.profile.label,
              onSubmitted: _rename,
              height: 28,
            ),
          ),
        ],
      ),
    );
  }
}

// Чисто визуальная стрелочка — нажатие обрабатывает вся правая зона
// карточки (ProfileSummaryCard.onTrailingTap), а не сама иконка.
class _ExpandButton extends StatelessWidget {
  final bool expanded;

  const _ExpandButton({required this.expanded});

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return SizedBox(
      width: 34,
      height: 34,
      child: Center(
        child: AnimatedSwitcher(
          duration: const Duration(milliseconds: 120),
          child: Icon(
            expanded ? Icons.keyboard_arrow_up : Icons.keyboard_arrow_down,
            key: ValueKey(expanded),
            color: t.textMuted,
            size: 19,
          ),
        ),
      ),
    );
  }
}
