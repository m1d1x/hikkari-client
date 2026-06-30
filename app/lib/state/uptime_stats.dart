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

  /// Average RTT over the last [windowSamples] entries (about ~2 min at the
  /// 5-second tick we use). Stabler than `avgRtt` for "is this profile
  /// healthy right now?" decisions — single bad spikes don't drag the
  /// average across the full history, but a sustained slow trend does.
  /// -1 if no successful probes inside the window.
  static int recentAvgRtt(List<PingSample> hist, {int windowSamples = 24}) {
    if (hist.isEmpty) return sentinelMissing;
    final slice = hist.length > windowSamples
        ? hist.sublist(hist.length - windowSamples)
        : hist;
    final ok = slice.where((s) => s.ok).toList();
    if (ok.isEmpty) return sentinelMissing;
    final sum = ok.fold<int>(0, (a, s) => a + s.rttMs);
    return (sum / ok.length).round();
  }
}
