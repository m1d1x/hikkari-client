//! Trust: the manifest is believed because WE signed it, not because it came
//! over TLS from our own CDN (plan 38 §0.4).
//!
//! # Why the signature covers a payload, not a canonicalised object
//!
//! The rule this module is built on is "verify the bytes you were given". The
//! manifest travels base64-encoded inside an envelope:
//!
//! ```json
//! { "key_id": "release-1", "signature": "<64 bytes, base64>", "payload": "<base64>" }
//! ```
//!
//! and the signature covers exactly the bytes that base64 decodes to — which
//! are also exactly the bytes that get parsed. The tempting alternative is to
//! sign a canonical serialisation of the parsed object (sorted keys, no
//! whitespace); it fails the moment the signer's and the verifier's idea of
//! canonical differ by one space, one key order or one number format, and it
//! fails silently in the direction of *rejecting good releases* — or, with a
//! sloppy implementation, accepting altered ones. Here there is nothing to
//! agree about: one blob of bytes, signed and checked as-is. Re-serialising the
//! parsed manifest is never part of verification.
//!
//! # Key rotation
//!
//! The client carries TWO public keys (current and next) and the envelope names
//! which one signed it. Without that, a compromised key could not be replaced:
//! every installation in the field would only ever accept the old one. Rotating
//! means signing with the next key, which installations already trust.
//!
//! ⚠️ No key material lives in this repository. The private key is a CI secret
//! (38.8/38.9) and the public keys are supplied by the caller — see
//! [`TrustedKey`]. `nekko-manifest keygen` makes a keypair when one is needed.

use base64::engine::general_purpose::STANDARD as B64;
use base64::Engine as _;
use ed25519_dalek::{Signature, Signer, SigningKey, VerifyingKey};
use serde::{Deserialize, Serialize};

use crate::error::ManifestError;
use crate::manifest::{Channel, Manifest, Platform};

/// A public key the client is willing to believe, with the id the envelope
/// refers to it by.
///
/// Held as a list by the caller rather than a constant in this crate: the keys
/// are a property of a *release*, and baking them in here would mean this
/// crate's tests could not run without the real ones.
#[derive(Debug, Clone)]
pub struct TrustedKey {
    pub id: String,
    pub key: VerifyingKey,
}

impl TrustedKey {
    /// Build from the hex form `nekko-manifest keygen` prints.
    pub fn from_hex(id: &str, public_hex: &str) -> Result<Self, ManifestError> {
        let raw = hex::decode(public_hex.trim())
            .map_err(|e| ManifestError::Malformed(format!("public key is not hex: {e}")))?;
        let bytes: [u8; 32] = raw
            .try_into()
            .map_err(|_| ManifestError::Malformed("public key is not 32 bytes".to_owned()))?;
        let key = VerifyingKey::from_bytes(&bytes)
            .map_err(|e| ManifestError::Malformed(format!("bad public key: {e}")))?;
        Ok(TrustedKey {
            id: id.to_owned(),
            key,
        })
    }
}

/// What the client expects this manifest to be about. Stated by the caller so
/// that a mismatch is a refusal instead of an assumption.
#[derive(Debug, Clone, Copy)]
pub struct Expect {
    pub platform: Platform,
    pub channel: Channel,
}

#[derive(Serialize, Deserialize)]
struct Envelope {
    key_id: String,
    signature: String,
    payload: String,
}

/// Sign a manifest, producing the bytes to publish.
///
/// Used by the generator binary. It is in the library, not the binary, so that
/// the tests exercise the same code path a release goes through.
pub fn sign(
    manifest: &Manifest,
    key_id: &str,
    signing: &SigningKey,
) -> Result<Vec<u8>, ManifestError> {
    manifest.validate()?;

    // Pretty-printed on purpose: this file gets read by a human during a
    // botched release. Whitespace is free here precisely because the signature
    // covers these bytes rather than a normalised form of them.
    let payload = serde_json::to_vec_pretty(manifest)
        .map_err(|e| ManifestError::Malformed(format!("serialising manifest: {e}")))?;
    let signature = signing.sign(&payload);

    let envelope = Envelope {
        key_id: key_id.to_owned(),
        signature: B64.encode(signature.to_bytes()),
        payload: B64.encode(&payload),
    };
    serde_json::to_vec_pretty(&envelope)
        .map_err(|e| ManifestError::Malformed(format!("serialising envelope: {e}")))
}

