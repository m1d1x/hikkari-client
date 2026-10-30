//! The manifest: what a release consists of, and what the client decides after
//! reading one.
//!
//! # Shape
//!
//! ```json
//! {
//!   "version": "0.2.0",
//!   "channel": "stable",
//!   "platform": "macos",
//!   "minimum_version": "0.1.0",
//!   "published_at": 1757563200,
//!   "base_url": "https://update.nekkochat.com",
//!   "files": [{ "path": "Contents/MacOS/nekko", "sha256": "…", "size": 8123456 }]
//! }
//! ```
//!
//! Per-file digests rather than one archive, because the two heaviest things we
//! ship — the ONNX Runtime dylib and the NekkoNoise models — do not change
//! between builds, and an archive would resend them every time (plan 38 §0.3).
//! The same list doubles as a self-check of an installation: "the app is acting
//! strange" becomes "compare the hashes", which is the difference between a
//! diagnosis and a guess.
//!
//! # Why `platform` is inside the signed payload
//!
//! A signature covers bytes, not the URL they were served from. Without this
//! field a genuine, correctly signed Windows manifest handed to a Mac client at
//! the Mac URL would verify — and the updater would then replace an app with
//! another platform's files. The channel is in there for the same reason.

use serde::{Deserialize, Serialize};

use crate::error::ManifestError;

/// `major.minor.patch`, ordered numerically.
///
/// Hand-rolled rather than a semver dependency because this is the whole of
/// what the project's versions ever are — `pubspec.yaml` and
/// `client-core/Cargo.toml` both carry exactly three numbers, pinned to each
/// other by `apps/nekko/test/app_version_test.dart`. Ordering is the only
/// operation anyone needs, and the string form round-trips.
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord)]
pub struct Version {
    pub major: u32,
    pub minor: u32,
    pub patch: u32,
}

impl Version {
    /// Parse `"1.2.3"`. Trailing build metadata is NOT accepted: `pubspec`'s
    /// `+3` counter belongs to the packager and would make two manifests that
    /// order differently look equal here.
    pub fn parse(s: &str) -> Result<Self, ManifestError> {
        let bad = || ManifestError::BadVersion(s.to_owned());
        let mut parts = s.split('.');
        let mut next = || -> Result<u32, ManifestError> {
            parts
                .next()
                .ok_or_else(bad)?
                .parse::<u32>()
                .map_err(|_| bad())
        };
        let v = Version {
            major: next()?,
            minor: next()?,
            patch: next()?,
        };
        if parts.next().is_some() {
            return Err(bad());
        }
        Ok(v)
    }
}

impl std::fmt::Display for Version {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}.{}.{}", self.major, self.minor, self.patch)
    }
}

impl Serialize for Version {
    fn serialize<S: serde::Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
        s.serialize_str(&self.to_string())
    }
}

impl<'de> Deserialize<'de> for Version {
    fn deserialize<D: serde::Deserializer<'de>>(d: D) -> Result<Self, D::Error> {
        let s = String::deserialize(d)?;
        Version::parse(&s).map_err(serde::de::Error::custom)
    }
}

/// Release track. Two of them exist in the format from the start because
/// adding a field to a signed artefact later means the clients already in the
/// field cannot read it (plan 38 §4); only `Stable` is served today.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Channel {
    Stable,
    Beta,
}

/// Which build a manifest describes. Not a guess from the running binary at
/// verification time — the caller states what it expects, so a mismatch is a
/// rejection rather than an assumption.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Platform {
    Macos,
    Windows,
}

impl Platform {
    /// The platform this binary was compiled for.
    pub fn current() -> Option<Self> {
        match std::env::consts::OS {
            "macos" => Some(Platform::Macos),
            "windows" => Some(Platform::Windows),
            _ => None,
        }
    }
}

impl std::fmt::Display for Platform {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Platform::Macos => write!(f, "macos"),
            Platform::Windows => write!(f, "windows"),
        }
    }
}

