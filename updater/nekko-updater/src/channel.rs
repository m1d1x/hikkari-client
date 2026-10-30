//! The delivery channel: where a client looks, and what it is allowed to
//! conclude from each thing it finds there (roadmap 38.3).
//!
//! # Shape
//!
//! ```text
//!   <base_url>/
//!       stable/macos/latest.json      ← the cheap probe: one version, ~120 bytes
//!       stable/macos/manifest.json    ← the signed envelope, 327 KB for a real bundle
//!       blobs/<sha256>                ← file bodies, content-addressed
//! ```
//!
//! Static files, not a Go route. An update has to work at the moment the API
//! is down, which is precisely when it is needed most; and targeting or
//! pulling a release is then a file overwrite rather than a deploy.
//!
//! # Why there is a probe at all
//!
//! A measured number, not a guess: the manifest of the live `nekko.app` is
//! **327 KB** (990 files, 24 links, 245 MB of payload). Checking "every few
//! hours" would pull ~2.6 MB a day in manifests alone, on every installation,
//! to report no news. So the poll asks a small file for the current version and
//! fetches the manifest only once that number has moved.
//!
//! # What the probe is allowed to decide
//!
//! Only ONE thing: whether to spend a request on the manifest. It is not
//! signed and it is not trusted.
//!
//! Signing it was considered and rejected — it buys nothing. Everything the
//! updater acts on comes out of [`crate::signature::verify`], so a forged probe
//! can cause exactly two things: a wasted fetch of a manifest that then fails
//! its signature check, or a client that never fetches. The second is update
//! suppression, which anyone able to forge the probe could achieve anyway by
//! serving nothing at all — a signature cannot make a file that was not
//! delivered appear. What a signed probe WOULD add is a second signed artefact
//! in the world with its own replay and staleness questions, which is real cost
//! against no gain.
//!
//! ⚠️ What makes that safe is a deliberate omission: **the probe does not carry
//! `minimum_version`**. A forced-update screen driven by an unauthenticated
//! file is somebody else's lever on our users. Nothing is lost by leaving it
//! out, because a manifest is invalid unless `minimum_version <= version`
//! ([`crate::manifest::Manifest::validate`]): if the probe says the channel is
//! at the version we already run, no mandatory update can be hiding behind it,
//! and any raised minimum arrives with the version bump that carries it.
//!
//! `platform` and `channel` ARE in the probe, and a mismatch stops the poll —
//! but as a misconfiguration detector (the wrong file synced to the wrong
//! prefix), never as a security boundary. The one that counts is inside the
//! signed payload.

use serde::{Deserialize, Serialize};

use crate::error::ManifestError;
use crate::manifest::{Channel, Platform, Version};
use crate::signature::Expect;

/// File name of the cheap probe under `<channel>/<platform>/`.
///
/// `latest.json` and not `version.json`: an installation carries its own
/// `version.json` (what is installed right now, plan 38 §1.1), and two files
/// with one name meaning different things is how a debugging session gets lost.
pub const PROBE_FILE: &str = "latest.json";

/// File name of the signed manifest under `<channel>/<platform>/`.
pub const MANIFEST_FILE: &str = "manifest.json";

/// Directory of content-addressed file bodies, at the channel root.
///
/// Shared across platforms and channels rather than nested under them: the
/// digest IS the identity, so a file common to two builds is one object, and
/// an unchanged file keeps one URL across releases. That is what makes
/// "download only what changed" true at the CDN and not just on disk.
pub const BLOBS_DIR: &str = "blobs";

/// `<base_url>/<channel>/<platform>`, without a trailing slash.
pub fn target_prefix(base_url: &str, platform: Platform, channel: Channel) -> String {
    format!(
        "{}/{}/{}",
        base_url.trim_end_matches('/'),
        channel,
        platform
    )
}

/// Where to ask for the current version.
pub fn probe_url(base_url: &str, platform: Platform, channel: Channel) -> String {
    format!(
        "{}/{}",
        target_prefix(base_url, platform, channel),
        PROBE_FILE
    )
}

