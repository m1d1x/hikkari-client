import 'dart:async';
import 'dart:io' show Platform;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';

import '../state/app_state.dart';
import '../theme/tokens.dart';
import '../ui/nekko_dialog.dart';
import '../ui/nekko_toast.dart';

enum ComponentHealth { ok, warning, error, checking }

class _LogEntry {
  final DateTime timestamp;
  final String level; // OK, INFO, WARN, ERR, FIX
  final String message;

  _LogEntry(this.level, this.message) : timestamp = DateTime.now();

  String get formattedTime {
    final h = timestamp.hour.toString().padLeft(2, '0');
    final m = timestamp.minute.toString().padLeft(2, '0');
    final s = timestamp.second.toString().padLeft(2, '0');
    return '$h:$m:$s';
  }
}

/// Network Doctor and System Auto-Fix modal dialog.
/// Diagnoses host OS network issues (stale system proxy, loopback DNS,
/// dead virtual adapter interfaces, routing conflicts) and provides
/// one-click automatic fixes.
class NetworkDoctorDialog extends StatefulWidget {
  final Future<void> Function(_NetworkDoctorDialogState state)?
  diagnosticRunner;
  final Future<void> Function(_NetworkDoctorDialogState state)?
  softRepairRunner;

  const NetworkDoctorDialog({
    super.key,
    this.diagnosticRunner,
    this.softRepairRunner,
  });

  static Future<void> show(
    BuildContext context, {
    Future<void> Function(_NetworkDoctorDialogState state)? diagnosticRunner,
    Future<void> Function(_NetworkDoctorDialogState state)? softRepairRunner,
  }) {
    final state = context.read<AppState>();
    return showDialog<void>(
      context: context,
      barrierColor: Colors.black54,
      builder: (_) => ChangeNotifierProvider<AppState>.value(
        value: state,
        child: NetworkDoctorDialog(
          diagnosticRunner: diagnosticRunner,
          softRepairRunner: softRepairRunner,
        ),
      ),
    );
  }

  @override
  State<NetworkDoctorDialog> createState() => _NetworkDoctorDialogState();
}

class _NetworkDoctorDialogState extends State<NetworkDoctorDialog> {
  final ScrollController _logScroll = ScrollController();
  final List<_LogEntry> _logs = [];

  ComponentHealth _proxyHealth = ComponentHealth.checking;
  ComponentHealth _dnsHealth = ComponentHealth.checking;
  ComponentHealth _adapterHealth = ComponentHealth.checking;
  ComponentHealth _coreHealth = ComponentHealth.checking;

  bool _isFixing = false;
  bool _isDiagnosing = false;
  bool _sendingSupport = false;

  /// macOS has other checks and another deep reset than Windows. The engine's
  /// report says which apply; until it arrives, the host OS does.
  bool _isMac = Platform.isMacOS;

  /// A click that closes the window must be deliberate: a stray or double
  /// click on the dimmed area right after the window appears, or right after
  /// a button press, would drop the next click onto the power button behind
  /// it. A short lock covers that; a long one would trap the user in a
  /// check that hangs.
  static const _closeLockFor = Duration(seconds: 1);
  bool _closeLocked = false;
  Timer? _closeLock;

  void _lockClose() {
    _closeLock?.cancel();
    if (!_closeLocked) setState(() => _closeLocked = true);
    _closeLock = Timer(_closeLockFor, () {
      if (mounted) setState(() => _closeLocked = false);
    });
  }

  @override
  void initState() {
    super.initState();
    _closeLocked = true;
    _closeLock = Timer(_closeLockFor, () {
      if (mounted) setState(() => _closeLocked = false);
    });
    _appendLog(
      'INFO',
      'запуск модуля диагностики сети (System Network Doctor)',
    );
    WidgetsBinding.instance.addPostFrameCallback((_) => _runDiagnostics());
  }

  @override
  void dispose() {
    _closeLock?.cancel();
    _logScroll.dispose();
    super.dispose();
  }

