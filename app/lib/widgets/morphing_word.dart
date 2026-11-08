import 'package:flutter/material.dart';

/// A status word that changes softly and never moves sideways.
///
/// - Shown: the word fades in, rising a few pixels.
/// - Changed: the shared beginning stays put and only the ending crossfades —
///   "подключаем" → "подключено" swaps just "аем" for "ено".
/// - Hidden (`text: null`): the reverse of showing.
///
/// The word sits left-aligned in a box as wide as [reserve], and that box is
/// what gets centred, so words of different length share one left edge.
class MorphingWord extends StatefulWidget {
  const MorphingWord({
    super.key,
    required this.text,
    required this.reserve,
    required this.style,
  });

  final String? text;

  /// The widest word this slot shows; sets the box width.
  final String reserve;
  final TextStyle style;

  @override
  State<MorphingWord> createState() => _MorphingWordState();
}

class _MorphingWordState extends State<MorphingWord>
    with SingleTickerProviderStateMixin {
  static const _shift = 5.0;

  late final AnimationController _run = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 380),
  );

  String? _from;
  String? _to;
  int _shared = 0;

  @override
  void initState() {
    super.initState();
    _to = widget.text;
    _run.value = 1;
  }

  @override
  void didUpdateWidget(MorphingWord old) {
    super.didUpdateWidget(old);
    if (old.text != widget.text) _morph(widget.text);
  }

  void _morph(String? next) {
    // An interrupted run restarts from its target: a half-shown word counts
    // as shown, a half-hidden one is dropped rather than brought back.
    final from = _to;
    var shared = 0;
    if (from != null && next != null) {
      final n = from.length < next.length ? from.length : next.length;
      while (shared < n && from[shared] == next[shared]) {
        shared++;
      }
    }
    setState(() {
      _from = from;
      _to = next;
      _shared = shared;
    });
    _run.forward(from: 0).whenComplete(() {
      if (mounted) setState(() => _from = null);
    });
  }

  @override
  void dispose() {
    _run.dispose();
    super.dispose();
  }

  Widget _part(String text, double opacity, double dy) => Opacity(
    opacity: opacity.clamp(0.0, 1.0),
    child: Transform.translate(
      offset: Offset(0, dy),
      child: Text(text, style: widget.style, maxLines: 1, softWrap: false),
    ),
  );

  @override
  Widget build(BuildContext context) {
    final box = TextPainter(
      text: TextSpan(text: widget.reserve, style: widget.style),
      textDirection: TextDirection.ltr,
      textScaler: MediaQuery.textScalerOf(context),
      maxLines: 1,
    )..layout();
    return SizedBox(
      width: box.width,
      height: box.height,
      child: AnimatedBuilder(
        animation: _run,
        builder: (context, _) {
          final t = Curves.easeOutCubic.transform(_run.value);
          final to = _to;
          final from = _from;
          final hiding = to == null;
          // Showing or hiding moves the whole word; a change moves its end.
          final stay = hiding || from == null ? '' : to.substring(0, _shared);
          final enter = hiding ? '' : to.substring(stay.length);
          final leave = from == null ? '' : from.substring(stay.length);
          return Stack(
            clipBehavior: Clip.none,
            children: [
              Positioned(
                left: 0,
                top: 0,
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    if (stay.isNotEmpty) _part(stay, 1, 0),
                    Stack(
                      clipBehavior: Clip.none,
                      children: [
                        if (leave.isNotEmpty)
                          // Leaving: up and out on a change, back down (the
                          // way it came) when the word hides.
                          _part(leave, 1 - t, (hiding ? _shift : -_shift) * t),
                        if (enter.isNotEmpty) _part(enter, t, _shift * (1 - t)),
                      ],
                    ),
                  ],
                ),
              ),
            ],
          );
        },
      ),
    );
  }
}