/// Check an envelope and hand back the manifest inside it.
///
/// Order matters and is deliberate: signature first, then the manifest's own
/// invariants, then the target check. Nothing about the payload is trusted —
/// not its length, not its shape — until the signature says it is ours.
pub fn verify(
    envelope_bytes: &[u8],
    trusted: &[TrustedKey],
    expect: Expect,
) -> Result<Manifest, ManifestError> {
    let envelope: Envelope = serde_json::from_slice(envelope_bytes)
        .map_err(|e| ManifestError::Malformed(format!("envelope is not JSON: {e}")))?;

    let key = trusted
        .iter()
        .find(|k| k.id == envelope.key_id)
        .ok_or_else(|| ManifestError::UnknownKey(envelope.key_id.clone()))?;

    let payload = B64
        .decode(envelope.payload.as_bytes())
        .map_err(|e| ManifestError::Malformed(format!("payload is not base64: {e}")))?;
    let sig_bytes: [u8; 64] = B64
        .decode(envelope.signature.as_bytes())
        .map_err(|e| ManifestError::Malformed(format!("signature is not base64: {e}")))?
        .try_into()
        .map_err(|_| ManifestError::Malformed("signature is not 64 bytes".to_owned()))?;

    // verify_strict, not verify: it refuses small-order public keys, for which
    // a signature can be forged that plain verification accepts.
    key.key
        .verify_strict(&payload, &Signature::from_bytes(&sig_bytes))
        .map_err(|_| ManifestError::BadSignature)?;

    let manifest: Manifest = serde_json::from_slice(&payload)
        .map_err(|e| ManifestError::Malformed(format!("payload is not a manifest: {e}")))?;
    manifest.validate()?;

    if manifest.platform != expect.platform || manifest.channel != expect.channel {
        return Err(ManifestError::WrongTarget {
            expected: format!("{}/{}", expect.platform, expect.channel),
            found: format!("{}/{}", manifest.platform, manifest.channel),
        });
    }

    Ok(manifest)
}

/// A fresh signing key, drawn from OS entropy.
///
/// Workspace rule (nekko-crypto-core/src/rng.rs): OS entropy only, never
/// `rand::thread_rng()`.
pub fn generate_signing_key() -> SigningKey {
    SigningKey::generate(&mut rand_core::OsRng)
}

/// Re-exported under names that say what they are for, so a caller — the
/// generator binary, or whatever wires the baked-in keys — need not depend on
/// ed25519-dalek itself.
pub use ed25519_dalek::{SigningKey as ReleaseSigningKey, VerifyingKey as ReleaseVerifyingKey};

#[cfg(test)]
mod tests {
    use super::*;
    use crate::manifest::{FileEntry, Version};

    fn manifest(platform: Platform, channel: Channel) -> Manifest {
        Manifest {
            version: Version::parse("1.2.3").unwrap(),
            channel,
            platform,
            minimum_version: Version::parse("1.0.0").unwrap(),
            published_at: 1_757_563_200,
            base_url: "https://update.nekkochat.com".to_owned(),
            files: vec![FileEntry {
                path: "Contents/MacOS/nekko".to_owned(),
                sha256: "a".repeat(64),
                size: 8_123_456,
                // The bundle's program: the one entry where this bit decides
                // whether the installed application starts.
                executable: true,
            }],
            links: Vec::new(),
        }
    }

    fn expect() -> Expect {
        Expect {
            platform: Platform::Macos,
            channel: Channel::Stable,
        }
    }

    fn trusted(id: &str, signing: &SigningKey) -> TrustedKey {
        TrustedKey {
            id: id.to_owned(),
            key: signing.verifying_key(),
        }
    }

    /// Signs arbitrary bytes, which `sign` deliberately cannot do — it
    /// validates first. Needed to prove the VERIFIER also refuses a manifest
    /// that a correctly-held key signed: the generator is not the only way
    /// bytes can get signed, and "it was signed by us" must not imply "it is
    /// safe to act on".
    fn envelope_over(payload: &[u8], key_id: &str, signing: &SigningKey) -> Vec<u8> {
        let envelope = Envelope {
            key_id: key_id.to_owned(),
            signature: B64.encode(signing.sign(payload).to_bytes()),
            payload: B64.encode(payload),
        };
        serde_json::to_vec(&envelope).unwrap()
    }

    #[test]
    fn a_signed_manifest_comes_back_unchanged() {
        let signing = generate_signing_key();
        let original = manifest(Platform::Macos, Channel::Stable);
        let bytes = sign(&original, "release-1", &signing).unwrap();
        let got = verify(&bytes, &[trusted("release-1", &signing)], expect()).unwrap();
        assert_eq!(got, original);
    }

    #[test]
    fn one_flipped_byte_in_the_payload_is_caught() {
        let signing = generate_signing_key();
        let bytes = sign(&manifest(Platform::Macos, Channel::Stable), "k", &signing).unwrap();

        // Alter the manifest inside the envelope: decode, edit, re-encode, keep
        // the original signature. This is what a CDN-side tamper looks like.
        let mut envelope: Envelope = serde_json::from_slice(&bytes).unwrap();
        let mut payload = B64.decode(envelope.payload.as_bytes()).unwrap();
        let version_pos = payload
            .windows(5)
            .position(|w| w == b"1.2.3")
            .expect("the version string is in the payload");
        payload[version_pos + 4] = b'9'; // 1.2.3 -> 1.2.9
        envelope.payload = B64.encode(&payload);
        let tampered = serde_json::to_vec(&envelope).unwrap();

        assert_eq!(
            verify(&tampered, &[trusted("k", &signing)], expect()),
            Err(ManifestError::BadSignature)
        );
    }

