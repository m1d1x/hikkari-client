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
class NekkoboxCoreFfi extends NekkoboxCore {
  final ffi.DynamicLibrary _lib;
  late final _CallDart _call;
  late final _FreeDart _free;

  NekkoboxCoreFfi._(this._lib) {
    _call = _lib.lookupFunction<_CallC, _CallDart>('NekkoboxCall');
    _free = _lib.lookupFunction<_FreeC, _FreeDart>('NekkoboxFree');
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

  @override
  Future<String> rawCallAsync(String method, [String argsJson = '']) {
    final libName = _libName();
    return Isolate.run(() => _isolateCall(libName, method, argsJson));
  }

  static String _isolateCall(String libName, String method, String argsJson) {
    final lib = ffi.DynamicLibrary.open(libName);
    final call = lib.lookupFunction<_CallC, _CallDart>('NekkoboxCall');
    final free = lib.lookupFunction<_FreeC, _FreeDart>('NekkoboxFree');
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
