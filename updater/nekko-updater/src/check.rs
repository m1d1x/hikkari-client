//! The poll: ask the channel what it serves, and decide — in that order, and
//! with the cheap question first.
//!
//! ```text
//!   latest.json  (100 B, unsigned, one decision: is the manifest worth reading)
//!        │ moved
//!        ▼
//!   manifest.json (327 KB, signed)  ──▶ verify ──▶ decide(installed)
//!        ▲ If-None-Match
//!        └── the copy already on disk, when the channel says 304
//! ```
//!
//! This module is the whole of what a caller needs to know about the channel:
//! [`poll`] composes the probe ([`crate::channel`]), the signature check
//! ([`crate::signature`]) and the decision ([`crate::manifest::decide`]) into
//! one call whose outcome says what to do. Preparing the update afterwards is
//! [`crate::stage::prepare`].
//!
//! Two properties worth stating because they are easy to lose later:
//!
//! * **The manifest is fetched only when the probe moved.** At 327 KB per
//!   manifest and a poll every few hours, doing otherwise costs every
//!   installation ~2.6 MB a day to be told there is no news.
//! * **Nothing the probe said survives the manifest.** The version acted on is
//!   the signed one; the probe's number is a hint that has already done its
//!   only job by the time the manifest is parsed.

use crate::channel::{self, ProbeVerdict, VersionProbe};
use crate::error::{ManifestError, UpdateError};
use crate::manifest::{decide, Channel, Manifest, Platform, UpdateDecision, Version};
use crate::signature::{self, Expect, TrustedKey};
use crate::transport::{Document, UpdateTransport};

/// Which channel this installation follows, and whose signatures it believes.
pub struct ChannelConfig {
    /// Root the probe, manifest and blob URLs are built from.
    pub base_url: String,
    pub platform: Platform,
    pub channel: Channel,
    /// The public keys a manifest may be signed with — two of them in a
    /// release build (current and next), because a key that cannot be rotated
    /// is a key that cannot be replaced after it leaks (plan 38 §0.4).
    pub trusted: Vec<TrustedKey>,
}

impl ChannelConfig {
    fn expect(&self) -> Expect {
        Expect {
            platform: self.platform,
            channel: self.channel,
        }
    }
}

/// The manifest this installation last read, kept so a poll that finds the
/// same release does not pull it again.
///
/// ⚠️ Cached as the **envelope bytes**, not as a parsed manifest: the whole
/// trust model is "verify the bytes you were given", and a cache of parsed
/// objects would have to be re-serialised to be checked — reintroducing
/// exactly the canonicalisation problem the format avoids. So the cache is
/// re-verified on every use, which costs one ed25519 check.
pub struct CachedManifest {
    pub envelope: Vec<u8>,
    pub etag: Option<String>,
}

/// What the poll found.
#[derive(Debug)]
pub enum Poll {
    /// The channel serves what is installed.
    UpToDate,
    /// The channel serves something older: a release was pulled, or an old
    /// file is being replayed. Reported so it can be logged, never followed.
    ChannelBehind,
    /// The probe under this prefix is for another platform or channel — the
    /// wrong file synced to the wrong place. An operational fault; the
    /// manifest is not fetched, because the address itself is suspect.
    WrongTarget,
    /// A signed manifest, and what it means for this installation.
    Found {
        manifest: Manifest,
        decision: UpdateDecision,
        /// The bytes that verified, to be cached and to be re-checked next
        /// time.
        envelope: Vec<u8>,
        etag: Option<String>,
    },
}