impl std::fmt::Display for Channel {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Channel::Stable => write!(f, "stable"),
            Channel::Beta => write!(f, "beta"),
        }
    }
}

/// One file of an installation.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct FileEntry {
    /// Where the file goes, relative to the install root, with `/` separators
    /// on every platform. Validated (see [`Manifest::validate`]) because this
    /// string decides where bytes get written.
    pub path: String,
    /// Lowercase hex sha256 of the file's contents. This is both the integrity
    /// check and the identity: the updater downloads an entry only when the
    /// digest differs from what is installed.
    pub sha256: String,
    /// Size in bytes. Not a security property (the digest is) — it is what
    /// lets the client show progress and refuse a download that has clearly
    /// run away.
    pub size: u64,
    /// Whether this file must be executable, on platforms that have such a
    /// bit.
    ///
    /// ⚠️ Found by 38.4, not by the format work, and it is load-bearing: a
    /// staging tree assembled from downloaded blobs has no way to know that
    /// `Contents/MacOS/nekko` is a program. HTTP carries no file mode, and
    /// `fs::copy` preserves one only for entries taken from the old
    /// installation — while the one file guaranteed to change in every release
    /// is exactly the binary. Without this bit the updater would build a
    /// bundle whose every byte is correct and which cannot be launched.
    ///
    /// One bit rather than a mode: inside an application bundle the only
    /// distinction that exists is 755 against 644. Omitted from the JSON when
    /// false — at 990 files the noise would outweigh the information.
    #[serde(default, skip_serializing_if = "is_false")]
    pub executable: bool,
}

fn is_false(b: &bool) -> bool {
    !*b
}

impl FileEntry {
    /// Where to fetch this entry from, given the manifest's `base_url`.
    ///
    /// Content-addressed rather than `<base>/<version>/<path>`: the digest is
    /// already the identity, so an unchanged file keeps one URL across
    /// releases, which is what makes "download only what changed" true at the
    /// CDN as well as on disk. The layout behind `base_url` is 38.3's to own;
    /// this is the one line that couples the two.
    pub fn url(&self, base_url: &str) -> String {
        format!(
            "{}/{}/{}",
            base_url.trim_end_matches('/'),
            crate::channel::BLOBS_DIR,
            self.sha256
        )
    }
}

/// A symlink of an installation.
///
/// ⚠️ This exists because of a measured fact, not for completeness: the real
/// `nekko.app` carries **24 symlinks across 990 files** — every embedded
/// `.framework` ships the `Versions/Current` pair that Apple's bundle layout
/// requires. A manifest of paths and digests alone cannot describe our own
/// application: following a link would record the target's bytes twice under
/// two paths, and an updater rebuilding from that would write two real files
/// where the bundle needs a link, producing a bundle that looks complete and
/// is not signed-correct.
///
/// Copying the old installation instead of rebuilding it does not rescue the
/// file-only format either: a plugin added in a new release brings a framework
/// whose links exist nowhere to copy from.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct LinkEntry {
    /// Where the link goes, relative to the install root. Same rules as
    /// [`FileEntry::path`].
    pub path: String,
    /// What it points at, relative to the link's own directory — the form
    /// Apple's bundles use (`Versions/Current/Resources`). Absolute targets are
    /// refused: a link out of the bundle is not part of the release.
    pub target: String,
}

