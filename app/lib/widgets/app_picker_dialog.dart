import 'dart:async';

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../core/app_discovery.dart';
import '../state/app_state.dart';
import '../theme/tokens.dart';

class AppPickerDialog extends StatefulWidget {
  final Set<String> alreadyAdded;
  final List<DiscoveredApp>? cachedApps;
  const AppPickerDialog({
    super.key,
    this.alreadyAdded = const {},
    this.cachedApps,
  });

  static Future<String?> show(
    BuildContext context, {
    Set<String> alreadyAdded = const {},
    List<DiscoveredApp>? cachedApps,
  }) {
    return showDialog<String>(
      context: context,
      builder: (_) => ChangeNotifierProvider.value(
        value: context.read<AppState>(),
        child: AppPickerDialog(
          alreadyAdded: alreadyAdded,
          cachedApps: cachedApps,
        ),
      ),
    );
  }

  @override
  State<AppPickerDialog> createState() => _AppPickerDialogState();
}

class _AppPickerDialogState extends State<AppPickerDialog> {
  List<DiscoveredApp> _mainApps = [];
  List<DiscoveredApp> _extraApps = [];
  bool _loading = true;
  bool _showExtra = false;
  bool _extraLoading = false;
  String _query = '';
  final _searchCtl = TextEditingController();
  final _scrollCtl = ScrollController();
  Timer? _pollTimer;

  @override
  void initState() {
    super.initState();
    if (widget.cachedApps != null && widget.cachedApps!.isNotEmpty) {
      _mainApps = widget.cachedApps!;
      _loading = false;
    } else {
      _loadMain();
    }
    _startPolling();
  }

  void _startPolling() {
    _pollTimer = Timer.periodic(const Duration(seconds: 3), (_) {
      _poll();
    });
  }

  Future<void> _poll() async {
    final apps = await context.read<AppState>().core.listApps();
    if (!mounted) return;
    setState(() => _mainApps = apps);
    if (_showExtra && !_extraLoading) {
      final all = await context.read<AppState>().core.listApps(all: true);
      if (!mounted) return;
      final mainExes = _mainApps.map((a) => a.exe.toLowerCase()).toSet();
      setState(() {
        _extraApps =
            all.where((a) => !mainExes.contains(a.exe.toLowerCase())).toList();
      });
    }
  }

  Future<void> _loadMain() async {
    final apps = await context.read<AppState>().core.listApps();
    if (!mounted) return;
    setState(() {
      _mainApps = apps;
      _loading = false;
    });
  }

  Future<void> _loadExtra() async {
    setState(() {
      _showExtra = true;
      _extraLoading = true;
    });
    final all = await context.read<AppState>().core.listApps(all: true);
    if (!mounted) return;
    final mainExes = _mainApps.map((a) => a.exe.toLowerCase()).toSet();
    final complement =
        all.where((a) => !mainExes.contains(a.exe.toLowerCase())).toList();
    setState(() {
      _extraApps = complement;
      _extraLoading = false;
    });
    _scrollToExtra();
  }

