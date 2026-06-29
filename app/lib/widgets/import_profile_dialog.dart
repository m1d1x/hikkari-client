import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';

import '../state/app_state.dart';
import '../theme/tokens.dart';

/// Compact "import by link" dialog. Accepts a single server URI (currently
/// `hysteria2://...`), delegates to AppState.addProfileFromUri, closes
/// itself on success. Errors are surfaced inline so the user can fix the
/// link without losing what they pasted.
class ImportProfileDialog extends StatefulWidget {
  const ImportProfileDialog({super.key});

  static Future<void> show(BuildContext context) {
    final state = context.read<AppState>();
    return showDialog(
      context: context,
      barrierColor: Colors.black54,
      builder: (_) => ChangeNotifierProvider<AppState>.value(
        value: state,
        child: const ImportProfileDialog(),
      ),
    );
  }

  @override
  State<ImportProfileDialog> createState() => _ImportProfileDialogState();
}

class _ImportProfileDialogState extends State<ImportProfileDialog> {
  final _ctrl = TextEditingController();
  String? _error;
  bool _submitting = false;

  @override
  void dispose() {
    _ctrl.dispose();
    super.dispose();
  }

  Future<void> _pasteFromClipboard() async {
    final data = await Clipboard.getData(Clipboard.kTextPlain);
    final text = data?.text?.trim();
    if (text != null && text.isNotEmpty) {
      setState(() {
        _ctrl.text = text;
        _error = null;
      });
    }
  }

  Future<void> _submit() async {
    final uri = _ctrl.text.trim();
    if (uri.isEmpty) {
      setState(() => _error = 'вставь ссылку');
      return;
    }
    setState(() {
      _submitting = true;
      _error = null;
    });
    final state = context.read<AppState>();
    final name = await state.addProfileFromUri(uri);
    if (!mounted) return;
    if (name != null) {
      Navigator.of(context).maybePop();
    } else {
      setState(() {
        _submitting = false;
        _error = state.lastError ?? 'не удалось добавить';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return Dialog(
      backgroundColor: t.surface,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(t.radiusLg),
      ),
      insetPadding: const EdgeInsets.symmetric(horizontal: 24, vertical: 80),
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 480),
        child: Padding(
          padding: const EdgeInsets.fromLTRB(20, 18, 20, 16),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              _header(t),
              const SizedBox(height: 10),
              _hint(t),
              const SizedBox(height: 12),
              _input(t),
              if (_error != null) ...[
                const SizedBox(height: 8),
                Text(
                  _error!,
                  style: TextStyle(color: t.bad, fontSize: 12),
                ),
              ],
              const SizedBox(height: 14),
              _actions(t),
            ],
          ),
        ),
      ),
    );
  }

  Widget _header(NekkoTokens t) => Row(
        children: [
          Text(
            'импорт по ссылке',
            style: TextStyle(
              color: t.text,
              fontSize: 14,
              fontWeight: FontWeight.w600,
            ),
          ),
          const Spacer(),
          IconButton(
            iconSize: 16,
            padding: EdgeInsets.zero,
            constraints: const BoxConstraints(minWidth: 28, minHeight: 28),
            onPressed: _submitting
                ? null
                : () => Navigator.of(context).maybePop(),
            icon: Icon(Icons.close, color: t.textMuted),
          ),
        ],
      );

  Widget _hint(NekkoTokens t) => Text(
        'hysteria2:// · vless:// · trojan:// · ss://',
        style: TextStyle(
          color: t.textFaint,
          fontSize: 11,
          fontFamily: t.monoFamily,
        ),
      );

  Widget _input(NekkoTokens t) => TextField(
        controller: _ctrl,
        enabled: !_submitting,
        maxLines: 4,
        minLines: 3,
        autofocus: true,
        style: TextStyle(
          color: t.text,
          fontSize: 12,
          fontFamily: t.monoFamily,
          height: 1.35,
        ),
        decoration: InputDecoration(
          hintText: 'hysteria2://… или vless://… или ss://…',
          hintStyle: TextStyle(
            color: t.textFaint,
            fontSize: 12,
            fontFamily: t.monoFamily,
          ),
          filled: true,
          fillColor: t.surface2,
          isDense: true,
          contentPadding:
              const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
          border: OutlineInputBorder(
            borderRadius: BorderRadius.circular(t.radius),
            borderSide: BorderSide(color: t.border),
          ),
          enabledBorder: OutlineInputBorder(
            borderRadius: BorderRadius.circular(t.radius),
            borderSide: BorderSide(color: t.border),
          ),
          focusedBorder: OutlineInputBorder(
            borderRadius: BorderRadius.circular(t.radius),
            borderSide: BorderSide(color: t.borderStrong),
          ),
        ),
      );

  Widget _actions(NekkoTokens t) => Row(
        children: [
          TextButton.icon(
            onPressed: _submitting ? null : _pasteFromClipboard,
            icon: Icon(Icons.content_paste, size: 14, color: t.textMuted),
            label: Text(
              'вставить',
              style: TextStyle(color: t.textMuted, fontSize: 12),
            ),
            style: TextButton.styleFrom(
              padding:
                  const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
              minimumSize: Size.zero,
              tapTargetSize: MaterialTapTargetSize.shrinkWrap,
            ),
          ),
          const Spacer(),
          TextButton(
            onPressed: _submitting
                ? null
                : () => Navigator.of(context).maybePop(),
            child: Text(
              'отмена',
              style: TextStyle(color: t.textMuted, fontSize: 12),
            ),
          ),
          const SizedBox(width: 6),
          FilledButton(
            onPressed: _submitting ? null : _submit,
            style: FilledButton.styleFrom(
              backgroundColor: t.text,
              foregroundColor: t.bg,
              padding:
                  const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
              shape: RoundedRectangleBorder(
                borderRadius: BorderRadius.circular(t.radius),
              ),
              minimumSize: Size.zero,
              tapTargetSize: MaterialTapTargetSize.shrinkWrap,
            ),
            child: _submitting
                ? SizedBox(
                    width: 12,
                    height: 12,
                    child: CircularProgressIndicator(
                      strokeWidth: 1.5,
                      valueColor: AlwaysStoppedAnimation(t.bg),
                    ),
                  )
                : const Text('добавить', style: TextStyle(fontSize: 12)),
          ),
        ],
      );
}