/// A release, as described by its (signed) manifest.
///
/// `deny_unknown_fields` is deliberately NOT set. A manifest is served to
/// installations that already exist, and the one thing that cannot be fixed
/// remotely is a client that refuses to parse the file telling it to update —
/// the same reasoning as the minimum-version field on the wire (plan 38 §38.6).
/// Adding a field must therefore be safe for old clients by construction.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Manifest {
    /// The version this release installs.
    pub version: Version,
    pub channel: Channel,
    pub platform: Platform,
    /// Installations below this are required to update rather than asked
    /// (see [`decide`]).
    pub minimum_version: Version,
    /// Epoch seconds the manifest was generated. Carried for telemetry and for
    /// a human staring at two manifests wondering which is newer.
    ///
    /// ⚠️ It is NOT yet a freshness check: a signed manifest replayed forever
    /// would keep a client from ever learning about a newer release, and
    /// defending against that needs a policy (how stale is too stale) plus a
    /// trustworthy clock. Deliberately deferred, not overlooked.
    pub published_at: i64,
    /// Root the file URLs are built from (38.3).
    pub base_url: String,
    pub files: Vec<FileEntry>,
    /// Symlinks the installation needs. `#[serde(default)]` so a Windows
    /// manifest — which has none — simply omits the field, and so that a
    /// manifest written before this field existed still parses.
    #[serde(default)]
    pub links: Vec<LinkEntry>,
}

impl Manifest {
    /// Everything that must hold before the updater may act on this manifest.
    ///
    /// Called by [`crate::signature::verify`] — a manifest that has not been
    /// through signature *and* these checks never reaches a caller, so no
    /// downstream code has to remember to re-check.
    pub fn validate(&self) -> Result<(), ManifestError> {
        if self.minimum_version > self.version {
            return Err(ManifestError::ImpossibleMinimum {
                minimum: self.minimum_version.to_string(),
                version: self.version.to_string(),
            });
        }
        if self.files.is_empty() {
            return Err(ManifestError::InvalidFile(
                "manifest lists no files".to_owned(),
            ));
        }
        if self.base_url.is_empty() {
            return Err(ManifestError::InvalidFile("base_url is empty".to_owned()));
        }

        let mut seen: Vec<&str> = Vec::with_capacity(self.files.len() + self.links.len());
        for f in &self.files {
            validate_path(&f.path)?;
            validate_digest(&f.sha256)?;
            claim(&mut seen, &f.path)?;
        }
        for l in &self.links {
            validate_path(&l.path)?;
            validate_link_target(&l.path, &l.target)?;
            claim(&mut seen, &l.path)?;
        }
        Ok(())
    }
}

/// One path, one entry — across files AND links together, because both end up
/// as a name in the same directory tree.
fn claim<'a>(seen: &mut Vec<&'a str>, path: &'a str) -> Result<(), ManifestError> {
    if seen.contains(&path) {
        return Err(ManifestError::InvalidFile(format!(
            "{path:?} listed twice — which entry wins is not a question the \
             updater should have to answer"
        )));
    }
    seen.push(path);
    Ok(())
}

/// A link is only acceptable if what it points at is also inside the install
/// directory.
///
/// Resolved lexically against the link's own directory, which is the right
/// answer here and not a shortcut: the target is checked before anything is
/// created, so there is no filesystem to ask, and a lexical check cannot be
/// fooled by a link that appears between the check and the write.
fn validate_link_target(path: &str, target: &str) -> Result<(), ManifestError> {
    let bad = |why: &str| {
        Err(ManifestError::InvalidFile(format!(
            "link {path:?} -> {target:?}: {why}"
        )))
    };
    if target.is_empty() {
        return bad("empty target");
    }
    if target.starts_with('/') || target.starts_with('\\') || target.contains(':') {
        return bad("absolute target — a link out of the bundle is not part of the release");
    }
    if target.contains('\\') {
        return bad("backslash separator");
    }

    // Everything but the last component of the link's own path is the directory
    // the target is relative to.
    let mut resolved: Vec<&str> = path.split('/').collect();
    resolved.pop();
    for component in target.split('/') {
        match component {
            "" => return bad("empty target component"),
            "." => {}
            ".." => {
                if resolved.pop().is_none() {
                    return bad("target climbs out of the install directory");
                }
            }
            other => resolved.push(other),
        }
    }
    if resolved.is_empty() {
        return bad("target resolves to the install directory itself");
    }
    Ok(())
}