/// Ask the channel, once.
///
/// `installed` is the running version — the single source of version truth on
/// the client (roadmap 38.1). `cached` is what a previous poll found, if any.
pub fn poll(
    transport: &dyn UpdateTransport,
    cfg: &ChannelConfig,
    installed: Version,
    cached: Option<&CachedManifest>,
) -> Result<Poll, UpdateError> {
    let probe_url = channel::probe_url(&cfg.base_url, cfg.platform, cfg.channel);
    // No conditional request on the probe: it is a hundred bytes, and a
    // validator round trip would cost a header to save nothing.
    let probe_bytes = match transport.get(&probe_url, None)? {
        Document::Body { bytes, .. } => bytes,
        Document::NotModified => {
            return Err(ManifestError::Malformed(
                "the channel answered 304 to a request that carried no validator".to_owned(),
            )
            .into())
        }
    };
    let probe = VersionProbe::parse(&probe_bytes)?;

    match channel::assess(&probe, installed, cfg.expect()) {
        ProbeVerdict::UpToDate => return Ok(Poll::UpToDate),
        ProbeVerdict::ChannelBehind => return Ok(Poll::ChannelBehind),
        ProbeVerdict::WrongTarget => return Ok(Poll::WrongTarget),
        ProbeVerdict::FetchManifest => {}
    }

    let manifest_url = channel::manifest_url(&cfg.base_url, cfg.platform, cfg.channel);
    let etag = cached.and_then(|c| c.etag.as_deref());
    let (envelope, etag) = match transport.get(&manifest_url, etag)? {
        Document::Body { bytes, etag } => (bytes, etag),
        Document::NotModified => match cached {
            Some(c) => (c.envelope.clone(), c.etag.clone()),
            // Only reachable if the channel answers 304 to a request without a
            // validator, which is a broken channel rather than a state we
            // should have a branch for.
            None => {
                return Err(ManifestError::Malformed(
                    "the channel answered 304 but nothing was cached".to_owned(),
                )
                .into())
            }
        },
    };

    let manifest = signature::verify(&envelope, &cfg.trusted, cfg.expect())?;
    let decision = decide(installed, &manifest);
    Ok(Poll::Found {
        manifest,
        decision,
        envelope,
        etag,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::channel::{MANIFEST_FILE, PROBE_FILE};
    use crate::manifest::FileEntry;
    use crate::signature::ReleaseSigningKey;
    use crate::transport::fake::FakeChannel;

    const BASE: &str = "https://update.nekkochat.com";

    fn key() -> ReleaseSigningKey {
        // A fixed key, so a test failure is about the logic and not about
        // entropy. No key material ships in this repository; this one is born
        // and dies inside the test binary.
        ReleaseSigningKey::from_bytes(&[7u8; 32])
    }

    fn cfg() -> ChannelConfig {
        let signing = key();
        ChannelConfig {
            base_url: BASE.to_owned(),
            platform: Platform::Macos,
            channel: Channel::Stable,
            trusted: vec![TrustedKey::from_hex(
                "release-1",
                &hex::encode(signing.verifying_key().to_bytes()),
            )
            .unwrap()],
        }
    }

    fn manifest(version: &str, minimum: &str) -> Manifest {
        Manifest {
            version: Version::parse(version).unwrap(),
            channel: Channel::Stable,
            platform: Platform::Macos,
            minimum_version: Version::parse(minimum).unwrap(),
            published_at: 1_757_563_200,
            base_url: BASE.to_owned(),
            files: vec![FileEntry {
                path: "Contents/MacOS/nekko".to_owned(),
                sha256: "a".repeat(64),
                size: 10,
                executable: true,
            }],
            links: Vec::new(),
        }
    }

    fn probe(version: &str) -> Vec<u8> {
        VersionProbe {
            version: Version::parse(version).unwrap(),
            platform: Platform::Macos,
            channel: Channel::Stable,
            published_at: 1_757_563_200,
        }
        .to_bytes()
        .unwrap()
    }

    fn channel_with(probe_body: Vec<u8>, manifest: Option<&Manifest>) -> FakeChannel {
        let mut chan = FakeChannel::default();
        chan.objects
            .insert(format!("{BASE}/stable/macos/{PROBE_FILE}"), probe_body);
        if let Some(m) = manifest {
            chan.objects.insert(
                format!("{BASE}/stable/macos/{MANIFEST_FILE}"),
                signature::sign(m, "release-1", &key()).unwrap(),
            );
        }
        chan
    }

    fn v(s: &str) -> Version {
        Version::parse(s).unwrap()
    }

    #[test]
    fn the_manifest_is_not_fetched_when_the_probe_says_there_is_no_news() {
        // The reason the probe exists: 327 KB not spent to be told nothing.
        let chan = channel_with(probe("0.2.0"), Some(&manifest("0.2.0", "0.1.0")));
        let found = poll(&chan, &cfg(), v("0.2.0"), None).unwrap();
        assert!(matches!(found, Poll::UpToDate), "{found:?}");
        let urls: Vec<String> = chan.gets().into_iter().map(|(u, _)| u).collect();
        assert_eq!(urls, vec![format!("{BASE}/stable/macos/{PROBE_FILE}")]);
    }

    #[test]
    fn a_newer_release_comes_back_verified_and_decided() {
        let chan = channel_with(probe("0.3.0"), Some(&manifest("0.3.0", "0.1.0")));
        match poll(&chan, &cfg(), v("0.2.0"), None).unwrap() {
            Poll::Found {
                manifest, decision, ..
            } => {
                assert_eq!(manifest.version, v("0.3.0"));
                assert_eq!(decision, UpdateDecision::Available);
            }
            other => panic!("expected a manifest, got {other:?}"),
        }
    }

    #[test]
    fn an_installation_below_the_minimum_is_told_it_must_update() {
        let chan = channel_with(probe("0.3.0"), Some(&manifest("0.3.0", "0.3.0")));
        match poll(&chan, &cfg(), v("0.2.0"), None).unwrap() {
            Poll::Found { decision, .. } => assert_eq!(decision, UpdateDecision::Mandatory),
            other => panic!("expected a manifest, got {other:?}"),
        }
    }

    #[test]
    fn a_channel_serving_an_older_release_is_reported_not_followed() {
        let chan = channel_with(probe("0.1.0"), Some(&manifest("0.1.0", "0.1.0")));
        let found = poll(&chan, &cfg(), v("0.2.0"), None).unwrap();
        assert!(matches!(found, Poll::ChannelBehind), "{found:?}");
        assert_eq!(chan.gets().len(), 1, "no manifest fetch for a rollback");
    }

    #[test]
    fn a_probe_for_another_platform_stops_the_poll_at_the_probe() {
        let mut wrong = VersionProbe {
            version: v("0.9.0"),
            platform: Platform::Macos,
            channel: Channel::Stable,
            published_at: 0,
        };
        wrong.platform = Platform::Windows;
        let chan = channel_with(wrong.to_bytes().unwrap(), Some(&manifest("0.9.0", "0.1.0")));
        let found = poll(&chan, &cfg(), v("0.2.0"), None).unwrap();
        assert!(matches!(found, Poll::WrongTarget), "{found:?}");
        assert_eq!(chan.gets().len(), 1);
    }

    #[test]
    fn a_manifest_signed_by_someone_else_is_refused_after_being_fetched() {
        let mut chan = channel_with(probe("0.3.0"), None);
        let other = ReleaseSigningKey::from_bytes(&[9u8; 32]);
        chan.objects.insert(
            format!("{BASE}/stable/macos/{MANIFEST_FILE}"),
            signature::sign(&manifest("0.3.0", "0.1.0"), "release-1", &other).unwrap(),
        );
        let err = poll(&chan, &cfg(), v("0.2.0"), None).unwrap_err();
        assert!(
            matches!(err, UpdateError::Manifest(ManifestError::BadSignature)),
            "got {err}"
        );
    }

    #[test]
    fn a_cached_manifest_is_reused_when_the_channel_says_it_has_not_changed() {
        // The case that actually happens: a preparation was interrupted, the
        // probe still points at the same new release, and polling again must
        // not re-read 327 KB.
        let manifest = manifest("0.3.0", "0.1.0");
        let mut chan = channel_with(probe("0.3.0"), Some(&manifest));
        let url = format!("{BASE}/stable/macos/{MANIFEST_FILE}");
        chan.etags.insert(url.clone(), "\"abc\"".to_owned());
        let envelope = chan.objects.get(&url).unwrap().clone();

        let cached = CachedManifest {
            envelope,
            etag: Some("\"abc\"".to_owned()),
        };
        match poll(&chan, &cfg(), v("0.2.0"), Some(&cached)).unwrap() {
            Poll::Found {
                manifest: m,
                decision,
                ..
            } => {
                assert_eq!(m.version, v("0.3.0"));
                assert_eq!(decision, UpdateDecision::Available);
            }
            other => panic!("expected a manifest, got {other:?}"),
        }
        // And the validator was actually sent, which is what earned the 304.
        assert_eq!(
            chan.gets().last().unwrap().1.as_deref(),
            Some("\"abc\""),
            "the cached ETag must go out with the request"
        );
    }

    #[test]
    fn a_cached_manifest_is_still_verified_every_time_it_is_used() {
        // A cache of *parsed* manifests would make this test impossible to
        // write, which is the reason the cache holds bytes.
        let manifest = manifest("0.3.0", "0.1.0");
        let mut chan = channel_with(probe("0.3.0"), Some(&manifest));
        let url = format!("{BASE}/stable/macos/{MANIFEST_FILE}");
        chan.etags.insert(url.clone(), "\"abc\"".to_owned());

        let mut tampered = chan.objects.get(&url).unwrap().clone();
        // Flip a byte inside the base64 payload.
        let at = tampered.len() / 2;
        tampered[at] ^= 0x01;
        let cached = CachedManifest {
            envelope: tampered,
            etag: Some("\"abc\"".to_owned()),
        };
        assert!(poll(&chan, &cfg(), v("0.2.0"), Some(&cached)).is_err());
    }

    #[test]
    fn a_channel_that_answers_html_is_an_error_and_not_no_news() {
        let chan = channel_with(b"<html>403</html>".to_vec(), None);
        let err = poll(&chan, &cfg(), v("0.2.0"), None).unwrap_err();
        assert!(matches!(err, UpdateError::Manifest(_)), "got {err}");
    }

    #[test]
    fn a_channel_with_no_probe_at_all_is_a_transport_error() {
        let chan = FakeChannel::default();
        let err = poll(&chan, &cfg(), v("0.2.0"), None).unwrap_err();
        match err {
            UpdateError::Transport(e) => assert!(!e.is_retryable()),
            other => panic!("expected a transport error, got {other}"),
        }
    }
}