/// Where the signed manifest lives.
///
/// Fetch it with `If-None-Match` against the ETag of the last one read: a
/// client that polls after an aborted download would otherwise re-pull 327 KB
/// to learn nothing. See `docs/UPDATE_CHANNEL.md` for the cache headers the
/// bucket must set for that to work.
pub fn manifest_url(base_url: &str, platform: Platform, channel: Channel) -> String {
    format!(
        "{}/{}",
        target_prefix(base_url, platform, channel),
        MANIFEST_FILE
    )
}

/// The cheap probe: what version the channel is serving, and nothing that
/// could be acted on.
///
/// Unknown fields are accepted (no `deny_unknown_fields`) for the same reason
/// the manifest accepts them: the clients reading this file are already
/// installed, and a client that refuses to parse the file announcing an update
/// is the one client that cannot be fixed remotely.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct VersionProbe {
    /// The version the channel currently serves.
    pub version: Version,
    /// Which build this prefix is for. A cross-check against a mis-synced
    /// file, not a trust decision — see the module docs.
    pub platform: Platform,
    pub channel: Channel,
    /// Epoch seconds, for a human comparing this against the manifest's own
    /// `published_at`. Not a freshness check: that needs a policy and a
    /// trustworthy clock, and is still open (plan 38 §4).
    pub published_at: i64,
}

impl VersionProbe {
    /// Parse a probe body. Deliberately strict about *shape* — a truncated or
    /// HTML-error-page response must not read as "no news".
    pub fn parse(bytes: &[u8]) -> Result<Self, ManifestError> {
        serde_json::from_slice(bytes)
            .map_err(|e| ManifestError::Malformed(format!("version probe is not a probe: {e}")))
    }

    /// Serialise, pretty, for the publisher.
    pub fn to_bytes(&self) -> Result<Vec<u8>, ManifestError> {
        serde_json::to_vec_pretty(self)
            .map_err(|e| ManifestError::Malformed(format!("serialising version probe: {e}")))
    }
}

/// What the poll concluded. Every variant is a reason not to act, except the
/// first — which is a reason to spend one request, still not to act.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ProbeVerdict {
    /// The channel names a newer version: fetch the manifest, then
    /// [`crate::signature::verify`] and [`crate::manifest::decide`] as usual.
    /// The probe's number is discarded at that point — the signed one wins,
    /// including when they disagree.
    FetchManifest,
    /// The channel is serving what is installed. The common case, and the
    /// whole reason this file is small.
    UpToDate,
    /// The channel serves an OLDER version than what is installed. Either a
    /// release was pulled or an old file is being replayed; mirrors
    /// [`crate::manifest::UpdateDecision::Stale`], and like it deserves a log
    /// line rather than silence. No manifest fetch: the probe already said
    /// everything, and there is nothing in a manifest for an older version the
    /// updater would act on.
    ChannelBehind,
    /// The probe is for another platform or channel — the wrong file under this
    /// prefix. An operational fault, reported instead of followed.
    WrongTarget,
}