/// A file path is only acceptable if it cannot address anything but a spot
/// inside the install directory.
///
/// This is the manifest's sharpest edge. The updater takes these strings and
/// writes bytes at them; a `..`, a leading slash or a drive letter turns a
/// signed manifest into "write anywhere the user can write". Rejecting them
/// here means no later stage has to be careful.
fn validate_path(path: &str) -> Result<(), ManifestError> {
    let bad = |why: &str| Err(ManifestError::InvalidFile(format!("{path:?}: {why}")));
    if path.is_empty() {
        return bad("empty path");
    }
    if path.starts_with('/') || path.starts_with('\\') {
        return bad("absolute path");
    }
    if path.contains('\\') {
        return bad("backslash separator — manifests use '/' on every platform");
    }
    if path.contains(':') {
        return bad("colon — a drive letter or an alternate data stream");
    }
    if path.contains('\0') {
        return bad("NUL byte");
    }
    // Component-wise, so that a legitimate name like "..dotfile" survives while
    // a real parent reference does not.
    for component in path.split('/') {
        match component {
            "" => return bad("empty path component — '//' or a trailing slash"),
            "." => return bad("'.' component"),
            ".." => return bad("'..' component escapes the install directory"),
            _ => {}
        }
    }
    Ok(())
}

fn validate_digest(digest: &str) -> Result<(), ManifestError> {
    let ok = digest.len() == 64
        && digest
            .bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b));
    if ok {
        Ok(())
    } else {
        Err(ManifestError::InvalidFile(format!(
            "{digest:?} is not a lowercase hex sha256"
        )))
    }
}

/// What to do about a verified manifest, given what is installed.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum UpdateDecision {
    /// The installation is already at the manifest's version.
    UpToDate,
    /// The manifest describes an OLDER release than what is installed. Not an
    /// error and not an update: either a release was pulled deliberately, or
    /// an old signed manifest is being replayed. Its own variant because the
    /// two are indistinguishable here and both deserve a log line — silently
    /// reporting "up to date" would hide a rollback attempt entirely.
    Stale,
    /// A newer release is available; the user chooses when (plan 38 §3.5 — an
    /// update never interrupts a call).
    Available,
    /// The installation is below `minimum_version` and must update before it
    /// can keep working.
    Mandatory,
}