  void _appendLog(String level, String message) {
    if (!mounted) return;
    setState(() {
      _logs.add(_LogEntry(level, message));
    });
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_logScroll.hasClients) {
        _logScroll.animateTo(
          _logScroll.position.maxScrollExtent,
          duration: const Duration(milliseconds: 150),
          curve: Curves.easeOut,
        );
      }
    });
  }

  void appendLog(String level, String message) => _appendLog(level, message);
  void setProxyHealth(ComponentHealth h, [String? _]) =>
      setState(() => _proxyHealth = h);
  void setDnsHealth(ComponentHealth h, [String? _]) =>
      setState(() => _dnsHealth = h);
  void setAdapterHealth(ComponentHealth h, [String? _]) =>
      setState(() => _adapterHealth = h);
  void setCoreHealth(ComponentHealth h, [String? _]) =>
      setState(() => _coreHealth = h);

  Future<void> _runDiagnostics() async {
    if (_isDiagnosing) return;
    _lockClose();
    setState(() {
      _isDiagnosing = true;
      _proxyHealth = ComponentHealth.checking;
      _dnsHealth = ComponentHealth.checking;
      _adapterHealth = ComponentHealth.checking;
      _coreHealth = ComponentHealth.checking;
    });

    if (widget.diagnosticRunner != null) {
      await widget.diagnosticRunner!(this);
      if (mounted) setState(() => _isDiagnosing = false);
      return;
    }

    _appendLog('INFO', '--- начало полной проверки системы ---');
    final state = context.read<AppState>();

    // Log lines never carry addresses or adapter names: the log goes to
    // support as is.
    try {
      final rep = await state.networkDiagnose();
      if (!mounted) return;
      _isMac = rep['os'] == 'darwin';
      if (rep['simulated'] == true) {
        _appendLog(
          'INFO',
          'режим без движка: результаты проверки симулированы',
        );
      }
      _applyProxyVerdict(
        ((rep['proxy'] as Map?)?['verdict'] as String?) ?? 'none',
        supported: rep['supported'] != false,
      );
      final supported = rep['supported'] != false;
      _applyHost(
        (rep['host'] as Map?)?.cast<String, dynamic>() ?? const {},
        supported: supported,
      );
      if (supported) {
        _applyDirect(
          (rep['direct'] as Map?)?.cast<String, dynamic>() ?? const {},
          tunnelUp: state.isRunning,
        );
      }
      final overrides = (rep['hostsOverrides'] as int?) ?? -1;
      if (overrides > 0) {
        _appendLog(
          'WARN',
          'файл hosts: $overrides запис${overrides == 1 ? "ь" : "ей"} подменяет популярные домены: «глубокий сброс» отключит их',
        );
      }
      if (supported) _applySystemScan(rep);
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _proxyHealth = ComponentHealth.warning;
        _dnsHealth = ComponentHealth.warning;
        _adapterHealth = ComponentHealth.warning;
      });
      _appendLog('ERR', 'проверка сети не выполнена: $e');
    }

    // Core state
    final lastError = state.lastError;
    if (lastError != null && lastError.isNotEmpty) {
      setState(() => _coreHealth = ComponentHealth.error);
      _appendLog('ERR', 'ядро: зафиксирована ошибка: $lastError');
    } else {
      setState(() => _coreHealth = ComponentHealth.ok);
      _appendLog(
        'OK',
        'ядро: движок ${state.engineId}, статус: ${state.isRunning ? "активен" : "отключен"}',
      );
    }

    _appendLog('INFO', '--- диагностика завершена ---');
    if (mounted) {
      setState(() => _isDiagnosing = false);
    }
  }

  void _applyProxyVerdict(String verdict, {required bool supported}) {
    if (!supported) {
      setState(() => _proxyHealth = ComponentHealth.ok);
      _appendLog('INFO', 'системный прокси: проверка не требуется на этой ОС');
      return;
    }
    switch (verdict) {
      case 'orphan':
        setState(() => _proxyHealth = ComponentHealth.warning);
        _appendLog(
          'WARN',
          'системный прокси указывает на неработающий локальный порт: «починить сеть» отключит его',
        );
      case 'live':
        setState(() => _proxyHealth = ComponentHealth.ok);
        _appendLog(
          'OK',
          'системный прокси: работающий локальный прокси, не трогаем',
        );
      case 'remote':
        setState(() => _proxyHealth = ComponentHealth.ok);
        _appendLog(
          'OK',
          'системный прокси: внешний (корпоративный), не трогаем',
        );
      default:
        setState(() => _proxyHealth = ComponentHealth.ok);
        _appendLog('OK', 'системный прокси: не настроен');
    }
  }

  bool _adapterChecked = false;
  bool _dnsChecked = false;

  // A check that did not run is not a good result: only a platform without
  // the check at all stays green.
  ComponentHealth _unchecked(bool supported) =>
      supported ? ComponentHealth.warning : ComponentHealth.ok;

  void _applyHost(Map<String, dynamic> host, {required bool supported}) {
    _adapterChecked = (host['iface'] as String? ?? 'unchecked') != 'unchecked';
    _dnsChecked = (host['dns'] as String? ?? 'unchecked') != 'unchecked';
    final probed = host['probed'] == true;
    final when = probed ? '' : ' (по данным последнего подключения)';
    switch (host['iface'] as String? ?? 'unchecked') {
      case 'auto':
        setState(() => _adapterHealth = ComponentHealth.ok);
        _appendLog('OK', 'сетевой адаптер: основной адаптер работает$when');
      case 'pinned':
        setState(() => _adapterHealth = ComponentHealth.ok);
        _appendLog(
          'FIX',
          'сетевой адаптер: основной адаптер не пропускает трафик, прямой трафик идёт через рабочий$when',
        );
      case 'none_reachable':
        setState(() => _adapterHealth = ComponentHealth.error);
        _appendLog(
          'ERR',
          'сетевой адаптер: ни один адаптер не выходит в интернет$when',
        );
      default:
        setState(() => _adapterHealth = _unchecked(supported));
        _appendLog('INFO', 'сетевой адаптер: проверка не выполнялась');
    }
    switch (host['dns'] as String? ?? 'unchecked') {
      case 'system':
        setState(() => _dnsHealth = ComponentHealth.ok);
        _appendLog('OK', 'DNS: системный резолвер отвечает$when');
      case 'fallback':
        setState(() => _dnsHealth = ComponentHealth.ok);
        _appendLog(
          'FIX',
          'DNS: системный резолвер не отвечает, для прямого трафика включён резервный$when',
        );
      case 'unreachable':
        setState(() => _dnsHealth = ComponentHealth.error);
        _appendLog(
          'ERR',
          'DNS: не отвечает ни системный, ни резервный резолвер$when',
        );
      default:
        setState(() => _dnsHealth = _unchecked(supported));
        _appendLog('INFO', 'DNS: проверка не выполнялась');
    }
  }

  // What the direct path really reaches. It outranks the settings above: they
  // can all look right on a machine where nothing opens.
  void _applyDirect(Map<String, dynamic> direct, {required bool tunnelUp}) {
    if (direct['checked'] != true) {
      _appendLog('INFO', 'прямой интернет: проверка не выполнена');
      return;
    }
    if (direct['internet'] != true) {
      setState(() => _adapterHealth = ComponentHealth.error);
      _appendLog(
        'ERR',
        'прямой интернет: сайты мимо VPN не открываются, приложения, идущие напрямую, работать не будут',
      );
      return;
    }
    _appendLog('OK', 'прямой интернет: сайты мимо VPN открываются');
    // Attached to a tunnel already running there is no selection to report,
    // and sites reached by name through the direct path answer for both cards.
    setState(() {
      if (!_adapterChecked) _adapterHealth = ComponentHealth.ok;
      if (!_dnsChecked) _dnsHealth = ComponentHealth.ok;
    });
    final blocked =
        (direct['secureDnsBlocked'] as List?)?.cast<String>() ?? const [];
    if (blocked.isEmpty) return;
    final names = blocked.join(', ');
    if (tunnelUp) {
      _appendLog(
        'FIX',
        'безопасный DNS ($names) недоступен у провайдера: браузеры, идущие напрямую, получают адреса через VPN',
      );
    } else {
      setState(() => _dnsHealth = ComponentHealth.warning);
      _appendLog(
        'WARN',
        'безопасный DNS ($names) недоступен у провайдера: без VPN браузер с этим DNS не откроет ни один сайт. Включите VPN или отключите «безопасный DNS» в настройках браузера',
      );
    }
  }

  // DPI tools and foreign WFP providers go only to the log: no card turns red
  // over the user's own software. Null means the check could not run, which
  // must not read as «nothing found».
  void _applySystemScan(Map<String, dynamic> rep) {
    final scanError = rep['scanError'] as String?;
    if (scanError != null && scanError.isNotEmpty) {
      setState(() {
        if (_adapterHealth == ComponentHealth.ok) {
          _adapterHealth = ComponentHealth.warning;
        }
      });
      _appendLog('ERR', 'проверка системы службой не выполнена: $scanError');
    }
    final dpi = (rep['dpiTools'] as List?)?.cast<String>();
    if (_isMac) {
      // The tools the deep reset stops are Windows drivers: nothing to report.
    } else if (dpi == null) {
      _appendLog(
        'INFO',
        'сторонние сетевые фильтры: проверка не выполнена',
      );
    } else if (dpi.isEmpty) {
      _appendLog('OK', 'сторонние сетевые фильтры: не обнаружены');
    } else {
      _appendLog(
        'WARN',
        'сторонние сетевые фильтры: ${dpi.join(", ")}: «глубокий сброс» остановит их и переведёт в ручной запуск',
      );
    }
    final wfp = (rep['foreignWfp'] as List?)?.cast<String>();
    final firewall = _isMac
        ? 'сторонние правила pf'
        : 'сторонние WFP-провайдеры';
    if (wfp == null) {
      _appendLog('INFO', '$firewall: проверка не выполнена');
    } else if (wfp.isEmpty) {
      _appendLog('OK', '$firewall: не обнаружены');
    } else {
      _appendLog(
        'INFO',
        '$firewall: ${wfp.join(", ")}: только для сведения, не трогаем',
      );
    }
  }

  static const _actionLabels = {
    'proxy_disable': 'отключение зависшего системного прокси',
    'dns_reset': 'сброс DNS, указывающего на неработающую локальную службу',
    'dns_flush': 'очистка кэша DNS',
    'dns_fallback': 'резервный DNS вместо молчащего DNS сети (снимется сам)',
    'reconnect': 'переподключение туннеля с новой привязкой',
  };

  Future<void> _runSoftRepair() async {
    if (_isFixing) return;
    _lockClose();
    setState(() => _isFixing = true);

    if (widget.softRepairRunner != null) {
      await widget.softRepairRunner!(this);
      if (mounted) setState(() => _isFixing = false);
      return;
    }

    _appendLog('FIX', '=== запуск авто-исправления ===');
    final state = context.read<AppState>();
    var ok = false;
    try {
      final res = await state.repairNetwork();
      if (!mounted) return;
      if (res['simulated'] == true) {
        _appendLog('INFO', 'режим без движка: исправление симулировано');
      }
      final actions = (res['actions'] as List?) ?? const [];
      for (final raw in actions) {
        final a = (raw as Map).cast<String, dynamic>();
        final label = _actionLabels[a['name']] ?? a['name'];
        _appendLog(
          a['done'] == true ? 'FIX' : 'INFO',
          '$label: ${a['done'] == true ? "выполнено" : "не требуется"}',
        );
      }
      state.clearError();
      ok = true;
    } catch (e) {
      if (mounted) _appendLog('ERR', 'исправление не выполнено: $e');
    }

    if (!mounted) return;
    setState(() => _isFixing = false);
    if (ok) {
      _appendLog('OK', '=== авто-исправление завершено ===');
      NekkoToast.show(
        context,
        'сетевые настройки и кэш DNS сброшены',
        icon: Icons.check_circle_outline_rounded,
      );
      await _runDiagnostics();
    }
  }

  Future<void> _runDeepReset() async {
    final confirmed = await NekkoDialog.confirm(
      context,
      title: 'глубокий сброс сетевого стека',
      message: _isMac
          ? 'Таблица маршрутов будет очищена, а активные сетевые подключения выключатся и включатся заново: сеть пропадёт на несколько секунд, настройки адаптеров сохранятся. '
                'Подменённые записи в файле hosts будут отключены (с резервной копией). VPN отключится, другие VPN могут потерять соединение.\n\n'
                'Перезагрузка не нужна. Продолжить?'
          : 'Будет выполнен полный сброс сетевого стека Winsock и TCP/IP, а подменённые записи в файле hosts будут отключены (с резервной копией). '
                'Сторонние сетевые фильтры (например, winws и драйвер WinDivert) будут остановлены и переведены в ручной запуск: сами они не удаляются, но после перезагрузки не запустятся. VPN отключится.\n\n'
                'Для применения изменений потребуется перезагрузка компьютера. Продолжить?',
      confirmLabel: 'сбросить стек',
    );
    if (!confirmed || !mounted) return;
    _lockClose();

    final state = context.read<AppState>();
    _appendLog('FIX', '=== глубокий сброс сетевого стека ===');
    try {
      var res = await state.resetNetworkStack();
      if (!mounted) return;
      if (res['refused'] == true) {
        final names = ((res['staticAdapters'] as List?) ?? const []).join(', ');
        _appendLog(
          'WARN',
          'на адаптерах вручную задан IP-адрес: сброс приостановлен',
        );
        final force = await NekkoDialog.confirm(
          context,
          title: 'сброс удалит ручные настройки IP',
          message:
              'У адаптеров $names вручную задан IP-адрес. Сброс удалит эти настройки, '
              'и после перезагрузки их придётся ввести заново, иначе сеть не заработает.\n\n'
              'Сбросить всё равно?',
          confirmLabel: 'сбросить и удалить',
        );
        if (!force || !mounted) {
          _appendLog('INFO', 'глубокий сброс отменён');
          return;
        }
        res = await state.resetNetworkStack(force: true);
        if (!mounted) return;
      }
      if (res['simulated'] == true) {
        _appendLog('INFO', 'режим без движка: сброс симулирован');
      }
      final hostsDisabled = (res['hostsDisabled'] as int?) ?? 0;
      if (hostsDisabled > 0) {
        _appendLog(
          'FIX',
          'файл hosts: отключено записей: $hostsDisabled (создана резервная копия)',
        );
      }
      final dpiStopped =
          (res['dpiStopped'] as List?)?.cast<String>() ?? const [];
      if (dpiStopped.isNotEmpty) {
        _appendLog(
          'FIX',
          'сторонние сетевые фильтры остановлены, запуск ручной: ${dpiStopped.join(", ")} (прежние настройки сохранены)',
        );
      }
      final dpiFailed = (res['dpiFailed'] as List?)?.cast<String>() ?? const [];
      if (dpiFailed.isNotEmpty) {
        _appendLog(
          'WARN',
          'не удалось полностью остановить: ${dpiFailed.join(", ")}',
        );
      }
      if (res['rebootRequired'] == true) {
        _appendLog('WARN', 'сброс выполнен: перезагрузите компьютер');
        NekkoToast.show(
          context,
          'стек сброшен: перезагрузите компьютер',
          icon: Icons.restart_alt_rounded,
        );
      } else if (res['networkPending'] == true) {
        _appendLog(
          'WARN',
          'сброс выполнен, но сеть ещё не поднялась: подождите минуту и нажмите «проверить»',
        );
        NekkoToast.show(
          context,
          'сеть ещё поднимается',
          icon: Icons.hourglass_top_rounded,
        );
      } else {
        _appendLog(
          'OK',
          '=== глубокий сброс завершён, перезагрузка не нужна ===',
        );
        NekkoToast.show(
          context,
          'сеть пересобрана',
          icon: Icons.check_circle_outline_rounded,
        );
        await _runDiagnostics();
      }
    } catch (e) {
      if (mounted) _appendLog('ERR', 'глубокий сброс не выполнен: $e');
    }
  }

  Future<void> _copyLogs() async {
    final text = _logs
        .map((l) => '[${l.formattedTime}] [${l.level}] ${l.message}')
        .join('\n');
    await Clipboard.setData(ClipboardData(text: text));
    if (!mounted) return;
    NekkoToast.show(
      context,
      'журнал скопирован в буфер',
      icon: Icons.copy_rounded,
    );
  }

  void _clearLogs() {
    setState(() {
      _logs.clear();
      _appendLog('INFO', 'журнал очищен');
    });
    NekkoToast.show(
      context,
      'журнал очищен',
      icon: Icons.delete_outline_rounded,
      hold: NekkoToast.glanceHold,
    );
  }

  void _simulateTotalBreakdown() {
    setState(() {
      _proxyHealth = ComponentHealth.error;
      _dnsHealth = ComponentHealth.error;
      _adapterHealth = ComponentHealth.error;
      _coreHealth = ComponentHealth.error;
    });
    final state = context.read<AppState>();
    state.simulateEngineFailure(
      'FATAL[0000] start engine: configure tun interface: operation not permitted\n'
      '2026-11-01T02:50:12Z [singbox/inbound] tun: open utun4: permission denied\n'
      '[engine.stderr] sing-box core died unexpectedly with exit code 1',
    );
    _appendLog('WARN', '=== симуляция полной поломки активирована ===');
    _appendLog(
      'ERR',
      '[proxy] обнаружен зависший системный прокси: 127.0.0.1:7890 (wininet)',
    );
    _appendLog('ERR', '[dns] сбой прямого DNS запроса: connection timed out');
    _appendLog(
      'ERR',
      '[adapters] конфликт виртуального интерфейса TAP-Windows Adapter V9',
    );
    _appendLog(
      'ERR',
      '[core] аварийный сбой sing-box: permission denied on utun / wintun',
    );
    NekkoToast.show(
      context,
      'симуляция сбоя всех компонентов активирована',
      icon: Icons.warning_amber_rounded,
    );
  }

  Future<void> _sendSupportReport() async {
    if (_sendingSupport) return;
    setState(() => _sendingSupport = true);
    _appendLog('INFO', 'отправка отчёта диагностики в поддержку...');
    try {
      final state = context.read<AppState>();
      final logSummary = _logs
          .map((l) => '[${l.formattedTime}] [${l.level}] ${l.message}')
          .join('\n');
      await state.submitSupportReport('Отчёт авто-фикса:\n$logSummary');
      _appendLog('OK', 'отчёт успешно передан в поддержку');
      if (!mounted) return;
      NekkoToast.show(
        context,
        'отчёт передан поддержке',
        icon: Icons.check_rounded,
        hold: NekkoToast.glanceHold,
      );
    } catch (e) {
      _appendLog('WARN', 'не удалось передать отчёт: $e');
      if (!mounted) return;
      NekkoToast.show(
        context,
        'не удалось сохранить отчёт',
        icon: Icons.error_outline_rounded,
        hold: NekkoToast.glanceHold,
      );
    } finally {
      if (mounted) setState(() => _sendingSupport = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;

    return PopScope(
      canPop: !_closeLocked,
      child: Dialog(
        backgroundColor: t.surface,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(t.radiusLg),
        ),
        insetPadding: const EdgeInsets.symmetric(horizontal: 20, vertical: 20),
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 520, maxHeight: 680),
          child: Padding(
            padding: const EdgeInsets.fromLTRB(20, 18, 20, 16),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                // Header — clean, quiet, no pink square
                Row(
                  children: [
                    Icon(
                      Icons.auto_fix_high_rounded,
                      size: 19,
                      color: t.primary,
                    ),
                    const SizedBox(width: 9),
                    Text(
                      'диагностика',
                      style: TextStyle(
                        color: t.text,
                        fontSize: 15,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 14),

                // Status cards grid (neutral surface, clean icons, right-side checkmark/cross)
                Row(
                  children: [
                    Expanded(
                      child: _StatusCard(
                        icon: Icons.alt_route_rounded,
                        label: 'системный прокси',
                        health: _proxyHealth,
                      ),
                    ),
                    const SizedBox(width: 8),
                    Expanded(
                      child: _StatusCard(
                        icon: Icons.dns_outlined,
                        label: 'резолвер',
                        health: _dnsHealth,
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 8),
                Row(
                  children: [
                    Expanded(
                      child: _StatusCard(
                        icon: Icons.router_outlined,
                        label: 'сетевые адаптеры',
                        health: _adapterHealth,
                      ),
                    ),
                    const SizedBox(width: 8),
                    Expanded(
                      child: _StatusCard(
                        icon: Icons.shield_outlined,
                        label: 'состояние ядра',
                        health: _coreHealth,
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 12),

                // Terminal Console Container (no divider line, clean top bar)
                Expanded(
                  child: Container(
                    decoration: BoxDecoration(
                      color: t.bg,
                      borderRadius: BorderRadius.circular(t.radius),
                      border: Border.all(color: t.border),
                    ),
                    clipBehavior: Clip.antiAlias,
                    child: Column(
                      children: [
                        // Clean top bar inside console: subtle test button on left, copy icon with tooltip on right
                        Padding(
                          padding: const EdgeInsets.fromLTRB(10, 6, 8, 2),
                          child: Row(
                            children: [
                              // A fake failure means nothing to anyone but a
                              // tester (seven taps on the title).
                              if (context.watch<AppState>().presets?.tester ??
                                  false)
                                _HoverActionText(
                                  icon: Icons.bug_report_outlined,
                                  label: 'симулировать поломку',
                                  color: t.textFaint,
                                  onTap: _simulateTotalBreakdown,
                                ),
                              const Spacer(),
                              _CopyIconButton(onTap: _copyLogs),
                            ],
                          ),
                        ),
                        // Log lines list directly below (NO divider line)
                        Expanded(
                          child: ListView.builder(
                            controller: _logScroll,
                            padding: const EdgeInsets.fromLTRB(10, 2, 10, 6),
                            itemCount: _logs.length,
                            itemBuilder: (context, index) {
                              final item = _logs[index];
                              final tagColor = switch (item.level) {
                                'OK' => t.ok.withValues(alpha: 0.9),
                                'FIX' => t.primary,
                                'WARN' => t.warn,
                                'ERR' => t.bad,
                                _ => t.textFaint,
                              };
                              return Padding(
                                padding: const EdgeInsets.symmetric(
                                  vertical: 1.5,
                                ),
                                child: Text.rich(
                                  TextSpan(
                                    children: [
                                      TextSpan(
                                        text: '[${item.formattedTime}] ',
                                        style: TextStyle(
                                          color: t.textFaint,
                                          fontSize: 11,
                                          fontFamily: t.monoFamily,
                                        ),
                                      ),
                                      TextSpan(
                                        text: '[${item.level}] ',
                                        style: TextStyle(
                                          color: tagColor,
                                          fontSize: 11,
                                          fontWeight: FontWeight.w600,
                                          fontFamily: t.monoFamily,
                                        ),
                                      ),
                                      TextSpan(
                                        text: item.message,
                                        style: TextStyle(
                                          color: t.textSecondary,
                                          fontSize: 11,
                                          fontFamily: t.monoFamily,
                                        ),
                                      ),
                                    ],
                                  ),
                                ),
                              );
                            },
                          ),
                        ),
                      ],
                    ),
                  ),
                ),

                // Action buttons bar under the log
                const SizedBox(height: 12),
                Row(
                  children: [
                    Expanded(
                      child: _DoctorActionButton(
                        primary: true,
                        icon: Icons.healing_rounded,
                        label: 'починить сеть',
                        busy: _isFixing,
                        onPressed: _runSoftRepair,
                      ),
                    ),
                    const SizedBox(width: 8),
                    Expanded(
                      child: _DoctorActionButton(
                        icon: Icons.refresh_rounded,
                        label: 'проверить',
                        busy: _isDiagnosing,
                        onPressed: _runDiagnostics,
                      ),
                    ),
                    const SizedBox(width: 8),
                    Expanded(
                      child: _DoctorActionButton(
                        destructive: true,
                        icon: Icons.layers_clear_rounded,
                        label: 'глубокий сброс',
                        onPressed: _runDeepReset,
                      ),
                    ),
                  ],
                ),

                // Footer below buttons
                const SizedBox(height: 8),
                Row(
                  mainAxisAlignment: MainAxisAlignment.spaceBetween,
                  children: [
                    _HoverActionText(
                      icon: Icons.send_rounded,
                      label: _sendingSupport
                          ? 'отправка...'
                          : 'отправить в поддержку',
                      busy: _sendingSupport,
                      onTap: _sendSupportReport,
                    ),
                    _HoverActionText(
                      icon: Icons.delete_outline_rounded,
                      label: 'очистить лог',
                      onTap: _clearLogs,
                    ),
                  ],
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _StatusCard extends StatelessWidget {
  final IconData icon;
  final String label;
  final ComponentHealth health;

  const _StatusCard({
    required this.icon,
    required this.label,
    required this.health,
  });

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final (statusIcon, statusColor) = switch (health) {
      ComponentHealth.ok => (Icons.check_circle_rounded, t.ok),
      ComponentHealth.warning ||
      ComponentHealth.error => (Icons.cancel_rounded, t.bad),
      ComponentHealth.checking => (Icons.hourglass_top_rounded, t.textMuted),
    };

    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 11),
      decoration: BoxDecoration(
        color: t.surface2,
        borderRadius: BorderRadius.circular(t.radius),
        border: Border.all(color: t.border),
      ),
      child: Row(
        children: [
          Icon(icon, size: 16, color: t.textMuted),
          const SizedBox(width: 10),
          Expanded(
            child: Text(
              label,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(
                color: t.text,
                fontSize: 12,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
          const SizedBox(width: 8),
          Icon(statusIcon, size: 16, color: statusColor),
        ],
      ),
    );
  }
}

class _DoctorActionButton extends StatefulWidget {
  final IconData icon;
  final String label;
  final bool primary;
  final bool destructive;
  final bool busy;
  final VoidCallback? onPressed;

  const _DoctorActionButton({
    required this.icon,
    required this.label,
    this.primary = false,
    this.destructive = false,
    this.busy = false,
    required this.onPressed,
  });

  @override
  State<_DoctorActionButton> createState() => _DoctorActionButtonState();
}

class _DoctorActionButtonState extends State<_DoctorActionButton>
    with SingleTickerProviderStateMixin {
  late final AnimationController _anim;
  late final Animation<double> _curve;

  @override
  void initState() {
    super.initState();
    _anim = AnimationController(vsync: this, duration: AppDurations.hover);
    _curve = CurvedAnimation(parent: _anim, curve: AppCurves.hover);
  }

  @override
  void didUpdateWidget(covariant _DoctorActionButton oldWidget) {
    super.didUpdateWidget(oldWidget);
    if ((widget.onPressed == null || widget.busy) && _anim.value > 0) {
      _anim.reverse();
    }
  }

  @override
  void dispose() {
    _anim.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final enabled = widget.onPressed != null && !widget.busy;

    final Color normalBg;
    final Color hoverBg;
    final Color normalBorder;
    final Color hoverBorder;
    final Color normalIcon;
    final Color hoverIcon;
    final Color normalText;
    final Color hoverText;

    if (!enabled) {
      normalBg = t.surface2.withValues(alpha: 0.4);
      hoverBg = normalBg;
      normalBorder = t.border;
      hoverBorder = t.border;
      normalIcon = t.textFaint;
      hoverIcon = t.textFaint;
      normalText = t.textFaint;
      hoverText = t.textFaint;
    } else if (widget.primary) {
      normalBg = Color.alphaBlend(
        t.primary.withValues(alpha: 0.12),
        t.surface2,
      );
      hoverBg = Color.alphaBlend(t.primary.withValues(alpha: 0.24), t.surface2);
      normalBorder = t.primary.withValues(alpha: 0.45);
      hoverBorder = t.primary;
      normalIcon = t.primary;
      hoverIcon = t.primary;
      normalText = t.text;
      hoverText = Colors.white;
    } else if (widget.destructive) {
      normalBg = t.surface2;
      hoverBg = Color.alphaBlend(t.bad.withValues(alpha: 0.14), t.surface2);
      normalBorder = t.border;
      hoverBorder = t.bad.withValues(alpha: 0.65);
      normalIcon = t.textMuted;
      hoverIcon = t.bad;
      normalText = t.textSecondary;
      hoverText = t.bad;
    } else {
      normalBg = t.surface2;
      hoverBg = Color.alphaBlend(
        Colors.white.withValues(alpha: 0.08),
        t.surface2,
      );
      normalBorder = t.borderStrong;
      hoverBorder = Color.alphaBlend(
        Colors.white.withValues(alpha: 0.25),
        t.borderStrong,
      );
      normalIcon = t.textMuted;
      hoverIcon = t.text;
      normalText = t.textSecondary;
      hoverText = t.text;
    }

    return MouseRegion(
      cursor: enabled ? SystemMouseCursors.click : SystemMouseCursors.basic,
      onEnter: (_) {
        if (enabled) _anim.forward();
      },
      onExit: (_) {
        _anim.reverse();
      },
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: enabled ? widget.onPressed : null,
        child: AnimatedBuilder(
          animation: _curve,
          builder: (context, _) {
            final progress = _curve.value;
            final bg = Color.lerp(normalBg, hoverBg, progress)!;
            final borderColor = Color.lerp(
              normalBorder,
              hoverBorder,
              progress,
            )!;
            final iconColor = Color.lerp(normalIcon, hoverIcon, progress)!;
            final textColor = Color.lerp(normalText, hoverText, progress)!;

            return Container(
              height: 35,
              padding: const EdgeInsets.symmetric(horizontal: 10),
              decoration: BoxDecoration(
                color: bg,
                borderRadius: BorderRadius.circular(t.radius),
                border: Border.all(color: borderColor),
              ),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  if (widget.busy)
                    SizedBox(
                      width: 13,
                      height: 13,
                      child: CircularProgressIndicator(
                        strokeWidth: 1.5,
                        color: iconColor,
                      ),
                    )
                  else
                    Icon(widget.icon, size: 15, color: iconColor),
                  const SizedBox(width: 7),
                  Flexible(
                    child: Text(
                      widget.label,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        color: textColor,
                        fontSize: 12,
                        fontWeight: widget.primary
                            ? FontWeight.w600
                            : FontWeight.w500,
                      ),
                    ),
                  ),
                ],
              ),
            );
          },
        ),
      ),
    );
  }
}

class _CopyIconButton extends StatefulWidget {
  final VoidCallback onTap;

  const _CopyIconButton({required this.onTap});

  @override
  State<_CopyIconButton> createState() => _CopyIconButtonState();
}

class _CopyIconButtonState extends State<_CopyIconButton>
    with SingleTickerProviderStateMixin {
  late final AnimationController _anim;
  late final Animation<double> _curve;

  @override
  void initState() {
    super.initState();
    _anim = AnimationController(vsync: this, duration: AppDurations.hover);
    _curve = CurvedAnimation(parent: _anim, curve: AppCurves.hover);
  }

  @override
  void dispose() {
    _anim.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;

    return Tooltip(
      message: 'скопировать лог',
      waitDuration: const Duration(milliseconds: 300),
      child: MouseRegion(
        cursor: SystemMouseCursors.click,
        onEnter: (_) => _anim.forward(),
        onExit: (_) => _anim.reverse(),
        child: GestureDetector(
          behavior: HitTestBehavior.opaque,
          onTap: widget.onTap,
          child: AnimatedBuilder(
            animation: _curve,
            builder: (context, _) {
              final progress = _curve.value;
              final bg = Color.lerp(Colors.transparent, t.surface2, progress)!;
              final iconColor = Color.lerp(t.textMuted, t.text, progress)!;

              return Container(
                padding: const EdgeInsets.all(6),
                decoration: BoxDecoration(
                  color: bg,
                  borderRadius: BorderRadius.circular(6),
                ),
                child: Icon(Icons.copy_rounded, size: 18, color: iconColor),
              );
            },
          ),
        ),
      ),
    );
  }
}

class _HoverActionText extends StatefulWidget {
  final IconData icon;
  final String label;
  final Color? color;
  final bool busy;
  final VoidCallback onTap;

  const _HoverActionText({
    required this.icon,
    required this.label,
    this.color,
    this.busy = false,
    required this.onTap,
  });

  @override
  State<_HoverActionText> createState() => _HoverActionTextState();
}

class _HoverActionTextState extends State<_HoverActionText>
    with SingleTickerProviderStateMixin {
  late final AnimationController _anim;
  late final Animation<double> _curve;

  @override
  void initState() {
    super.initState();
    _anim = AnimationController(vsync: this, duration: AppDurations.hover);
    _curve = CurvedAnimation(parent: _anim, curve: AppCurves.hover);
  }

  @override
  void dispose() {
    _anim.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final baseColor = widget.color ?? t.textMuted;
    final activeColor = widget.color != null
        ? Color.alphaBlend(Colors.white.withValues(alpha: 0.35), widget.color!)
        : t.text;

    return MouseRegion(
      cursor: widget.busy ? SystemMouseCursors.basic : SystemMouseCursors.click,
      onEnter: (_) {
        if (!widget.busy) _anim.forward();
      },
      onExit: (_) {
        _anim.reverse();
      },
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: widget.busy ? null : widget.onTap,
        child: AnimatedBuilder(
          animation: _curve,
          builder: (context, _) {
            final progress = _curve.value;
            final targetColor = Color.lerp(baseColor, activeColor, progress)!;
            final bg = Color.lerp(
              Colors.transparent,
              Color.alphaBlend(
                Colors.white.withValues(alpha: 0.05),
                t.surface2,
              ),
              progress,
            )!;

            return Container(
              padding: const EdgeInsets.symmetric(horizontal: 7, vertical: 4),
              decoration: BoxDecoration(
                color: bg,
                borderRadius: BorderRadius.circular(t.radius),
              ),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  if (widget.busy)
                    SizedBox(
                      width: 12,
                      height: 12,
                      child: CircularProgressIndicator(
                        strokeWidth: 1.5,
                        color: targetColor,
                      ),
                    )
                  else
                    Icon(widget.icon, size: 13, color: targetColor),
                  const SizedBox(width: 5),
                  Text(
                    widget.label,
                    style: TextStyle(
                      color: targetColor,
                      fontSize: 11,
                      fontWeight: FontWeight.w500,
                    ),
                  ),
                ],
              ),
            );
          },
        ),
      ),
    );
  }
}