  void _scrollToExtra() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted || !_scrollCtl.hasClients) return;
      final target = _scrollCtl.position.maxScrollExtent;
      _scrollCtl.animateTo(
        target,
        duration: const Duration(milliseconds: 200),
        curve: Curves.easeOutCubic,
      );
    });
  }

  @override
  void dispose() {
    _pollTimer?.cancel();
    _searchCtl.dispose();
    _scrollCtl.dispose();
    super.dispose();
  }

  List<_Entry> _filter(List<DiscoveredApp> apps) {
    final addedLower =
        widget.alreadyAdded.map((e) => e.toLowerCase()).toSet();
    final result = apps
        .map((a) => _Entry(
              app: a,
              added: addedLower.contains(a.exe.toLowerCase()),
            ))
        .toList();
    if (_query.isEmpty) return result;
    final q = _query.toLowerCase();
    return result
        .where((d) =>
            d.app.name.toLowerCase().contains(q) ||
            d.app.exe.toLowerCase().contains(q))
        .toList();
  }

  int _listLength(List<_Entry> main, List<_Entry> extra) {
    var count = main.length + 1;
    if (_showExtra) {
      count += _extraLoading ? 1 : extra.length;
    }
    return count;
  }

  Widget _listItem(
      NekkoTokens t, int i, List<_Entry> main, List<_Entry> extra) {
    if (i < main.length) {
      return SizedBox(
        height: 52,
        child: _AppTile(
          entry: main[i],
          onTap: main[i].added
              ? null
              : () => Navigator.of(context).pop(main[i].app.exe),
        ),
      );
    }
    if (i == main.length) {
      return _ShowAllDivider(
        expanded: _showExtra,
        onTap: () {
          if (_showExtra) {
            setState(() {
              _showExtra = false;
              _extraApps = [];
            });
          } else {
            _loadExtra();
          }
        },
      );
    }
    if (_extraLoading) {
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 16),
        child: Center(
          child: SizedBox(
            width: 16,
            height: 16,
            child: CircularProgressIndicator(
              strokeWidth: 2, color: t.textMuted,
            ),
          ),
        ),
      );
    }
    final ei = i - main.length - 1;
    if (ei < 0 || ei >= extra.length) return const SizedBox.shrink();
    return SizedBox(
      height: 52,
      child: _AppTile(
        entry: extra[ei],
        onTap: extra[ei].added
            ? null
            : () => Navigator.of(context).pop(extra[ei].app.exe),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final mainItems = _filter(_mainApps);
    final extraItems = _showExtra ? _filter(_extraApps) : <_Entry>[];

    return Dialog(
      elevation: 0,
      backgroundColor: t.surface,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(t.radiusLg),
        side: BorderSide(color: t.border),
      ),
      child: SizedBox(
        width: 400,
        height: 500,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 18, 20, 0),
              child: Row(
                children: [
                  Text(
                    'выбрать приложение',
                    style: TextStyle(
                      color: t.text,
                      fontSize: 15,
                      fontWeight: FontWeight.w700,
                    ),
                  ),
                  const Spacer(),
                  if (!_loading)
                    Text(
                      '${_mainApps.length}',
                      style: TextStyle(color: t.textFaint, fontSize: 12),
                    ),
                ],
              ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 14, 16, 8),
              child: Container(
                height: 40,
                decoration: BoxDecoration(
                  color: t.bg,
                  border: Border.all(color: t.border),
                  borderRadius: BorderRadius.circular(t.radius),
                ),
                clipBehavior: Clip.antiAlias,
                child: Material(
                  type: MaterialType.transparency,
                  child: TextField(
                    controller: _searchCtl,
                    autofocus: true,
                    style: TextStyle(
                      color: t.text,
                      fontFamily: t.monoFamily,
                      fontSize: 12,
                      fontWeight: FontWeight.w500,
                      letterSpacing: 0.02 * 12,
                    ),
                    cursorColor: t.text,
                    cursorWidth: 1.5,
                    cursorRadius: const Radius.circular(1),
                    onChanged: (v) => setState(() => _query = v),
                    decoration: InputDecoration(
                      isDense: true,
                      filled: true,
                      fillColor: Colors.transparent,
                      hoverColor: Colors.transparent,
                      focusColor: Colors.transparent,
                      contentPadding:
                          const EdgeInsets.fromLTRB(14, 13, 14, 9),
                      border: InputBorder.none,
                      enabledBorder: InputBorder.none,
                      focusedBorder: InputBorder.none,
                      hintText: 'поиск…',
                      hintStyle: TextStyle(
                        color: t.textFaint,
                        fontFamily: t.monoFamily,
                        fontSize: 12,
                        fontWeight: FontWeight.w500,
                        letterSpacing: 0.02 * 12,
                      ),
                    ),
                  ),
                ),
              ),
            ),
            Expanded(
              child: _loading
                  ? Center(
                      child: SizedBox(
                        width: 20,
                        height: 20,
                        child: CircularProgressIndicator(
                          strokeWidth: 2,
                          color: t.textMuted,
                        ),
                      ),
                    )
                  : mainItems.isEmpty && extraItems.isEmpty && !_showExtra
                      ? Center(
                          child: Text(
                            'ничего не найдено',
                            style: TextStyle(
                                color: t.textFaint, fontSize: 12),
                          ),
                        )
                      : ListView.builder(
                          controller: _scrollCtl,
                          padding: const EdgeInsets.fromLTRB(8, 0, 8, 8),
                          itemCount: _listLength(mainItems, extraItems),
                          itemBuilder: (_, i) =>
                              _listItem(t, i, mainItems, extraItems),
                        ),
            ),
          ],
        ),
      ),
    );
  }
}