    #[test]
    fn a_signature_from_another_key_is_caught() {
        let ours = generate_signing_key();
        let theirs = generate_signing_key();
        // Signed by someone else, but announced under an id we trust.
        let bytes = sign(&manifest(Platform::Macos, Channel::Stable), "k", &theirs).unwrap();
        assert_eq!(
            verify(&bytes, &[trusted("k", &ours)], expect()),
            Err(ManifestError::BadSignature)
        );
    }

    #[test]
    fn an_unknown_key_id_is_not_reported_as_a_bad_signature() {
        // The two are different situations: this one is what a client sees once
        // signing has rotated past both keys it was built with, and no retry
        // will fix it.
        let signing = generate_signing_key();
        let bytes = sign(
            &manifest(Platform::Macos, Channel::Stable),
            "release-3",
            &signing,
        )
        .unwrap();
        assert_eq!(
            verify(&bytes, &[trusted("release-1", &signing)], expect()),
            Err(ManifestError::UnknownKey("release-3".to_owned()))
        );
    }

    #[test]
    fn rotation_works_because_the_client_carries_two_keys() {
        let current = generate_signing_key();
        let next = generate_signing_key();
        let keys = [trusted("release-1", &current), trusted("release-2", &next)];

        // Today's manifest, signed with the current key.
        let today = sign(
            &manifest(Platform::Macos, Channel::Stable),
            "release-1",
            &current,
        )
        .unwrap();
        verify(&today, &keys, expect()).unwrap();

        // After rotation: same installation, key it already trusts.
        let after = sign(
            &manifest(Platform::Macos, Channel::Stable),
            "release-2",
            &next,
        )
        .unwrap();
        verify(&after, &keys, expect()).unwrap();
    }

    #[test]
    fn a_genuine_manifest_for_the_wrong_target_is_refused() {
        let signing = generate_signing_key();
        // Correctly signed Windows manifest served at the macOS URL. The
        // signature covers bytes, not the URL, so only the field inside the
        // payload can catch this.
        let windows = sign(&manifest(Platform::Windows, Channel::Stable), "k", &signing).unwrap();
        assert!(matches!(
            verify(&windows, &[trusted("k", &signing)], expect()),
            Err(ManifestError::WrongTarget { .. })
        ));

        let beta = sign(&manifest(Platform::Macos, Channel::Beta), "k", &signing).unwrap();
        assert!(matches!(
            verify(&beta, &[trusted("k", &signing)], expect()),
            Err(ManifestError::WrongTarget { .. })
        ));
    }

    #[test]
    fn the_signer_refuses_to_sign_a_manifest_it_should_not() {
        let signing = generate_signing_key();
        let mut m = manifest(Platform::Macos, Channel::Stable);
        m.files[0].path = "../../../etc/cron.d/payload".to_owned();
        assert!(matches!(
            sign(&m, "k", &signing),
            Err(ManifestError::InvalidFile(_))
        ));
    }

    #[test]
    fn the_verifier_refuses_it_too_even_with_a_valid_signature() {
        // Defence in depth, and not a theoretical one: the generator is one way
        // bytes get signed, not the only way. A verifier that trusted paths
        // because the signature checked out would turn any signing mishap into
        // a write-anywhere.
        let signing = generate_signing_key();
        let payload = br#"{
            "version": "1.2.3",
            "channel": "stable",
            "platform": "macos",
            "minimum_version": "1.0.0",
            "published_at": 1,
            "base_url": "https://u",
            "files": [{"path":"../../../etc/cron.d/payload","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","size":1}]
        }"#;
        let bytes = envelope_over(payload, "k", &signing);
        assert!(matches!(
            verify(&bytes, &[trusted("k", &signing)], expect()),
            Err(ManifestError::InvalidFile(_))
        ));
    }

    #[test]
    fn garbage_is_rejected_before_anything_is_believed() {
        let signing = generate_signing_key();
        let keys = [trusted("k", &signing)];

        for junk in [
            &b""[..],
            b"not json",
            br#"{"key_id":"k"}"#,
            br#"{"key_id":"k","signature":"!!!","payload":"!!!"}"#,
            br#"{"key_id":"k","signature":"AAAA","payload":"e30="}"#,
        ] {
            assert!(
                verify(junk, &keys, expect()).is_err(),
                "accepted junk: {:?}",
                String::from_utf8_lossy(junk)
            );
        }

        // Valid signature over bytes that are not a manifest at all.
        let bytes = envelope_over(b"{}", "k", &signing);
        assert!(matches!(
            verify(&bytes, &keys, expect()),
            Err(ManifestError::Malformed(_))
        ));
    }

    #[test]
    fn a_public_key_reads_back_from_its_hex_form() {
        let signing = generate_signing_key();
        let hex_form = hex::encode(signing.verifying_key().to_bytes());
        let key = TrustedKey::from_hex("release-1", &format!("  {hex_form}\n")).unwrap();
        let bytes = sign(
            &manifest(Platform::Macos, Channel::Stable),
            "release-1",
            &signing,
        )
        .unwrap();
        verify(&bytes, &[key], expect()).unwrap();

        assert!(TrustedKey::from_hex("x", "not hex").is_err());
        assert!(TrustedKey::from_hex("x", "aabb").is_err());
    }
}
