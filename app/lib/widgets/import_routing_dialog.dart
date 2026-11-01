import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';

import '../state/app_state.dart';
import '../theme/tokens.dart';
import '../ui/nekko_dialog.dart';
import '../ui/nekko_dialog_action.dart';
import '../ui/nekko_toast.dart';

/// Dialog allowing the user to paste a JSON snapshot of routing rules and apply it.
class ImportRoutingDialog extends StatefulWidget {
  const ImportRoutingDialog({super.key});

  static Future<bool?> show(BuildContext context) {
    final state = context.read<AppState>();
    return showDialog<bool>(
      context: context,
      barrierColor: Colors.black54,
      builder: (_) => ChangeNotifierProvider<AppState>.value(
        value: state,
        child: const ImportRoutingDialog(),
      ),
    );
  }

  @override
  State<ImportRoutingDialog> createState() => _ImportRoutingDialogState();
}

class _ImportRoutingDialogState extends State<ImportRoutingDialog> {
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
    if (!mounted) return;
    if (text != null && text.isNotEmpty) {
      setState(() {
        _ctrl.text = text;
        _error = null;
      });
    }
  }

  String? _validateJson(String text) {
    if (text.isEmpty) {
      return 'Вставьте JSON-снимок правил';
    }
    try {
      final decoded = json.decode(text);
      if (decoded is! Map<String, dynamic>) {
        return 'Ожидается JSON-объект';
      }
      if (!decoded.containsKey('version')) {
        return 'Отсутствует обязательное поле "version"';
      }
      if (decoded['version'] != 1) {
        return 'Неподдерживаемая версия: ${decoded['version']} (ожидается 1)';
      }
      if (decoded.containsKey('groups') && decoded['groups'] is! Map) {
        return 'Поле "groups" должно быть объектом';
      }
      if (decoded.containsKey('services') && decoded['services'] is! Map) {
        return 'Поле "services" должно быть объектом';
      }
    } on FormatException catch (e) {
      return 'Некорректный JSON: ${e.message}';
    } catch (e) {
      return 'Ошибка проверки: $e';
    }
    return null;
  }

  Future<void> _submit() async {
    final text = _ctrl.text.trim();
    final localValidation = _validateJson(text);
    if (localValidation != null) {
      setState(() => _error = localValidation);
      return;
    }

    setState(() {
      _submitting = true;
      _error = null;
    });

    try {
      final state = context.read<AppState>();
      await state.importRoutingSnapshot(text);
      if (!mounted) return;
      Navigator.of(context).pop(true);
      NekkoToast.show(context, 'Правила импортированы');
    } catch (e) {
      if (!mounted) return;
      var msg = e.toString();
      if (msg.startsWith('Exception: ')) {
        msg = msg.substring('Exception: '.length);
      }
      setState(() {
        _error = msg;
        _submitting = false;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;

    return NekkoDialog(
      title: 'импорт правил',
      content: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Container(
            decoration: BoxDecoration(
              color: t.surface2,
              borderRadius: BorderRadius.circular(t.radius),
              border: Border.all(
                color: _error != null ? t.bad : t.border,
              ),
            ),
            child: TextField(
              key: const ValueKey('import_routing_text_field'),
              controller: _ctrl,
              enabled: !_submitting,
              maxLines: 8,
              minLines: 4,
              style: TextStyle(
                color: t.text,
                fontSize: 12,
                fontFamily: t.monoFamily,
                height: 1.4,
              ),
              decoration: InputDecoration(
                contentPadding: const EdgeInsets.all(12),
                hintText: '{\n  "version": 1,\n  "groups": { ... },\n  "services": { ... }\n}',
                hintStyle: TextStyle(
                  color: t.textFaint,
                  fontSize: 12,
                  fontFamily: t.monoFamily,
                ),
                border: InputBorder.none,
              ),
              onChanged: (_) {
                if (_error != null) {
                  setState(() => _error = null);
                }
              },
            ),
          ),
          if (_error != null) ...[
            const SizedBox(height: 8),
            Text(
              _error!,
              key: const ValueKey('import_routing_error_text'),
              style: TextStyle(
                color: t.bad,
                fontSize: 11.5,
                height: 1.3,
              ),
            ),
          ],
        ],
      ),
      actions: LayoutBuilder(
        builder: (context, constraints) {
          final paste = NekkoDialogAction(
            key: const ValueKey('import_routing_paste_btn'),
            icon: Icons.content_paste,
            label: 'вставить',
            enabled: !_submitting,
            onPressed: _pasteFromClipboard,
          );
          final buttons = Wrap(
            alignment: WrapAlignment.end,
            spacing: 6,
            runSpacing: 6,
            children: [
              NekkoDialogAction(
                label: 'отмена',
                enabled: !_submitting,
                onPressed: () => Navigator.of(context).maybePop(),
              ),
              NekkoDialogAction(
                key: const ValueKey('import_routing_submit_btn'),
                label: 'импортировать',
                enabled: !_submitting,
                busy: _submitting,
                primary: true,
                onPressed: _submit,
              ),
            ],
          );

          if (constraints.maxWidth < 320) {
            return Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Align(alignment: Alignment.centerLeft, child: paste),
                const SizedBox(height: 8),
                Align(alignment: Alignment.centerRight, child: buttons),
              ],
            );
          }

          return Row(
            children: [
              paste,
              const SizedBox(width: 8),
              Expanded(
                child: Align(alignment: Alignment.centerRight, child: buttons),
              ),
            ],
          );
        },
      ),
    );
  }
}