class _Entry {
  final DiscoveredApp app;
  final bool added;
  const _Entry({required this.app, required this.added});
}

class _AppTile extends StatefulWidget {
  final _Entry entry;
  final VoidCallback? onTap;
  const _AppTile({required this.entry, this.onTap});

  @override
  State<_AppTile> createState() => _AppTileState();
}

class _AppTileState extends State<_AppTile> {
  bool _hover = false;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final e = widget.entry;
    final dim = e.added;

    return MouseRegion(
      cursor:
          dim ? SystemMouseCursors.basic : SystemMouseCursors.click,
      onEnter: (_) {
        if (!dim) setState(() => _hover = true);
      },
      onExit: (_) => setState(() => _hover = false),
      child: GestureDetector(
        onTap: widget.onTap,
        behavior: HitTestBehavior.opaque,
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 100),
          padding:
              const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
          decoration: BoxDecoration(
            color: _hover ? t.surface2 : t.surface,
            borderRadius: BorderRadius.circular(t.radius),
          ),
          child: Row(
            children: [
              SizedBox(
                width: 28,
                height: 28,
                child: e.app.iconBytes != null
                    ? ClipRRect(
                        borderRadius: BorderRadius.circular(4),
                        child: Image.memory(
                          e.app.iconBytes!,
                          width: 28,
                          height: 28,
                          filterQuality: FilterQuality.medium,
                          errorBuilder: (_, __, ___) => Icon(
                            Icons.apps,
                            size: 20,
                            color: dim ? t.textFaint : t.textMuted,
                          ),
                        ),
                      )
                    : Icon(
                        Icons.apps,
                        size: 20,
                        color: dim ? t.textFaint : t.textMuted,
                      ),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  mainAxisAlignment: MainAxisAlignment.center,
                  children: [
                    Text(
                      e.app.name,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        color: dim ? t.textFaint : t.text,
                        fontSize: 13,
                        fontWeight: FontWeight.w500,
                      ),
                    ),
                    const SizedBox(height: 1),
                    Text(
                      e.app.exe,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        color: dim
                            ? t.textFaint.withValues(alpha: 0.5)
                            : t.textMuted,
                        fontFamily: t.monoFamily,
                        fontSize: 11,
                      ),
                    ),
                  ],
                ),
              ),
              if (dim)
                Text(
                  'добавлено',
                  style:
                      TextStyle(color: t.textFaint, fontSize: 11),
                ),
            ],
          ),
        ),
      ),
    );
  }
}

class _ShowAllDivider extends StatefulWidget {
  final bool expanded;
  final VoidCallback onTap;
  const _ShowAllDivider({required this.expanded, required this.onTap});

  @override
  State<_ShowAllDivider> createState() => _ShowAllDividerState();
}

class _ShowAllDividerState extends State<_ShowAllDivider> {
  bool _hover = false;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 6, horizontal: 4),
      child: MouseRegion(
        cursor: SystemMouseCursors.click,
        onEnter: (_) => setState(() => _hover = true),
        onExit: (_) => setState(() => _hover = false),
        child: GestureDetector(
          onTap: widget.onTap,
          behavior: HitTestBehavior.opaque,
          child: Row(
            children: [
              Expanded(child: Divider(color: t.border, height: 1)),
              const SizedBox(width: 10),
              AnimatedDefaultTextStyle(
                duration: const Duration(milliseconds: 100),
                style: TextStyle(
                  color: _hover ? t.text : t.textMuted,
                  fontSize: 12,
                  fontWeight: FontWeight.w500,
                ),
                child: Text(widget.expanded
                    ? 'скрыть'
                    : 'показать все процессы'),
              ),
              const SizedBox(width: 4),
              AnimatedRotation(
                duration: const Duration(milliseconds: 150),
                turns: widget.expanded ? 0.5 : 0,
                child: Icon(
                  Icons.keyboard_arrow_down,
                  size: 16,
                  color: _hover ? t.text : t.textMuted,
                ),
              ),
              const SizedBox(width: 10),
              Expanded(child: Divider(color: t.border, height: 1)),
            ],
          ),
        ),
      ),
    );
  }
}
