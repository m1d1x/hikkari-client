//! `nekko-manifest` — makes and checks release manifests.
//!
//! Four subcommands, two of which the release pipeline (38.9) needs:
//!
//! ```sh
//! # once, wherever the signing key is supposed to live
//! nekko-manifest keygen --out release.key
//!
//! # in CI, over the packaged application directory: the whole channel tree,
//! # ready to sync to the bucket behind update.nekkochat.com (38.3)
//! nekko-manifest publish --dir build/macos/Nekko.app --version 0.2.0 \
//!     --platform macos --base-url https://update.nekkochat.com \
//!     --key release.key --key-id release-1 --out channel/
//!
//! # just the signed manifest, no file bodies — for inspecting a build
//! nekko-manifest build --dir build/macos/Nekko.app --version 0.2.0 \
//!     --platform macos --base-url https://update.nekkochat.com \
//!     --key release.key --key-id release-1 --out manifest.json
//!
//! # by hand, to confirm that what was published is what a client will accept
//! nekko-manifest verify --manifest manifest.json --public-key <hex> \
//!     --key-id release-1 --platform macos
//! ```
//!
//! It lives in the same crate as the verifier on purpose: a manifest is only
//! trustworthy if the bytes the signer signed are the bytes the client checks,
//! and one implementation cannot disagree with itself. A generator in another
//! language would need cross-language fixtures to stay honest, the way
//! internal/blobcrypto and nekko-crypto-core do.
//!
//! The argument parsing is hand-rolled: four subcommands do not justify
//! putting a CLI framework into a crate the client links.

use std::collections::BTreeMap;
use std::fs;
use std::path::{Path, PathBuf};
use std::process::ExitCode;

use nekko_updater::channel::{self as chan, VersionProbe};
use nekko_updater::manifest::{Channel, FileEntry, Manifest, Platform, Version};
use nekko_updater::signature::{self, Expect, TrustedKey};

const USAGE: &str = "usage: nekko-manifest <keygen|build|publish|verify> [options]\n\
                     see the module docs in src/bin/nekko-manifest.rs";

fn main() -> ExitCode {
    let args: Vec<String> = std::env::args().skip(1).collect();
    let result = match args.first().map(String::as_str) {
        Some("keygen") => keygen(&args[1..]),
        Some("build") => build(&args[1..]),
        Some("publish") => publish(&args[1..]),
        Some("verify") => verify(&args[1..]),
        _ => Err(USAGE.to_owned()),
    };
    match result {
        Ok(()) => ExitCode::SUCCESS,
        Err(why) => {
            eprintln!("nekko-manifest: {why}");
            ExitCode::FAILURE
        }
    }
}

/// `--flag value` pairs into a map. An unknown flag is an error rather than
/// ignored: a typo in a release command must not quietly produce a manifest
/// that is not what was asked for.
fn flags(args: &[String], known: &[&str]) -> Result<BTreeMap<String, String>, String> {
    let mut out = BTreeMap::new();
    let mut i = 0;
    while i < args.len() {
        let flag = args[i]
            .strip_prefix("--")
            .ok_or_else(|| format!("expected a --flag, got {:?}", args[i]))?;
        if !known.contains(&flag) {
            return Err(format!(
                "unknown flag --{flag}; known: {}",
                known.join(", ")
            ));
        }
        let value = args
            .get(i + 1)
            .ok_or_else(|| format!("--{flag} needs a value"))?;
        out.insert(flag.to_owned(), value.clone());
        i += 2;
    }
    Ok(out)
}

fn need<'a>(f: &'a BTreeMap<String, String>, key: &str) -> Result<&'a str, String> {
    f.get(key)
        .map(String::as_str)
        .ok_or_else(|| format!("--{key} is required"))
}