/// Decide, never act. The caller owns the timing.
pub fn decide(installed: Version, manifest: &Manifest) -> UpdateDecision {
    if installed < manifest.minimum_version {
        return UpdateDecision::Mandatory;
    }
    match installed.cmp(&manifest.version) {
        std::cmp::Ordering::Less => UpdateDecision::Available,
        std::cmp::Ordering::Equal => UpdateDecision::UpToDate,
        std::cmp::Ordering::Greater => UpdateDecision::Stale,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn entry(path: &str) -> FileEntry {
        FileEntry {
            path: path.to_owned(),
            sha256: "a".repeat(64),
            size: 10,
            executable: false,
        }
    }

    fn manifest(files: Vec<FileEntry>) -> Manifest {
        Manifest {
            version: Version::parse("1.2.3").unwrap(),
            channel: Channel::Stable,
            platform: Platform::Macos,
            minimum_version: Version::parse("1.0.0").unwrap(),
            published_at: 1_757_563_200,
            base_url: "https://update.nekkochat.com".to_owned(),
            files,
            links: Vec::new(),
        }
    }

    fn link(path: &str, target: &str) -> LinkEntry {
        LinkEntry {
            path: path.to_owned(),
            target: target.to_owned(),
        }
    }

    #[test]
    fn versions_order_numerically_not_lexically() {
        let a = Version::parse("0.9.0").unwrap();
        let b = Version::parse("0.10.0").unwrap();
        // "0.10.0" < "0.9.0" as strings; the whole reason this is a type.
        assert!(a < b);
        assert_eq!(b.to_string(), "0.10.0");
    }

    #[test]
    fn a_pubspec_build_counter_is_not_a_version() {
        // `version: 0.1.0+3` in pubspec.yaml: the `+3` is the packager's
        // counter. Accepting it here would make two orderable manifests look
        // equal, so it is refused rather than trimmed.
        assert!(Version::parse("0.1.0+3").is_err());
        assert!(Version::parse("0.1").is_err());
        assert!(Version::parse("0.1.0.1").is_err());
        assert!(Version::parse("0.1.x").is_err());
    }

    #[test]
    fn version_survives_a_json_round_trip() {
        let v = Version::parse("3.14.15").unwrap();
        let json = serde_json::to_string(&v).unwrap();
        assert_eq!(json, "\"3.14.15\"");
        assert_eq!(serde_json::from_str::<Version>(&json).unwrap(), v);
    }

    #[test]
    fn a_file_url_is_content_addressed() {
        let e = entry("Contents/MacOS/nekko");
        // Trailing slash on the base must not double up.
        assert_eq!(
            e.url("https://update.nekkochat.com/"),
            format!("https://update.nekkochat.com/blobs/{}", "a".repeat(64))
        );
    }

    #[test]
    fn a_path_that_escapes_the_install_directory_is_refused() {
        for bad in [
            "../outside",
            "a/../../outside",
            "/etc/passwd",
            "\\\\server\\share",
            "C:/Windows/system32",
            "a//b",
            "./a",
            "a/",
            "",
        ] {
            let err = manifest(vec![entry(bad)]).validate().unwrap_err();
            assert!(
                matches!(err, ManifestError::InvalidFile(_)),
                "{bad:?} was accepted as {err:?}"
            );
        }
    }

    #[test]
    fn a_name_that_merely_looks_like_a_parent_reference_is_fine() {
        // Component-wise checking, so this survives.
        manifest(vec![entry("Contents/..dotfile")])
            .validate()
            .unwrap();
        manifest(vec![entry("a..b/c")]).validate().unwrap();
    }

    #[test]
    fn a_digest_that_is_not_a_sha256_is_refused() {
        for bad in [
            "",
            "abc",
            &"A".repeat(64), // uppercase
            &"g".repeat(64), // not hex
            &"a".repeat(63),
            &"a".repeat(65),
        ] {
            let mut m = manifest(vec![entry("f")]);
            m.files[0].sha256 = bad.to_owned();
            assert!(
                matches!(m.validate(), Err(ManifestError::InvalidFile(_))),
                "{bad:?} was accepted as a digest"
            );
        }
    }

    #[test]
    fn one_path_cannot_be_listed_twice() {
        let m = manifest(vec![entry("a"), entry("b"), entry("a")]);
        assert!(matches!(m.validate(), Err(ManifestError::InvalidFile(_))));
    }

    #[test]
    fn an_empty_release_is_refused() {
        assert!(matches!(
            manifest(vec![]).validate(),
            Err(ManifestError::InvalidFile(_))
        ));
        let mut m = manifest(vec![entry("a")]);
        m.base_url = String::new();
        assert!(matches!(m.validate(), Err(ManifestError::InvalidFile(_))));
    }

    #[test]
    fn a_minimum_above_the_shipped_version_is_refused() {
        let mut m = manifest(vec![entry("a")]);
        m.minimum_version = Version::parse("2.0.0").unwrap();
        assert!(matches!(
            m.validate(),
            Err(ManifestError::ImpossibleMinimum { .. })
        ));
        // Equal is fine: "this release is itself the floor".
        m.minimum_version = m.version;
        m.validate().unwrap();
    }

    #[test]
    fn the_decision_follows_from_what_is_installed() {
        let m = manifest(vec![entry("a")]); // version 1.2.3, minimum 1.0.0
        assert_eq!(
            decide(Version::parse("1.2.3").unwrap(), &m),
            UpdateDecision::UpToDate
        );
        assert_eq!(
            decide(Version::parse("1.1.0").unwrap(), &m),
            UpdateDecision::Available
        );
        assert_eq!(
            decide(Version::parse("0.9.9").unwrap(), &m),
            UpdateDecision::Mandatory
        );
        // Older manifest than what is installed: a pulled release or a replayed
        // manifest. Not reported as "up to date", which would hide it.
        assert_eq!(
            decide(Version::parse("2.0.0").unwrap(), &m),
            UpdateDecision::Stale
        );
    }

    #[test]
    fn an_unknown_field_does_not_make_a_manifest_unreadable() {
        // A client already installed must never be unable to parse the file
        // that tells it to update — the same argument as the wire's
        // minimum-version field (plan 38 §38.6).
        let json = r#"{
            "version": "1.0.0",
            "channel": "stable",
            "platform": "macos",
            "minimum_version": "1.0.0",
            "published_at": 1,
            "base_url": "https://u",
            "files": [{"path":"a","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","size":1}],
            "notes_for_humans": "a field invented after this client shipped"
        }"#;
        let m: Manifest = serde_json::from_str(json).unwrap();
        m.validate().unwrap();
    }

    #[test]
    fn a_framework_link_as_real_bundles_write_it_is_accepted() {
        // Exactly the shape found in nekko.app: 24 of these across 990 files.
        let mut m = manifest(vec![entry("Contents/Frameworks/F.framework/Versions/A/F")]);
        m.links = vec![
            link("Contents/Frameworks/F.framework/F", "Versions/Current/F"),
            link("Contents/Frameworks/F.framework/Versions/Current", "A"),
            link(
                "Contents/Frameworks/F.framework/Resources",
                "Versions/Current/Resources",
            ),
        ];
        m.validate().unwrap();
    }

    #[test]
    fn a_link_out_of_the_install_directory_is_refused() {
        for (path, target) in [
            ("a", "/etc/passwd"),
            ("a", "C:/Windows"),
            ("a", "../outside"),
            ("dir/a", "../../outside"),
            ("dir/a", "..\\outside"),
            ("a", ""),
            ("a", "."),
            ("dir/a", ".."),
        ] {
            let mut m = manifest(vec![entry("f")]);
            m.links = vec![link(path, target)];
            let err = m.validate().unwrap_err();
            assert!(
                matches!(err, ManifestError::InvalidFile(_)),
                "{path:?} -> {target:?} was accepted as {err:?}"
            );
        }
    }

    #[test]
    fn a_link_may_walk_up_as_long_as_it_lands_inside() {
        let mut m = manifest(vec![entry("f")]);
        // Contents/Frameworks/F.framework/Versions/A/Resources -> ../../Resources
        // resolves to Contents/Frameworks/F.framework/Resources. Inside.
        m.links = vec![link(
            "Contents/Frameworks/F.framework/Versions/A/Resources",
            "../../Resources",
        )];
        m.validate().unwrap();
    }

    #[test]
    fn a_link_cannot_take_a_path_a_file_already_claims() {
        let mut m = manifest(vec![entry("Contents/MacOS/nekko")]);
        m.links = vec![link("Contents/MacOS/nekko", "elsewhere")];
        assert!(matches!(m.validate(), Err(ManifestError::InvalidFile(_))));
    }

    #[test]
    fn a_manifest_without_links_still_parses() {
        // Windows releases have none, and manifests written before the field
        // existed omit it.
        let json = r#"{
            "version": "1.0.0",
            "channel": "stable",
            "platform": "windows",
            "minimum_version": "1.0.0",
            "published_at": 1,
            "base_url": "https://u",
            "files": [{"path":"nekko.exe","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","size":1}]
        }"#;
        let m: Manifest = serde_json::from_str(json).unwrap();
        assert!(m.links.is_empty());
        m.validate().unwrap();
    }
}
