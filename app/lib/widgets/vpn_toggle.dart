import 'package:flutter/widgets.dart';

import '../state/app_state.dart';
import '../ui/nekko_toast.dart';

/// The power button's press: toggles the tunnel and says which other VPN
/// clients the start turned off.
Future<void> toggleVpnWithNotice(BuildContext context, AppState s) async {
  final anchor = NekkoToast.anchor(context);
  final stopped = await s.toggleVpn();
  if (stopped.isEmpty) return;
  anchor.show(
    stopped.length == 1
        ? '${stopped.single} отключён'
        : 'Отключены: ${stopped.join(', ')}',
  );
}
