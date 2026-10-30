/// Universal result wrapper returned by every Core.call() across the FFI
/// boundary. Mirrors the Envelope struct in core/api/dispatch.go:
///
/// ```jsonc
/// { "ok": true,  "data": <any> }
/// { "ok": false, "error": "human message" }
/// ```
class Envelope {
  final bool ok;
  final Object? data;
  final String? error;

  const Envelope({required this.ok, this.data, this.error});

  factory Envelope.fromJson(Map<String, dynamic> json) => Envelope(
    ok: json['ok'] as bool? ?? false,
    data: json['data'],
    error: json['error'] as String?,
  );

  /// Returns `data` on success, throws [NekkoboxError] on failure.
  /// Use this when you want to treat the envelope like a normal Future.
  Object? unwrap() {
    if (!ok) throw NekkoboxError(error ?? 'unknown error');
    return data;
  }
}

/// Exception raised when an FFI call returns ok=false. The message is the
/// human-readable error from the Go side.
class NekkoboxError implements Exception {
  final String message;
  const NekkoboxError(this.message);

  @override
  String toString() => 'NekkoboxError: $message';
}