fn keygen(args: &[String]) -> Result<(), String> {
    let f = flags(args, &["out"])?;
    let out = PathBuf::from(need(&f, "out")?);
    if out.exists() {
        return Err(format!(
            "{} already exists — refusing to overwrite a signing key",
            out.display()
        ));
    }

    let signing = signature::generate_signing_key();
    fs::write(&out, hex::encode(signing.to_bytes())).map_err(|e| format!("writing key: {e}"))?;

    // A signing key readable by every account on the machine is not a signing
    // key. Best effort: Windows has no mode to set, which is one more reason
    // the real key belongs in a CI secret rather than on a disk.
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(&out, fs::Permissions::from_mode(0o600))
            .map_err(|e| format!("tightening key permissions: {e}"))?;
    }

    println!("private key written to {} (mode 600)", out.display());
    println!(
        "public key: {}",
        hex::encode(signing.verifying_key().to_bytes())
    );
    println!(
        "\nThe public key goes into the client's trusted list; the private key\n\
         goes into the release pipeline's secret store and nowhere else.\n\
         Never commit it."
    );
    Ok(())
}

/// Flags shared by `build` and `publish` — they describe the same release and
/// differ only in what they write.
const RELEASE_FLAGS: &[&str] = &[
    "dir",
    "version",
    "channel",
    "platform",
    "minimum-version",
    "base-url",
    "key",
    "key-id",
    "out",
];

/// A release, scanned and signed, before anything has been written.
struct Release {
    manifest: Manifest,
    /// The signed envelope — the bytes a client verifies, byte for byte.
    envelope: Vec<u8>,
    /// The tree the entries were read from; `publish` copies blobs out of it.
    dir: PathBuf,
    key_id: String,
}

/// Walk the packaged application, hash it, and sign the result.
///
/// Shared by both writers so that the manifest a human inspects with `build`
/// and the one `publish` puts on the CDN come out of one code path.
fn assemble(f: &BTreeMap<String, String>) -> Result<Release, String> {
    let dir = PathBuf::from(need(f, "dir")?);
    let version = parse_version(need(f, "version")?)?;
    let minimum_version = match f.get("minimum-version") {
        Some(v) => parse_version(v)?,
        None => Version {
            major: 0,
            minor: 0,
            patch: 0,
        },
    };
    let channel = parse_channel(f.get("channel").map(String::as_str))?;
    let platform = parse_platform(f.get("platform").map(String::as_str))?;
    let key_id = need(f, "key-id")?.to_owned();

    let key_hex = fs::read_to_string(need(f, "key")?).map_err(|e| format!("reading key: {e}"))?;
    let signing = signing_key_from_hex(key_hex.trim())?;

    // The walker lives in the library, not here: what this records is what
    // `stage::prepare` rebuilds, and the crate's tests walk that round trip.
    let scanned = nekko_updater::scan::scan_tree(&dir).map_err(|e| e.to_string())?;

    let manifest = Manifest {
        version,
        channel,
        platform,
        minimum_version,
        published_at: unix_now(),
        base_url: need(f, "base-url")?.to_owned(),
        files: scanned.files,
        links: scanned.links,
    };

    let envelope =
        signature::sign(&manifest, &key_id, &signing).map_err(|e| format!("signing: {e}"))?;

    Ok(Release {
        manifest,
        envelope,
        dir,
        key_id,
    })
}

fn build(args: &[String]) -> Result<(), String> {
    let f = flags(args, RELEASE_FLAGS)?;
    let release = assemble(&f)?;
    let manifest = &release.manifest;

    match f.get("out") {
        Some(path) => {
            fs::write(path, &release.envelope).map_err(|e| format!("writing manifest: {e}"))?;
            let total: u64 = manifest.files.iter().map(|x| x.size).sum();
            println!(
                "{} files, {} links, {total} bytes, version {} ({}/{}) → {path}",
                manifest.files.len(),
                manifest.links.len(),
                manifest.version,
                manifest.platform,
                manifest.channel
            );
        }
        None => print!("{}", String::from_utf8_lossy(&release.envelope)),
    }
    Ok(())
}