/// Decide whether the manifest is worth fetching.
///
/// `installed` is the version currently running, which is the single source of
/// version truth on the client side (roadmap 38.1).
pub fn assess(probe: &VersionProbe, installed: Version, expect: Expect) -> ProbeVerdict {
    if probe.platform != expect.platform || probe.channel != expect.channel {
        return ProbeVerdict::WrongTarget;
    }
    match installed.cmp(&probe.version) {
        std::cmp::Ordering::Less => ProbeVerdict::FetchManifest,
        std::cmp::Ordering::Equal => ProbeVerdict::UpToDate,
        std::cmp::Ordering::Greater => ProbeVerdict::ChannelBehind,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn probe(version: &str) -> VersionProbe {
        VersionProbe {
            version: Version::parse(version).unwrap(),
            platform: Platform::Macos,
            channel: Channel::Stable,
            published_at: 1_757_563_200,
        }
    }

    fn expect() -> Expect {
        Expect {
            platform: Platform::Macos,
            channel: Channel::Stable,
        }
    }

    fn v(s: &str) -> Version {
        Version::parse(s).unwrap()
    }

    #[test]
    fn urls_follow_channel_then_platform() {
        let base = "https://update.nekkochat.com";
        assert_eq!(
            probe_url(base, Platform::Macos, Channel::Stable),
            "https://update.nekkochat.com/stable/macos/latest.json"
        );
        assert_eq!(
            manifest_url(base, Platform::Windows, Channel::Beta),
            "https://update.nekkochat.com/beta/windows/manifest.json"
        );
    }

    #[test]
    fn a_trailing_slash_on_the_base_does_not_double_up() {
        // The base URL comes from a manifest field and from config; both have
        // been written with and without the slash by humans.
        assert_eq!(
            probe_url("http://127.0.0.1:8000/", Platform::Macos, Channel::Stable),
            "http://127.0.0.1:8000/stable/macos/latest.json"
        );
    }

    #[test]
    fn a_newer_channel_version_is_the_only_reason_to_fetch() {
        assert_eq!(
            assess(&probe("0.2.0"), v("0.1.0"), expect()),
            ProbeVerdict::FetchManifest
        );
        assert_eq!(
            assess(&probe("0.2.0"), v("0.2.0"), expect()),
            ProbeVerdict::UpToDate
        );
    }

    #[test]
    fn an_older_channel_version_is_reported_not_followed() {
        // A pulled release and a replayed old file are indistinguishable here.
        // Both are worth a log line; neither is worth a 327 KB fetch.
        assert_eq!(
            assess(&probe("0.1.0"), v("0.2.0"), expect()),
            ProbeVerdict::ChannelBehind
        );
    }

    #[test]
    fn the_windows_probe_under_the_mac_prefix_stops_the_poll() {
        let mut p = probe("0.9.0");
        p.platform = Platform::Windows;
        assert_eq!(assess(&p, v("0.1.0"), expect()), ProbeVerdict::WrongTarget);

        let mut p = probe("0.9.0");
        p.channel = Channel::Beta;
        assert_eq!(assess(&p, v("0.1.0"), expect()), ProbeVerdict::WrongTarget);
    }

    #[test]
    fn versions_in_the_probe_order_numerically() {
        // Same trap as in the manifest: "0.10.0" < "0.9.0" as strings.
        assert_eq!(
            assess(&probe("0.10.0"), v("0.9.0"), expect()),
            ProbeVerdict::FetchManifest
        );
    }

    #[test]
    fn the_probe_carries_no_minimum_version() {
        // The load-bearing omission, pinned by a test rather than a comment:
        // an unsigned file must not be able to raise a blocking
        // "update required" screen. If someone adds the field, this fails.
        let json = String::from_utf8(probe("0.2.0").to_bytes().unwrap()).unwrap();
        assert!(
            !json.contains("minimum"),
            "the probe must not carry a minimum version: {json}"
        );
    }

    #[test]
    fn a_probe_round_trips() {
        let p = probe("1.2.3");
        assert_eq!(VersionProbe::parse(&p.to_bytes().unwrap()).unwrap(), p);
    }

    #[test]
    fn a_field_added_later_does_not_break_an_old_client() {
        // The whole point of not setting deny_unknown_fields: this client is
        // already installed and cannot be told to be more tolerant.
        let json = br#"{"version":"0.3.0","platform":"macos","channel":"stable",
                        "published_at":1757563200,"rollout_percent":25}"#;
        let p = VersionProbe::parse(json).unwrap();
        assert_eq!(p.version, v("0.3.0"));
    }

    #[test]
    fn an_error_page_is_not_no_news() {
        // A CDN handing back HTML, or a truncated body, must be an error and
        // not read as "the channel is at 0.0.0" or as "up to date".
        assert!(VersionProbe::parse(b"<html>403</html>").is_err());
        assert!(VersionProbe::parse(br#"{"version":"0.3.0"}"#).is_err());
        assert!(VersionProbe::parse(b"").is_err());
    }

    #[test]
    fn a_probe_version_that_is_not_three_numbers_is_refused() {
        assert!(VersionProbe::parse(
            br#"{"version":"latest","platform":"macos","channel":"stable","published_at":0}"#
        )
        .is_err());
    }
}
