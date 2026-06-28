import '../core/models.dart';

/// Pure helpers over a profile's ping history. Ported 1:1 from the Svelte
/// `uptimePct` / `avgRtt` / `lastRttText` helpers — same defaults (returns
/// -1 sentinel when there's no data) so the UI rendering rules transfer.

class UptimeStats {
  static const int sentinelMissing = -1;

  /// Most recent sample, or null if history is empty.
  static PingSample? last(List<PingSample> hist) =>
      hist.isEmpty ? null : hist.last;

  /// Percent of `ok` samples in the window. Returns -1 if history is empty.
  static int uptimePct(List<PingSample> hist) {
    if (hist.isEmpty) return sentinelMissing;
    final ok = hist.where((s) => s.ok).length;
    return ((ok / hist.length) * 100).round();
  }

  /// Average RTT over `ok` samples only. -1 if no successful probes.
  static int avgRtt(List<PingSample> hist) {
    if (hist.isEmpty) return sentinelMissing;
    final ok = hist.where((s) => s.ok).toList();
    if (ok.isEmpty) return sentinelMissing;
    final sum = ok.fold<int>(0, (a, s) => a + s.rttMs);
    return (sum / ok.length).round();
  }
}
