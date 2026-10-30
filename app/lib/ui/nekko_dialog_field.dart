import 'package:flutter/material.dart';

import '../theme/tokens.dart';

/// Shared input styling from the profile import dialog.
class NekkoDialogField extends StatefulWidget {
  final TextEditingController controller;
  final String hint;
  final bool enabled;
  final int minLines;
  final int maxLines;
  final ValueChanged<String>? onSubmitted;
  final ValueChanged<String>? onChanged;

  const NekkoDialogField({
    super.key,
    required this.controller,
    required this.hint,
    this.enabled = true,
    this.minLines = 1,
    this.maxLines = 1,
    this.onSubmitted,
    this.onChanged,
  });

  @override
  State<NekkoDialogField> createState() => _NekkoDialogFieldState();
}

class _NekkoDialogFieldState extends State<NekkoDialogField> {
  final _focus = FocusNode();
  bool _hovered = false;

  @override
  void initState() {
    super.initState();
    _focus.addListener(_onFocus);
  }

  void _onFocus() => setState(() {});

  @override
  void dispose() {
    _focus.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return MouseRegion(
      onEnter: (_) => setState(() => _hovered = true),
      onExit: (_) => setState(() => _hovered = false),
      child: AnimatedContainer(
        duration: AppDurations.hover,
        curve: AppCurves.hover,
        decoration: BoxDecoration(
          color: t.bg,
          borderRadius: BorderRadius.circular(t.radius),
          border: Border.all(
            color: widget.enabled && (_hovered || _focus.hasFocus)
                ? t.borderStrong
                : t.border,
          ),
        ),
        child: TextField(
          controller: widget.controller,
          focusNode: _focus,
          enabled: widget.enabled,
          autofocus: true,
          minLines: widget.minLines,
          maxLines: widget.maxLines,
          onSubmitted: widget.onSubmitted,
          onChanged: widget.onChanged,
          style: TextStyle(
            color: t.text,
            fontSize: 12,
            fontFamily: t.monoFamily,
            height: 1.35,
          ),
          decoration: InputDecoration(
            hintText: widget.hint,
            hintStyle: TextStyle(
              color: t.textFaint,
              fontSize: 12,
              fontFamily: t.monoFamily,
            ),
            filled: true,
            fillColor: Colors.transparent,
            hoverColor: Colors.transparent,
            contentPadding: const EdgeInsets.symmetric(
              horizontal: 14,
              vertical: 16,
            ),
            border: InputBorder.none,
            enabledBorder: InputBorder.none,
            focusedBorder: InputBorder.none,
            disabledBorder: InputBorder.none,
          ),
        ),
      ),
    );
  }
}