/// Write the whole channel tree: file bodies, the signed manifest, the probe.
///
/// ```text
///   <out>/blobs/<sha256>                  every file body, content-addressed
///   <out>/stable/macos/manifest.json      the signed envelope
///   <out>/stable/macos/manifest-0.2.0.json  the same bytes, kept by version
///   <out>/stable/macos/latest.json        the cheap probe
/// ```
///
/// Idempotent by construction: a blob whose digest is already in the tree is
/// already that blob. Re-publishing the same build copies nothing, and the
/// second release of a day copies only what the build actually changed —
/// which is the same property that makes the client's download cheap, showing
/// up on the publishing side.
///
/// ⚠️ **Order is load-bearing: bodies, then manifest, then probe.** The probe
/// is the only thing that makes a client fetch, so it must be the last to
/// exist; and the manifest must not be readable before the blobs it names.
/// A sync to the bucket (38.9) has to preserve that order — uploading the
/// probe first means every client that polls during the upload fetches a
/// manifest pointing at blobs that are not there yet.
fn publish(args: &[String]) -> Result<(), String> {
    let f = flags(args, RELEASE_FLAGS)?;
    let root = PathBuf::from(need(&f, "out")?);
    let release = assemble(&f)?;
    let manifest = &release.manifest;

    let blobs = root.join(chan::BLOBS_DIR);
    let prefix = root
        .join(manifest.channel.to_string())
        .join(manifest.platform.to_string());
    fs::create_dir_all(&blobs).map_err(|e| format!("creating {}: {e}", blobs.display()))?;
    fs::create_dir_all(&prefix).map_err(|e| format!("creating {}: {e}", prefix.display()))?;

    let mut written = 0usize;
    let mut bytes = 0u64;
    for entry in &manifest.files {
        if copy_blob(&release.dir, entry, &blobs)? {
            written += 1;
            bytes += entry.size;
        }
    }

    // Links are not blobs: nothing to upload, the staging step (38.4) creates
    // them from the manifest.
    let manifest_path = prefix.join(chan::MANIFEST_FILE);
    write_atomic(&manifest_path, &release.envelope)?;
    // The same bytes under the version they describe. Pulling a bad release is
    // then `cp manifest-0.1.0.json manifest.json` plus the matching probe —
    // possible for whoever holds the bucket, without the signing key and
    // without a rebuild. That is what "the stop-cock is a file overwrite"
    // means in practice.
    write_atomic(
        &prefix.join(format!("manifest-{}.json", manifest.version)),
        &release.envelope,
    )?;

    let probe = VersionProbe {
        version: manifest.version,
        platform: manifest.platform,
        channel: manifest.channel,
        // The manifest's own stamp rather than a second call to the clock: two
        // numbers that should be equal and are read side by side must not be
        // able to differ by the duration of a publish.
        published_at: manifest.published_at,
    };
    let probe_bytes = probe
        .to_bytes()
        .map_err(|e| format!("serialising the probe: {e}"))?;
    write_atomic(&prefix.join(chan::PROBE_FILE), &probe_bytes)?;

    let total: u64 = manifest.files.iter().map(|x| x.size).sum();
    println!(
        "version {} ({}/{}), key {}: {} files ({total} bytes), {} links\n\
         {written} new blobs ({bytes} bytes copied), {} already present\n\
         → {}",
        manifest.version,
        manifest.platform,
        manifest.channel,
        release.key_id,
        manifest.files.len(),
        manifest.links.len(),
        manifest.files.len() - written,
        root.display(),
    );
    Ok(())
}

