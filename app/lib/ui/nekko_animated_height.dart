import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';

/// Animates height only on an explicit content change. Ordinary child layout
/// (including nested accordions) remains immediate and keeps its own timing.
class NekkoAnimatedHeight extends StatefulWidget {
  const NekkoAnimatedHeight({
    super.key,
    required this.contentKey,
    required this.duration,
    required this.curve,
    required this.child,
    this.enabled = true,
  });

  final Object contentKey;
  final Duration duration;
  final Curve curve;
  final bool enabled;
  final Widget child;

  @override
  State<NekkoAnimatedHeight> createState() => _NekkoAnimatedHeightState();
}

class _NekkoAnimatedHeightState extends State<NekkoAnimatedHeight>
    with SingleTickerProviderStateMixin {
  @override
  Widget build(BuildContext context) => _HeightViewport(
    vsync: this,
    contentKey: widget.contentKey,
    duration: widget.duration,
    curve: widget.curve,
    enabled: widget.enabled,
    child: widget.child,
  );
}

class _HeightViewport extends SingleChildRenderObjectWidget {
  const _HeightViewport({
    required this.vsync,
    required this.contentKey,
    required this.duration,
    required this.curve,
    required this.enabled,
    required super.child,
  });

  final TickerProvider vsync;
  final Object contentKey;
  final Duration duration;
  final Curve curve;
  final bool enabled;

  @override
  RenderObject createRenderObject(BuildContext context) =>
      _RenderHeightViewport(vsync, contentKey, duration, curve, enabled);

  @override
  void updateRenderObject(
    BuildContext context,
    _RenderHeightViewport renderObject,
  ) {
    renderObject.update(contentKey, duration, curve, enabled);
  }
}

class _RenderHeightViewport extends RenderShiftedBox {
  _RenderHeightViewport(
    TickerProvider vsync,
    this._contentKey,
    Duration duration,
    this._curve,
    this._enabled,
  ) : _controller = AnimationController(vsync: vsync, duration: duration),
      super(null) {
    _controller.addListener(() {
      if (_controller.value != _lastLayoutProgress) markNeedsLayout();
    });
  }

  final AnimationController _controller;
  Object _contentKey;
  Curve _curve;
  bool _enabled;
  bool _pending = false;
  bool _transitioning = false;
  double? _naturalHeight;
  double _from = 0;
  double _to = 0;
  double _lastLayoutProgress = 0;

  double get _height => _pending
      ? _from
      : _from + (_to - _from) * _curve.transform(_controller.value);

  void update(Object contentKey, Duration duration, Curve curve, bool enabled) {
    final current = _transitioning ? _height : _naturalHeight;
    _controller.duration = duration;
    _curve = curve;
    if (!enabled) {
      _controller.stop();
      // Closing the parent freezes a tab transition at its visible height.
      // It must not snap to the target before the outer accordion collapses.
      if (_pending) {
        _to = _from;
        _pending = false;
      }
    } else if (!_enabled) {
      // Reopening starts from the current tab's natural size while still hidden.
      _pending = _transitioning = false;
    } else if (_contentKey != contentKey && current != null) {
      _controller.stop();
      _from = current;
      _pending = _transitioning = true;
    }
    _enabled = enabled;
    _contentKey = contentKey;
    markNeedsLayout();
  }

  // SliverFillRemaining asks for intrinsic height before actual layout. It
  // must see the same intermediate height as paint, not the new tab's target.
  @override
  double computeMinIntrinsicHeight(double width) =>
      _transitioning ? _height : super.computeMinIntrinsicHeight(width);

  @override
  double computeMaxIntrinsicHeight(double width) =>
      _transitioning ? _height : super.computeMaxIntrinsicHeight(width);

  @override
  Size computeDryLayout(BoxConstraints constraints) {
    final natural =
        child?.getDryLayout(
          constraints.copyWith(minHeight: 0, maxHeight: double.infinity),
        ) ??
        Size.zero;
    return constraints.constrain(
      Size(natural.width, _transitioning ? _height : natural.height),
    );
  }

  @override
  void performLayout() {
    _lastLayoutProgress = _controller.value;
    final content = child;
    if (content == null) {
      size = constraints.smallest;
      return;
    }
    content.layout(
      constraints.copyWith(minHeight: 0, maxHeight: double.infinity),
      parentUsesSize: true,
    );
    final natural = content.size.height;
    if (_enabled && (_pending || (_transitioning && natural != _to))) {
      final current = _height;
      _from = current;
      _to = natural;
      _pending = false;
      if (_from == _to) {
        _controller.stop();
        _transitioning = false;
      } else {
        // Resetting to the new tween's start is part of this layout, not a
        // request to dirty the object again while it is being laid out.
        _lastLayoutProgress = 0;
        _controller.forward(from: 0);
      }
    }
    _naturalHeight = natural;
    size = constraints.constrain(
      Size(content.size.width, _transitioning ? _height : natural),
    );
    // A tab's heading stays attached to the card top. Bottom alignment would
    // move it instantly by the difference between old and new natural heights.
    (content.parentData! as BoxParentData).offset = Offset.zero;
    if (_transitioning && _controller.isCompleted) _transitioning = false;
  }

  @override
  void paint(PaintingContext context, Offset offset) {
    context.pushClipRect(
      needsCompositing,
      offset,
      Offset.zero & size,
      super.paint,
    );
  }

  @override
  Rect? describeApproximatePaintClip(RenderObject child) => Offset.zero & size;

  @override
  void detach() {
    // A reattached viewport starts at its current child's natural size.
    _controller.stop();
    _pending = _transitioning = false;
    super.detach();
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }
}
