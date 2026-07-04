import 'dart:async';
import 'dart:ffi' as ffi;
import 'dart:io' show Platform;
import 'dart:isolate';

import 'package:ffi/ffi.dart';

import 'nekkobox_core.dart';

// C signatures (matching the //export declarations in cmd/nekkobox-ffi):
//   char* NekkoboxCall(char* method, char* args_json);
//   void  NekkoboxFree(char* result);
//   void  NekkoboxShutdown();

typedef _CallC = ffi.Pointer<Utf8> Function(
    ffi.Pointer<Utf8>, ffi.Pointer<Utf8>);
typedef _CallDart = ffi.Pointer<Utf8> Function(
    ffi.Pointer<Utf8>, ffi.Pointer<Utf8>);
typedef _FreeC = ffi.Void Function(ffi.Pointer<Utf8>);
typedef _FreeDart = void Function(ffi.Pointer<Utf8>);

/// FFI-backed [NekkoboxCore] for desktop platforms. Loads the shared library
/// shipped next to the executable and forwards every call through
/// NekkoboxCall on the Go side.
///
/// Threading model: every engine call except the bootstrap [init]/[shutdown]
/// goes through a single long-lived **worker isolate** (see [rawCallAsync]).
/// The worker opens the DLL once and processes a serialized queue of calls,
/// so a slow engine call (RestartVPN, a cold ProfileCountries DNS lookup, …)
/// blocks only the worker — never the UI isolate. The synchronous [rawCall]
/// on the main isolate is used purely for one-shot boot (Init) where blocking
/// the splash is acceptable and the worker may not exist yet.
class NekkoboxCoreFfi extends NekkoboxCore {
  final ffi.DynamicLibrary _lib;
  late final _CallDart _call;
  late final _FreeDart _free;

  // ----- worker isolate plumbing -----
  final ReceivePort _fromWorker = ReceivePort();
  final Completer<void> _workerReady = Completer<void>();
  final Map<int, Completer<String>> _pending = {};
  SendPort? _toWorker;
  Future<void>? _spawning;
  int _nextId = 0;

  NekkoboxCoreFfi._(this._lib) {
    _call = _lib.lookupFunction<_CallC, _CallDart>('NekkoboxCall');
    _free = _lib.lookupFunction<_FreeC, _FreeDart>('NekkoboxFree');
    _fromWorker.listen(_onWorkerMessage);
  }

  /// Opens the platform-appropriate shared library. The file is expected to
  /// live alongside the executable (same folder dart:io exposes via
  /// `Platform.resolvedExecutable`).
  factory NekkoboxCoreFfi.open() {
    final name = _libName();
    // dart:ffi searches the OS loader path; on Windows that includes the
    // folder of the calling exe, which is where we drop nekkobox.dll.
    final lib = ffi.DynamicLibrary.open(name);
    return NekkoboxCoreFfi._(lib);
  }

  static String _libName() {
    if (Platform.isWindows) return 'nekkobox.dll';
    if (Platform.isMacOS) return 'libnekkobox.dylib';
    return 'libnekkobox.so';
  }

  /// Synchronous call on the *current* (main) isolate. Reserved for bootstrap
  /// (Init/Shutdown) — everything else must use [rawCallAsync] so the engine
  /// never runs on the UI thread.
  @override
  String rawCall(String method, [String argsJson = '']) {
    final cMethod = method.toNativeUtf8();
    final cArgs = argsJson.toNativeUtf8();
    try {
      final resultPtr = _call(cMethod, cArgs);
      try {
        return resultPtr.toDartString();
      } finally {
        _free(resultPtr);
      }
    } finally {
      calloc.free(cMethod);
      calloc.free(cArgs);
    }
  }

  /// Enqueues a call on the worker isolate and completes when it answers.
  /// Calls are serialized worker-side (one at a time), which matches the
  /// engine's own mutex — a long call just delays the ones queued behind it,
  /// and the UI isolate stays free the whole time.
  @override
  Future<String> rawCallAsync(String method, [String argsJson = '']) async {
    await _ensureWorker();
    final id = _nextId++;
    final completer = Completer<String>();
    _pending[id] = completer;
    _toWorker!.send([id, method, argsJson]);
    return completer.future;
  }

  Future<void> _ensureWorker() {
    return _spawning ??= _spawnWorker();
  }

  Future<void> _spawnWorker() async {
    await Isolate.spawn(
      _workerMain,
      [_fromWorker.sendPort, _libName()],
      debugName: 'nekkobox-engine',
    );
    // The worker's first message is its own SendPort; [_onWorkerMessage]
    // completes [_workerReady] once it arrives.
    await _workerReady.future;
  }

  void _onWorkerMessage(dynamic msg) {
    if (msg is SendPort) {
      _toWorker = msg;
      if (!_workerReady.isCompleted) _workerReady.complete();
      return;
    }
    // Response frame: [id, ok(bool), payload(String)].
    final frame = msg as List;
    final id = frame[0] as int;
    final completer = _pending.remove(id);
    if (completer == null) return;
    if (frame[1] == true) {
      completer.complete(frame[2] as String);
    } else {
      completer.completeError(StateError(frame[2] as String));
    }
  }

  /// Worker entrypoint. Opens the DLL once, then serves calls off a port.
  /// Any exception is reported back as a failed frame rather than crashing
  /// the isolate, so one bad call can't take the engine bridge down.
  static void _workerMain(List<Object> boot) {
    final SendPort toMain = boot[0] as SendPort;
    final String libName = boot[1] as String;

    final lib = ffi.DynamicLibrary.open(libName);
    final call = lib.lookupFunction<_CallC, _CallDart>('NekkoboxCall');
    final free = lib.lookupFunction<_FreeC, _FreeDart>('NekkoboxFree');

    final port = ReceivePort();
    toMain.send(port.sendPort);

    port.listen((dynamic msg) {
      final frame = msg as List;
      final id = frame[0] as int;
      final method = frame[1] as String;
      final argsJson = frame[2] as String;
      try {
        final result = _isolateCall(call, free, method, argsJson);
        toMain.send([id, true, result]);
      } catch (e) {
        toMain.send([id, false, e.toString()]);
      }
    });
  }

  static String _isolateCall(
    _CallDart call,
    _FreeDart free,
    String method,
    String argsJson,
  ) {
    final cMethod = method.toNativeUtf8();
    final cArgs = argsJson.toNativeUtf8();
    try {
      final resultPtr = call(cMethod, cArgs);
      try {
        return resultPtr.toDartString();
      } finally {
        free(resultPtr);
      }
    } finally {
      calloc.free(cMethod);
      calloc.free(cArgs);
    }
  }
}
