import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';
import 'package:qr_flutter/qr_flutter.dart';

import '../core/models.dart';
import '../state/app_state.dart';
import '../theme/tokens.dart';
import '../ui/nekko_toast.dart';

/// Bottom sheet «поделиться профилем»: QR-код + ссылка-подписка с кнопкой
/// копирования. Открывается из развёрнутой карточки профиля.
///
/// Ссылку отдаёт движок ([AppState.exportProfileUri]); если он не умеет её
/// экспортировать (старый бэкенд без `ExportProfileURI`), приходит null и
/// шит показывает состояние «ссылка недоступна». В dry-run ссылка
/// синтезируется локально, так что модалку видно при отладке дизайна.
class ProfileShareSheet extends StatelessWidget {
  final ProfileInfo profile;

  const ProfileShareSheet({super.key, required this.profile});

  static Future<void> show(BuildContext context, ProfileInfo profile) {
    final t = context.tokens;
    final state = context.read<AppState>();
    return showModalBottomSheet(
      context: context,
      backgroundColor: t.surface,
      barrierColor: Colors.black54,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(t.radiusLg)),
      ),
      builder: (_) => ChangeNotifierProvider<AppState>.value(
        value: state,
        child: ProfileShareSheet(profile: profile),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return SafeArea(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(20, 8, 20, 20),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Center(
              child: Container(
                width: 36,
                height: 4,
                decoration: BoxDecoration(
                  color: t.borderStrong,
                  borderRadius: BorderRadius.circular(2),
                ),
              ),
            ),
            const SizedBox(height: 16),
            Text('поделиться профилем', style: t.sectionTitle),
            const SizedBox(height: 4),
            Text(
              profile.label,
              style:
                  TextStyle(color: t.textMuted, fontSize: NekkoTokens.sizeBody),
            ),
            const SizedBox(height: 18),
            FutureBuilder<String?>(
              future: context.read<AppState>().exportProfileUri(profile.name),
              builder: (ctx, snap) {
                if (snap.connectionState != ConnectionState.done) {
                  return _frame(t, const Center(
                    child: SizedBox(
                      width: 22,
                      height: 22,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    ),
                  ));
                }
                final uri = snap.data;
                if (uri == null || uri.isEmpty) return _unavailable(t);
                return Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    _qr(t, uri),
                    const SizedBox(height: 18),
                    _linkRow(context, t, uri),
                  ],
                );
              },
            ),
          ],
        ),
      ),
    );
  }

  // Квадрат-контейнер фиксированного размера — общий каркас для QR,
  // спиннера и состояния «недоступно», чтобы модалка не прыгала по высоте.
  Widget _frame(NekkoTokens t, Widget child) {
    return Center(
      child: Container(
        width: 220,
        height: 220,
        alignment: Alignment.center,
        decoration: BoxDecoration(
          color: t.bg,
          border: Border.all(color: t.border),
          borderRadius: BorderRadius.circular(t.radius),
        ),
        child: child,
      ),
    );
  }

  Widget _qr(NekkoTokens t, String uri) {
    return Center(
      child: ClipRRect(
        borderRadius: BorderRadius.circular(20),
        child: Container(
          width: 240,
          height: 240,
          color: Colors.white,
          padding: const EdgeInsets.all(10),
          child: Stack(
            alignment: Alignment.center,
            children: [
              // Watermark: розовый силуэт логотипа проступает в БЕЛЫХ
              // (фоновых) зонах. Лежит на белом, поверх — тёмные модули,
              // которые перекрывают лого там, где стоят (тёмные квадратики
              // остаются тёмными, без наложения).
              Opacity(
                opacity: 0.9,
                child: ColorFiltered(
                  colorFilter: ColorFilter.mode(t.primary, BlendMode.srcIn),
                  child: Image.asset(
                    'assets/ico.png',
                    width: 140,
                    height: 140,
                    filterQuality: FilterQuality.medium,
                  ),
                ),
              ),
              // Модули поверх: прозрачный фон (виден белый лист + лого),
              // тёмные КВАДРАТНЫЕ модули, «глаза» — кружочки. H (30%)
              // держит перекрытие логотипом.
              QrImageView(
                data: uri,
                version: QrVersions.auto,
                errorCorrectionLevel: QrErrorCorrectLevel.H,
                // Свой quiet-zone у QR выключаем — рамку даёт контейнер.
                padding: EdgeInsets.zero,
                backgroundColor: Colors.transparent,
                eyeStyle: QrEyeStyle(
                  eyeShape: QrEyeShape.circle,
                  color: t.bg,
                ),
                dataModuleStyle: QrDataModuleStyle(
                  dataModuleShape: QrDataModuleShape.square,
                  color: t.bg,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _unavailable(NekkoTokens t) {
    return _frame(
      t,
      Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(Icons.link_off, size: 40, color: t.textFaint),
          const SizedBox(height: 10),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 20),
            child: Text(
              'движок не смог экспортировать ссылку',
              textAlign: TextAlign.center,
              style: TextStyle(
                  color: t.textMuted, fontSize: NekkoTokens.sizeBody, height: 1.35),
            ),
          ),
        ],
      ),
    );
  }

  Widget _linkRow(BuildContext context, NekkoTokens t, String uri) {
    return Container(
      padding: const EdgeInsets.fromLTRB(12, 10, 6, 10),
      decoration: BoxDecoration(
        color: t.bg,
        border: Border.all(color: t.border),
        borderRadius: BorderRadius.circular(t.radius),
      ),
      child: Row(
        children: [
          Expanded(
            child: Text(
              uri,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(
                color: t.textSecondary,
                fontSize: NekkoTokens.sizeBody,
                fontFamily: t.monoFamily,
              ),
            ),
          ),
          const SizedBox(width: 6),
          Material(
            color: Colors.transparent,
            borderRadius: BorderRadius.circular(t.radius),
            child: InkWell(
              onTap: () async {
                await Clipboard.setData(ClipboardData(text: uri));
                if (!context.mounted) return;
                NekkoToast.show(
                  context,
                  'ссылка скопирована',
                  icon: Icons.check_rounded,
                  hold: NekkoToast.glanceHold,
                );
              },
              borderRadius: BorderRadius.circular(t.radius),
              mouseCursor: SystemMouseCursors.click,
              hoverColor: t.primary.withValues(alpha: 0.10),
              child: Container(
                padding: const EdgeInsets.all(8),
                child: Icon(Icons.copy, size: 18, color: t.text),
              ),
            ),
          ),
        ],
      ),
    );
  }
}
