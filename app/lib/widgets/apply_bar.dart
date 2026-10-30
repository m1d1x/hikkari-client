import 'package:flutter/material.dart';

/// Stub. The "apply pending" status now lives inside [RouteSection] as a
/// footer next to the adder — closer to where the edits happen. This widget
/// stays as a hollow shell so the call site on the home screen keeps
/// compiling; delete it (and its reference) when we're confident no global
/// banner will come back.
class ApplyBar extends StatelessWidget {
  const ApplyBar({super.key});

  @override
  Widget build(BuildContext context) => const SizedBox.shrink();
}