/// Put one file body in the blob store. Returns whether it had to be copied.
///
/// A digest already present is not re-copied: the name IS the content. The
/// size is checked anyway, because that costs one `stat` and catches a channel
/// tree somebody edited by hand — the one way a wrong body can end up under a
/// right name here.
fn copy_blob(dir: &Path, entry: &FileEntry, blobs: &Path) -> Result<bool, String> {
    let blob = blobs.join(&entry.sha256);
    match fs::metadata(&blob) {
        Ok(meta) if meta.len() == entry.size => return Ok(false),
        Ok(meta) => {
            return Err(format!(
                "{} is {} bytes but {} says {} — the blob store has been \
                 tampered with or truncated; delete it and republish",
                blob.display(),
                meta.len(),
                entry.path,
                entry.size
            ))
        }
        Err(_) => {}
    }

    // Copy to a temporary name and rename into place, so an interrupted
    // publish cannot leave a half-written body under a digest that promises
    // its whole content. A client would reject such a blob by hash and retry
    // it forever, which is a bug that looks like a network fault.
    let source = dir.join(&entry.path);
    let tmp = blobs.join(format!(".tmp-{}", entry.sha256));
    fs::copy(&source, &tmp).map_err(|e| format!("copying {}: {e}", source.display()))?;
    fs::rename(&tmp, &blob).map_err(|e| format!("placing {}: {e}", blob.display()))?;
    Ok(true)
}

/// Write via a temporary file and a rename: a reader that polls mid-publish
/// sees the old file or the new one, never a truncated one.
fn write_atomic(path: &Path, bytes: &[u8]) -> Result<(), String> {
    let tmp = path.with_extension("tmp");
    fs::write(&tmp, bytes).map_err(|e| format!("writing {}: {e}", tmp.display()))?;
    fs::rename(&tmp, path).map_err(|e| format!("placing {}: {e}", path.display()))
}

fn verify(args: &[String]) -> Result<(), String> {
    let f = flags(
        args,
        &["manifest", "public-key", "key-id", "platform", "channel"],
    )?;

    let bytes = fs::read(need(&f, "manifest")?).map_err(|e| format!("reading manifest: {e}"))?;
    let key_id = need(&f, "key-id")?;
    let trusted = TrustedKey::from_hex(key_id, need(&f, "public-key")?)
        .map_err(|e| format!("public key: {e}"))?;
    let platform = parse_platform(f.get("platform").map(String::as_str))?;
    let channel = parse_channel(f.get("channel").map(String::as_str))?;

    let manifest = signature::verify(&bytes, &[trusted], Expect { platform, channel })
        .map_err(|e| format!("REJECTED: {e}"))?;

    println!(
        "accepted: version {} ({}/{}), minimum {}, {} files",
        manifest.version,
        manifest.platform,
        manifest.channel,
        manifest.minimum_version,
        manifest.files.len()
    );
    Ok(())
}

fn parse_version(s: &str) -> Result<Version, String> {
    Version::parse(s).map_err(|e| e.to_string())
}

fn parse_channel(arg: Option<&str>) -> Result<Channel, String> {
    match arg {
        None | Some("stable") => Ok(Channel::Stable),
        Some("beta") => Ok(Channel::Beta),
        Some(other) => Err(format!("unknown channel {other:?}")),
    }
}

fn parse_platform(arg: Option<&str>) -> Result<Platform, String> {
    match arg {
        Some("macos") => Ok(Platform::Macos),
        Some("windows") => Ok(Platform::Windows),
        Some(other) => Err(format!("unknown platform {other:?}")),
        None => Platform::current().ok_or_else(|| "--platform is required on this host".to_owned()),
    }
}

fn signing_key_from_hex(s: &str) -> Result<signature::ReleaseSigningKey, String> {
    let raw = hex::decode(s).map_err(|e| format!("key is not hex: {e}"))?;
    let bytes: [u8; 32] = raw
        .try_into()
        .map_err(|_| "key is not 32 bytes".to_owned())?;
    Ok(signature::ReleaseSigningKey::from_bytes(&bytes))
}

fn unix_now() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0)
}
