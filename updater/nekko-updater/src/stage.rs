//! Building the next version on disk, beside the one that is running (38.4).
//!
//! ```text
//!   ~/Applications/Nekko.app          the installation, untouched by everything here
//!   ~/Applications/.nekko-staging/
//!       tree/        ← the new version, exactly the manifest and nothing else
//!       parts/       ← bodies still arriving, named by digest
//!       READY.json   ← written last, and only once `tree/` has been verified
//! ```
//!
//! Three properties this shape exists for.
//!
//! **The installation is not touched until the last metre.** Everything below
//! writes inside the staging directory only. An update interrupted at any
//! point leaves a working application and some disposable files — which is why
//! the swap (38.5) can be a rename of a finished tree rather than a sequence
//! of steps that must all succeed.
//!
//! **`parts/` and `READY.json` are beside `tree/`, not inside it.** A stray
//! directory inside a macOS bundle is not cosmetic: it is part of what the
//! signature covers, so a bundle carrying our scratch files would fail
//! validation as tampered-with. The tree contains the manifest and nothing
//! else, and that is checked before it is called ready.
//!
//! **Symlinks are created, never copied.** The live `nekko.app` holds 24 of
//! them (a `Versions/Current` pair per embedded framework, 990 files in total).
//! Copying the old installation instead of rebuilding it would not rescue us
//! either: a plugin added in a new release brings a framework whose links exist
//! nowhere to copy from.

use std::collections::{HashMap, HashSet};
use std::fs::{self, File};
use std::io::{Read, Seek, SeekFrom, Write};
use std::path::{Path, PathBuf};

use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

use crate::error::UpdateError;
use crate::manifest::{Channel, FileEntry, LinkEntry, Manifest, Platform, Version};
use crate::transport::UpdateTransport;

/// Read and write in 64 KiB pieces: big enough that the syscall overhead
/// disappears against a 124 MB file, small enough that "stop now" (a call
/// starting, plan 38 §3.5) is answered inside a few milliseconds.
const CHUNK: usize = 64 * 1024;

/// How many times one file is re-fetched before the update gives up.
///
/// Small on purpose. The real retry is the schedule — the client polls the
/// channel every few hours anyway, so a download that cannot finish now is
/// simply not finished now, and what arrived stays on disk for the next
/// attempt. Retrying harder inside one attempt only turns a bad release into
/// load on the channel.
const ATTEMPTS: u32 = 3;

/// Where an installation lives and where its replacement is built.
#[derive(Debug, Clone)]
pub struct Layout {
    install: PathBuf,
    work: PathBuf,
}

impl Layout {
    /// `install` is the directory manifest paths are relative to — the `.app`
    /// on macOS, the install directory on Windows. `work` is the staging
    /// directory beside it.
    pub fn new(install: impl Into<PathBuf>, work: impl Into<PathBuf>) -> Self {
        Layout {
            install: install.into(),
            work: work.into(),
        }
    }

    pub fn install(&self) -> &Path {
        &self.install
    }

    /// The staging directory itself — where the readiness flag, the pending
    /// record and the parts live. Everything except the tree.
    pub fn work(&self) -> &Path {
        &self.work
    }

    /// The new version, as a complete tree. The swap renames or moves this.
    pub fn tree(&self) -> PathBuf {
        self.work.join("tree")
    }

    /// Bodies mid-flight, named by digest — so a partial download survives
    /// anything except the file itself changing, including the channel moving
    /// to a newer release while we were fetching.
    pub fn parts(&self) -> PathBuf {
        self.work.join("parts")
    }

    /// The readiness flag, and the only thing 38.5 needs to read.
    pub fn ready_file(&self) -> PathBuf {
        self.work.join("READY.json")
    }

    /// Create the staging directories, and refuse a layout the swap could not
    /// finish.
    ///
    /// The same-volume check is the whole point: on macOS the swap is two
    /// renames, and `rename` across volumes fails. Catching that here costs a
    /// stat and happens before anything is downloaded; catching it at the last
    /// metre means a user who has downloaded 200 MB and cannot install it.
    pub fn prepare_dirs(&self) -> Result<(), UpdateError> {
        fs::create_dir_all(self.tree())
            .map_err(|e| UpdateError::io(format!("creating {}", self.tree().display()), e))?;
        fs::create_dir_all(self.parts())
            .map_err(|e| UpdateError::io(format!("creating {}", self.parts().display()), e))?;
        self.check_same_volume()
    }

    #[cfg(unix)]
    fn check_same_volume(&self) -> Result<(), UpdateError> {
        use std::os::unix::fs::MetadataExt;
        let install = fs::metadata(&self.install)
            .map_err(|e| UpdateError::io(format!("stat {}", self.install.display()), e))?;
        let work = fs::metadata(&self.work)
            .map_err(|e| UpdateError::io(format!("stat {}", self.work.display()), e))?;
        if install.dev() != work.dev() {
            return Err(UpdateError::DifferentVolume {
                staging: self.work.display().to_string(),
                install: self.install.display().to_string(),
            });
        }
        Ok(())
    }

    #[cfg(not(unix))]
    fn check_same_volume(&self) -> Result<(), UpdateError> {
        // Windows: staging is a sibling of the installation, exactly as on
        // macOS — a child would be carried away by the first rename of the
        // swap. A different drive or UNC share shows up as a different path
        // prefix, which is the case worth refusing; volumes mounted into a
        // folder are not something an install directory lives under.
        use std::path::Component;
        fn prefix(p: &Path) -> Option<String> {
            match p.components().next() {
                Some(Component::Prefix(pre)) => {
                    Some(pre.as_os_str().to_string_lossy().to_ascii_lowercase())
                }
                _ => None,
            }
        }
        let install = fs::canonicalize(&self.install)
            .map_err(|e| UpdateError::io(format!("resolving {}", self.install.display()), e))?;
        let work = fs::canonicalize(&self.work)
            .map_err(|e| UpdateError::io(format!("resolving {}", self.work.display()), e))?;
        if prefix(&install) != prefix(&work) {
            return Err(UpdateError::DifferentVolume {
                staging: self.work.display().to_string(),
                install: self.install.display().to_string(),
            });
        }
        if work.starts_with(&install) {
            return Err(UpdateError::Incomplete(format!(
                "{} is inside {} — the swap renames the installation and would take the \
                 staged tree with it",
                self.work.display(),
                self.install.display()
            )));
        }
        Ok(())
    }
}

/// What is ready to be installed, as left behind for the swap.
///
/// Written last and atomically: the presence of this file is the one signal
/// 38.5 acts on, so it must not exist a moment before the tree it describes is
/// complete.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Ready {
    pub version: Version,
    pub platform: Platform,
    pub channel: Channel,
    /// Epoch seconds the tree finished building. A staging directory that has
    /// been sitting unapplied for weeks is a user who never quits the
    /// application (plan 38 §3.5), which is worth knowing rather than
    /// guessing.
    pub built_at: i64,
    pub files: usize,
    pub links: usize,
}

/// The readiness flag, if there is one and it parses.
///
/// A flag that does not parse is treated as absent rather than as an error:
/// the cure is to rebuild the tree, which is what "absent" already means here.
pub fn read_ready(layout: &Layout) -> Option<Ready> {
    let bytes = fs::read(layout.ready_file()).ok()?;
    serde_json::from_slice(&bytes).ok()
}

/// Where every entry of the next version comes from.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Plan {
    /// Already correct in the staging tree — left from an earlier attempt at
    /// this same release, and nothing to do.
    ///
    /// ⚠️ This bucket is what makes an interrupted preparation cheap to
    /// resume, and it took a failing test to notice: a fetched body is
    /// *renamed* into the tree, so keeping `parts/` around only ever rescued
    /// the one file that was in flight. A download cut at 90% would have
    /// re-fetched 90% on the next attempt. The tree itself is the resume
    /// state; a second cache beside it would have cost another 245 MB of disk
    /// and an eviction policy to get wrong.
    pub keep: Vec<FileEntry>,
    /// Already on disk in the current installation, byte-identical, reusable.
    pub take: Vec<FileEntry>,
    /// Changed, new, missing — or present and corrupt.
    pub fetch: Vec<FileEntry>,
    /// Bytes that will actually cross the wire: [`Self::fetch`] summed **per
    /// distinct digest**, not per entry.
    ///
    /// Two paths holding the same bytes are one download and one blob URL, so
    /// counting them twice would announce a bigger update than the one that
    /// happens — and would make a progress bar that never reaches its end.
    pub fetch_bytes: u64,
}

/// Decide, per entry, whether it is already staged, can be taken from the
/// installation, or has to be downloaded — by digest, never by date or size
/// alone.
///
/// The digest is the identity, so this doubles as the self-check the per-file
/// format was chosen for (plan 38 §0.3): an installed file whose bytes have
/// rotted does not match, lands in `fetch`, and gets replaced. Which means a
/// damaged installation repairs itself on the next update instead of carrying
/// the damage forward — the outcome a whole-archive format cannot reach,
/// because it never looks at what is installed.
///
/// A file whose size already differs is not hashed: different length is
/// different content, and skipping the read is free.
pub fn plan(manifest: &Manifest, layout: &Layout) -> Plan {
    let mut out = Plan::default();
    let mut counted: HashSet<&str> = HashSet::new();
    let tree = layout.tree();
    for entry in &manifest.files {
        if has_these_bytes(&tree, entry) {
            out.keep.push(entry.clone());
        } else if has_these_bytes(layout.install(), entry) {
            out.take.push(entry.clone());
        } else {
            if counted.insert(entry.sha256.as_str()) {
                out.fetch_bytes += entry.size;
            }
            out.fetch.push(entry.clone());
        }
    }
    out
}

/// Whether `dir` already holds exactly this entry.
fn has_these_bytes(dir: &Path, entry: &FileEntry) -> bool {
    let path = dir.join(&entry.path);
    // symlink_metadata: a path the manifest calls a file and the disk holds as
    // a link is not a match, whatever the link points at.
    match fs::symlink_metadata(&path) {
        Ok(meta) if meta.is_file() && meta.len() == entry.size => {}
        _ => return false,
    }
    matches!(digest_of(&path), Ok(d) if d == entry.sha256)
}

/// How far along the preparation is, for whoever is drawing it.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Progress {
    pub bytes_done: u64,
    pub bytes_total: u64,
    pub files_done: usize,
    pub files_total: usize,
}

/// The caller's answer to a progress report.
///
/// Stopping is a first-class outcome, not an error path: an update must never
/// be the reason a call stutters (plan 38 §3.5), so whoever draws the progress
/// bar is also who says "not now". One knob instead of a second callback.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Control {
    Continue,
    Stop,
}

/// Build the staging tree for a verified manifest.
///
/// Idempotent: called again with a manifest whose version is already staged, it
/// reports the existing [`Ready`] and does nothing. Called again after being
/// stopped, it resumes — bodies already fetched are still in `parts/`, and
/// every file already taken from the installation is cheap to take again.
///
/// ⚠️ The manifest must already have come through
/// [`crate::signature::verify`]. Path validation lives there, which is what
/// makes it safe for this function to join manifest paths onto a directory.
pub fn prepare(
    manifest: &Manifest,
    layout: &Layout,
    transport: &dyn UpdateTransport,
    progress: &mut dyn FnMut(&Progress) -> Control,
) -> Result<Ready, UpdateError> {
    if crate::swap::is_refused(layout, manifest.version) {
        // Breaks a loop that would otherwise be endless: a release that fails
        // to start gets rolled back, the channel still serves it, and the next
        // poll would download and install it again — every few hours, forever.
        // A version that has already failed on this machine is not tried again
        // until someone says to (`swap::forget_refusals`).
        return Err(UpdateError::Refused {
            version: manifest.version.to_string(),
        });
    }
    if let Some(ready) = read_ready(layout) {
        // The flag alone is not enough: a confirmed swap turns the staged tree
        // into the installation, and a flag left behind would announce a tree
        // that is no longer there.
        if ready.version == manifest.version
            && ready.platform == manifest.platform
            && layout.tree().is_dir()
        {
            return Ok(ready);
        }
    }
    layout.prepare_dirs()?;

    // The flag goes first, not last: from here until the tree is verified there
    // is no ready update, and a crash in between must not leave a stale flag
    // pointing at a half-built tree.
    let _ = fs::remove_file(layout.ready_file());

    // Whatever is in the tree from an earlier attempt is kept only where it
    // matches this manifest. Everything else goes now — that is what makes
    // "the tree holds the manifest and nothing else" a property of the
    // process rather than a hope, and it is also why a stray file from a
    // previous release cannot ride into a bundle whose signature covers it.
    let tree = layout.tree();
    let mut described: HashSet<&str> = HashSet::with_capacity(manifest.files.len());
    described.extend(manifest.files.iter().map(|f| f.path.as_str()));
    described.extend(manifest.links.iter().map(|l| l.path.as_str()));
    prune(&tree, &tree, &described)?;

    let plan = plan(manifest, layout);
    log::info!(
        target: "updater",
        "preparing {}: keep {}, take {}, fetch {} ({} bytes)",
        manifest.version,
        plan.keep.len(),
        plan.take.len(),
        plan.fetch.len(),
        plan.fetch_bytes
    );
    let started = std::time::Instant::now();
    let mut state = Progress {
        bytes_done: 0,
        bytes_total: plan.fetch_bytes,
        files_done: plan.keep.len(),
        files_total: manifest.files.len(),
    };
    if progress(&state) == Control::Stop {
        return Err(UpdateError::Stopped);
    }

    // Entries taken from the installation first: they are the cheap ones, and
    // doing them before the network means an interrupted preparation has
    // already made progress that survives.
    for entry in &plan.take {
        copy_into_tree(&layout.install().join(&entry.path), &tree.join(&entry.path))?;
        state.files_done += 1;
        if progress(&state) == Control::Stop {
            return Err(UpdateError::Stopped);
        }
    }

    // digest → a path in the tree that already holds those bytes. Two paths
    // with one digest are one download (the bundle does carry duplicates:
    // localisation stubs, repeated plists).
    let mut placed: HashMap<&str, PathBuf> = HashMap::new();
    for entry in &plan.fetch {
        let target = tree.join(&entry.path);
        if let Some(source) = placed.get(entry.sha256.as_str()) {
            copy_into_tree(source, &target)?;
        } else {
            let part = layout.parts().join(&entry.sha256);
            fetch_blob(entry, manifest, transport, &part, &mut state, progress)?;
            place(&part, &target)?;
            placed.insert(entry.sha256.as_str(), target);
        }
        state.files_done += 1;
        if progress(&state) == Control::Stop {
            return Err(UpdateError::Stopped);
        }
    }

    for entry in &manifest.files {
        set_executable(&tree.join(&entry.path), entry.executable)?;
    }
    for link in &manifest.links {
        create_link(&tree, link)?;
    }

    verify_tree(manifest, &tree)?;

    // Everything in parts/ has been placed. Clearing it keeps a stale body
    // from surviving into the next release, where it would be neither used nor
    // noticed — just disk.
    let _ = fs::remove_dir_all(layout.parts());

    let ready = Ready {
        version: manifest.version,
        platform: manifest.platform,
        channel: manifest.channel,
        built_at: unix_now(),
        files: manifest.files.len(),
        links: manifest.links.len(),
    };
    let bytes = serde_json::to_vec_pretty(&ready)
        .map_err(|e| UpdateError::Incomplete(format!("serialising the readiness flag: {e}")))?;
    write_atomic(&layout.ready_file(), &bytes)?;
    log::info!(
        target: "updater",
        "prepared {} in {} ms: {} files, {} links, {} bytes fetched",
        ready.version,
        started.elapsed().as_millis(),
        ready.files,
        ready.links,
        state.bytes_done
    );
    Ok(ready)
}

/// Fetch one blob into `part`, resuming whatever is already there.
///
/// The digest is checked over the finished file, never over what arrived in one
/// response: a body assembled from three interrupted attempts is correct or it
/// is not, and only the whole file can answer that.
fn fetch_blob(
    entry: &FileEntry,
    manifest: &Manifest,
    transport: &dyn UpdateTransport,
    part: &Path,
    state: &mut Progress,
    progress: &mut dyn FnMut(&Progress) -> Control,
) -> Result<(), UpdateError> {
    let url = entry.url(&manifest.base_url);
    // Bytes of this entry already counted into bytes_done, so a retry that
    // rewinds the file does not make the progress bar go backwards past what
    // other files contributed.
    let base_done = state.bytes_done;
    let mut last_digest: Option<String> = None;

    for attempt in 1..=ATTEMPTS {
        let have = fs::metadata(part).map(|m| m.len()).unwrap_or(0);
        if have == entry.size {
            // Already whole from an earlier run — nothing to fetch, just check.
            match digest_of(part) {
                Ok(d) if d == entry.sha256 => return Ok(()),
                Ok(d) => {
                    last_digest = Some(d);
                    let _ = fs::remove_file(part);
                    continue;
                }
                Err(e) => return Err(e),
            }
        }

        let body = match transport.open(&url, have) {
            Ok(body) => body,
            Err(e) if e.is_retryable() && attempt < ATTEMPTS => continue,
            Err(e) => return Err(e.into()),
        };

        let mut file = fs::OpenOptions::new()
            .create(true)
            // Not truncate: the point of this file is what is already in it.
            // The length is set explicitly below, to wherever the body that
            // actually arrived begins.
            .truncate(false)
            .write(true)
            .read(true)
            .open(part)
            .map_err(|e| UpdateError::io(format!("opening {}", part.display()), e))?;

        // ⚠️ The body does not necessarily start where we asked. A server that
        // ignores `Range` answers 200 with the whole object, and appending
        // that to a partial file yields a file that is too long — corruption
        // that a retry repeats rather than repairs. So the file is cut back to
        // wherever the body actually begins.
        file.set_len(body.offset)
            .map_err(|e| UpdateError::io(format!("truncating {}", part.display()), e))?;
        file.seek(SeekFrom::Start(body.offset))
            .map_err(|e| UpdateError::io(format!("seeking {}", part.display()), e))?;
        // Bytes already on disk count as done — and if the server ignored the
        // range and we are starting over, the bar honestly moves back.
        state.bytes_done = base_done + body.offset;

        let outcome = pump(body.read, &mut file, entry, state, base_done, progress);
        file.flush()
            .map_err(|e| UpdateError::io(format!("flushing {}", part.display()), e))?;
        drop(file);
        match outcome {
            Ok(()) => {}
            Err(UpdateError::Transport(e)) if e.is_retryable() && attempt < ATTEMPTS => continue,
            Err(e) => return Err(e),
        }

        let written = fs::metadata(part)
            .map(|m| m.len())
            .map_err(|e| UpdateError::io(format!("stat {}", part.display()), e))?;
        if written < entry.size {
            // The body ended early without the transport calling it an error.
            // Same treatment as an interruption: resume next attempt.
            continue;
        }
        if written > entry.size {
            // Longer than the manifest says: not something to resume from.
            let _ = fs::remove_file(part);
            continue;
        }

        let got = digest_of(part)?;
        if got == entry.sha256 {
            state.bytes_done = base_done + entry.size;
            return Ok(());
        }
        last_digest = Some(got);
        let _ = fs::remove_file(part);
    }

    log::warn!(
        target: "updater",
        "gave up fetching {} after {ATTEMPTS} attempts",
        entry.path
    );
    Err(UpdateError::DigestMismatch {
        path: entry.path.clone(),
        expected: entry.sha256.clone(),
        got: last_digest.unwrap_or_else(|| "never completed".to_owned()),
    })
}

/// Copy a body into the part file, reporting progress and honouring "stop".
fn pump(
    mut body: Box<dyn Read + Send>,
    file: &mut File,
    entry: &FileEntry,
    state: &mut Progress,
    base_done: u64,
    progress: &mut dyn FnMut(&Progress) -> Control,
) -> Result<(), UpdateError> {
    let mut buf = vec![0u8; CHUNK];
    loop {
        let n = match body.read(&mut buf) {
            Ok(0) => return Ok(()),
            Ok(n) => n,
            Err(e) => {
                return Err(UpdateError::Transport(
                    crate::transport::TransportError::Interrupted {
                        url: entry.path.clone(),
                        why: e.to_string(),
                    },
                ))
            }
        };
        file.write_all(&buf[..n])
            .map_err(|e| UpdateError::io("writing a downloaded body", e))?;
        state.bytes_done += n as u64;
        // A body longer than the manifest promised stops here rather than
        // filling the disk: `size` is not a security property, but it is a
        // bound, and honouring it costs one comparison per chunk.
        if state.bytes_done > base_done + entry.size {
            return Ok(());
        }
        if progress(state) == Control::Stop {
            return Err(UpdateError::Stopped);
        }
    }
}

/// Remove everything the manifest does not describe, and every directory left
/// empty by doing so.
///
/// Called before planning, so the plan's `keep` bucket sees only paths that
/// belong to this release.
fn prune(tree: &Path, dir: &Path, described: &HashSet<&str>) -> Result<(), UpdateError> {
    let entries = match fs::read_dir(dir) {
        Ok(entries) => entries,
        // No tree yet: nothing to prune, and `prepare_dirs` has already made
        // the directory it will be built in.
        Err(_) => return Ok(()),
    };
    for entry in entries {
        let entry = entry.map_err(|e| UpdateError::io(format!("reading {}", dir.display()), e))?;
        let path = entry.path();
        let relative = match path.strip_prefix(tree) {
            Ok(rel) => rel.to_string_lossy().replace('\\', "/"),
            Err(_) => continue,
        };
        let meta = fs::symlink_metadata(&path)
            .map_err(|e| UpdateError::io(format!("stat {}", path.display()), e))?;

        // A directory is never described by a manifest, so it survives exactly
        // as long as something inside it does.
        if meta.is_dir() && !meta.is_symlink() {
            prune(tree, &path, described)?;
            if fs::read_dir(&path)
                .map(|mut d| d.next().is_none())
                .unwrap_or(false)
            {
                let _ = fs::remove_dir(&path);
            }
            continue;
        }
        if !described.contains(relative.as_str()) {
            remove_any(&path)?;
        }
    }
    Ok(())
}

/// Remove whatever is at `path`, whichever kind of thing it is.
fn remove_any(path: &Path) -> Result<(), UpdateError> {
    let meta = match fs::symlink_metadata(path) {
        Ok(meta) => meta,
        Err(_) => return Ok(()),
    };
    let result = if meta.is_dir() && !meta.is_symlink() {
        fs::remove_dir_all(path)
    } else {
        fs::remove_file(path)
    };
    result.map_err(|e| UpdateError::io(format!("removing {}", path.display()), e))
}

/// Make room at `target`: its parent exists, and nothing of the wrong kind
/// sits where a file has to go.
///
/// The wrong kind is not hypothetical — a previous release can have had a
/// directory or a symlink at a path this one calls a file, and both `rename`
/// and `copy` fail against a directory.
fn clear_target(target: &Path) -> Result<(), UpdateError> {
    if let Some(parent) = target.parent() {
        fs::create_dir_all(parent)
            .map_err(|e| UpdateError::io(format!("creating {}", parent.display()), e))?;
    }
    match fs::symlink_metadata(target) {
        Ok(meta) if meta.is_dir() && !meta.is_symlink() => remove_any(target),
        Ok(meta) if meta.is_symlink() => remove_any(target),
        _ => Ok(()),
    }
}

/// Move a verified body to its place in the tree.
fn place(part: &Path, target: &Path) -> Result<(), UpdateError> {
    clear_target(target)?;
    fs::rename(part, target)
        .map_err(|e| UpdateError::io(format!("placing {}", target.display()), e))
}

fn copy_into_tree(source: &Path, target: &Path) -> Result<(), UpdateError> {
    clear_target(target)?;
    fs::copy(source, target)
        .map(|_| ())
        .map_err(|e| UpdateError::io(format!("copying {}", source.display()), e))
}

/// Make the file's execute bit exactly what the manifest says.
///
/// Exactly, in both directions: a file copied from an installation whose modes
/// have been fiddled with comes out of here the way the release describes it.
/// The staging tree is defined by the manifest, not by what happened to be on
/// disk.
#[cfg(unix)]
fn set_executable(path: &Path, executable: bool) -> Result<(), UpdateError> {
    use std::os::unix::fs::PermissionsExt;
    let mode = if executable { 0o755 } else { 0o644 };
    fs::set_permissions(path, fs::Permissions::from_mode(mode))
        .map_err(|e| UpdateError::io(format!("setting mode on {}", path.display()), e))
}

#[cfg(not(unix))]
fn set_executable(_path: &Path, _executable: bool) -> Result<(), UpdateError> {
    // Windows has no execute bit; `.exe` is a name, not a permission.
    Ok(())
}

#[cfg(unix)]
fn create_link(tree: &Path, link: &LinkEntry) -> Result<(), UpdateError> {
    let path = tree.join(&link.path);
    // Recreated rather than checked: `symlink` refuses an existing path, and a
    // link left from an earlier attempt may point somewhere else.
    clear_target(&path)?;
    let _ = fs::remove_file(&path);
    std::os::unix::fs::symlink(&link.target, &path)
        .map_err(|e| UpdateError::io(format!("linking {}", path.display()), e))
}

#[cfg(not(unix))]
fn create_link(_tree: &Path, link: &LinkEntry) -> Result<(), UpdateError> {
    // Creating a symlink on Windows needs SeCreateSymbolicLinkPrivilege, which
    // means an elevation prompt — and the whole installation model exists to
    // avoid those (plan 38 §0.1). Windows releases carry no links, so a
    // manifest that asks for one is a build fault, reported as such.
    Err(UpdateError::LinksUnsupported {
        path: link.path.clone(),
    })
}

/// The last check before a tree may be called ready.
///
/// Every entry, by size; every link, by target. Not a re-hash of 245 MB: each
/// body was already verified by digest when it arrived, and each taken file was
/// verified against the same digest when the plan chose it. What is left to
/// prove is that the tree is *complete* — the failure this catches is a file
/// that never got written, which is precisely what a half-finished build looks
/// like from the outside.
fn verify_tree(manifest: &Manifest, tree: &Path) -> Result<(), UpdateError> {
    for entry in &manifest.files {
        let path = tree.join(&entry.path);
        let meta = fs::symlink_metadata(&path)
            .map_err(|_| UpdateError::Incomplete(format!("{} is missing", entry.path)))?;
        if !meta.is_file() {
            return Err(UpdateError::Incomplete(format!(
                "{} is not a regular file",
                entry.path
            )));
        }
        if meta.len() != entry.size {
            return Err(UpdateError::Incomplete(format!(
                "{} is {} bytes, manifest says {}",
                entry.path,
                meta.len(),
                entry.size
            )));
        }
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let executable = meta.permissions().mode() & 0o111 != 0;
            if executable != entry.executable {
                return Err(UpdateError::Incomplete(format!(
                    "{} executable={executable}, manifest says {}",
                    entry.path, entry.executable
                )));
            }
        }
    }
    for link in &manifest.links {
        let path = tree.join(&link.path);
        let meta = fs::symlink_metadata(&path)
            .map_err(|_| UpdateError::Incomplete(format!("link {} is missing", link.path)))?;
        if !meta.is_symlink() {
            return Err(UpdateError::Incomplete(format!(
                "{} is not a symlink",
                link.path
            )));
        }
        let target = fs::read_link(&path)
            .map_err(|e| UpdateError::io(format!("reading link {}", path.display()), e))?;
        if target.to_string_lossy() != link.target {
            return Err(UpdateError::Incomplete(format!(
                "link {} points at {}, manifest says {}",
                link.path,
                target.display(),
                link.target
            )));
        }
    }
    Ok(())
}

fn digest_of(path: &Path) -> Result<String, UpdateError> {
    let mut file =
        File::open(path).map_err(|e| UpdateError::io(format!("opening {}", path.display()), e))?;
    let mut hasher = Sha256::new();
    let mut buf = vec![0u8; CHUNK];
    loop {
        let n = file
            .read(&mut buf)
            .map_err(|e| UpdateError::io(format!("reading {}", path.display()), e))?;
        if n == 0 {
            break;
        }
        hasher.update(&buf[..n]);
    }
    Ok(hex::encode(hasher.finalize()))
}

fn write_atomic(path: &Path, bytes: &[u8]) -> Result<(), UpdateError> {
    let tmp = path.with_extension("tmp");
    fs::write(&tmp, bytes).map_err(|e| UpdateError::io(format!("writing {}", tmp.display()), e))?;
    fs::rename(&tmp, path).map_err(|e| UpdateError::io(format!("placing {}", path.display()), e))
}

pub(crate) fn unix_now() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0)
}

#[cfg(test)]
mod tests_support {
    //! Fixtures shared by this file's test modules.
    use super::*;
    use crate::manifest::{Channel, Platform};
    use crate::scan::scan_tree;
    use crate::transport::fake::FakeChannel;
    use std::sync::atomic::{AtomicU32, Ordering};

    pub(super) const BASE: &str = "https://update.nekkochat.com";

    /// A scratch directory that removes itself. No `tempfile` dependency for
    /// something this crate needs in tests only.
    pub(super) struct Tmp(PathBuf);

    impl Tmp {
        pub(super) fn new(label: &str) -> Tmp {
            static N: AtomicU32 = AtomicU32::new(0);
            let path = std::env::temp_dir().join(format!(
                "nekko-updater-{label}-{}-{}",
                std::process::id(),
                N.fetch_add(1, Ordering::Relaxed)
            ));
            let _ = fs::remove_dir_all(&path);
            fs::create_dir_all(&path).unwrap();
            Tmp(path)
        }

        pub(super) fn join(&self, rel: &str) -> PathBuf {
            self.0.join(rel)
        }
    }

    impl Drop for Tmp {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.0);
        }
    }

    pub(super) fn write(path: &Path, bytes: &[u8]) {
        fs::create_dir_all(path.parent().unwrap()).unwrap();
        fs::write(path, bytes).unwrap();
    }

    #[cfg(unix)]
    pub(super) fn chmod(path: &Path, mode: u32) {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(path, fs::Permissions::from_mode(mode)).unwrap();
    }

    /// A tree shaped like the real bundle: a program, a framework with the
    /// `Versions/Current` pair, a file big enough to be cut in half mid-flight,
    /// and two paths holding identical bytes.
    pub(super) fn fixture(root: &Path) {
        write(&root.join("Contents/MacOS/nekko"), b"the program");
        write(&root.join("Contents/Info.plist"), b"<plist/>");
        write(&root.join("Contents/Resources/dup-a.txt"), b"twice over");
        write(&root.join("Contents/Resources/dup-b.txt"), b"twice over");
        write(
            &root.join("Contents/Resources/big.bin"),
            &vec![7u8; 200 * 1024],
        );
        write(
            &root.join("Contents/Frameworks/Foo.framework/Versions/A/Foo"),
            b"framework",
        );
        #[cfg(unix)]
        {
            chmod(&root.join("Contents/MacOS/nekko"), 0o755);
            let fw = root.join("Contents/Frameworks/Foo.framework");
            std::os::unix::fs::symlink("A", fw.join("Versions/Current")).unwrap();
            std::os::unix::fs::symlink("Versions/Current/Foo", fw.join("Foo")).unwrap();
        }
    }

    pub(super) fn manifest_of(root: &Path, version: &str) -> Manifest {
        let scanned = scan_tree(root).unwrap();
        let m = Manifest {
            version: Version::parse(version).unwrap(),
            channel: Channel::Stable,
            platform: Platform::Macos,
            minimum_version: Version::parse("0.1.0").unwrap(),
            published_at: 1_757_563_200,
            base_url: BASE.to_owned(),
            files: scanned.files,
            links: scanned.links,
        };
        // Everything below assumes a manifest that would have been accepted.
        m.validate().unwrap();
        m
    }

    /// A channel serving exactly what the manifest promises, from `root`.
    pub(super) fn channel_of(manifest: &Manifest, root: &Path) -> FakeChannel {
        let mut chan = FakeChannel::default();
        for entry in &manifest.files {
            chan.objects.insert(
                entry.url(BASE),
                fs::read(root.join(&entry.path)).unwrap_or_default(),
            );
        }
        chan
    }

    pub(super) fn go(
        manifest: &Manifest,
        layout: &Layout,
        chan: &FakeChannel,
    ) -> Result<Ready, UpdateError> {
        prepare(manifest, layout, chan, &mut |_| Control::Continue)
    }

    /// An installation and a staging directory beside it, on one volume.
    pub(super) fn layout_in(tmp: &Tmp) -> Layout {
        let install = tmp.join("install");
        fs::create_dir_all(&install).unwrap();
        Layout::new(install, tmp.join("staging"))
    }

    #[test]
    fn the_tree_it_builds_is_the_tree_the_scanner_described() {
        // The round trip that makes the two halves of the format one thing:
        // scan a bundle, publish it, rebuild it from nothing but the manifest
        // and the channel, scan the result — and get the same description,
        // digests, sizes, execute bits and symlink targets included.
        let tmp = Tmp::new("roundtrip");
        let source = tmp.join("source");
        fixture(&source);
        let manifest = manifest_of(&source, "0.2.0");
        let chan = channel_of(&manifest, &source);
        let layout = layout_in(&tmp);

        let ready = go(&manifest, &layout, &chan).unwrap();
        assert_eq!(ready.version, Version::parse("0.2.0").unwrap());
        assert_eq!(ready.files, manifest.files.len());
        assert_eq!(ready.links, manifest.links.len());

        let before = scan_tree(&source).unwrap();
        let after = scan_tree(&layout.tree()).unwrap();
        assert_eq!(before, after);
    }

    #[test]
    fn an_unchanged_file_is_taken_from_the_installation_and_never_fetched() {
        // The whole reason for a per-file manifest: the heavy files that do not
        // change between builds are not on the wire at all.
        let tmp = Tmp::new("reuse");
        let source = tmp.join("source");
        fixture(&source);
        let layout = layout_in(&tmp);
        // The installation is the previous release: same tree, older program.
        fixture(layout.install());
        write(
            &source.join("Contents/MacOS/nekko"),
            b"the program, rebuilt",
        );
        #[cfg(unix)]
        chmod(&source.join("Contents/MacOS/nekko"), 0o755);

        let manifest = manifest_of(&source, "0.2.0");
        let chan = channel_of(&manifest, &source);
        go(&manifest, &layout, &chan).unwrap();

        let opened: Vec<String> = chan.opens().into_iter().map(|(u, _)| u).collect();
        assert_eq!(
            opened.len(),
            1,
            "only the changed file should be fetched, got {opened:?}"
        );
        let program = manifest
            .files
            .iter()
            .find(|f| f.path.ends_with("MacOS/nekko"))
            .unwrap();
        assert_eq!(opened[0], program.url(BASE));
        assert_eq!(
            fs::read(layout.tree().join("Contents/Resources/big.bin")).unwrap(),
            vec![7u8; 200 * 1024],
            "the 200 KiB file should have come from the installation"
        );
    }

    #[test]
    #[cfg(unix)]
    fn the_program_comes_out_executable_and_the_rest_does_not() {
        // HTTP carries no file mode, so this bit can only come from the
        // manifest — and a bundle whose binary is not executable does not
        // start, however correct its bytes are.
        use std::os::unix::fs::PermissionsExt;
        let tmp = Tmp::new("exec");
        let source = tmp.join("source");
        fixture(&source);
        let manifest = manifest_of(&source, "0.2.0");
        let chan = channel_of(&manifest, &source);
        let layout = layout_in(&tmp);

        go(&manifest, &layout, &chan).unwrap();
        let mode = |rel: &str| {
            fs::metadata(layout.tree().join(rel))
                .unwrap()
                .permissions()
                .mode()
                & 0o777
        };
        assert_eq!(mode("Contents/MacOS/nekko"), 0o755);
        assert_eq!(mode("Contents/Info.plist"), 0o644);
    }

    #[test]
    #[cfg(unix)]
    fn an_installed_file_with_a_fiddled_mode_comes_out_as_the_release_says() {
        // The tree is defined by the manifest, not by what happened to be on
        // disk: a file taken from the installation gets the release's bit.
        use std::os::unix::fs::PermissionsExt;
        let tmp = Tmp::new("mode");
        let source = tmp.join("source");
        fixture(&source);
        let manifest = manifest_of(&source, "0.2.0");
        let chan = channel_of(&manifest, &source);
        let layout = layout_in(&tmp);
        fixture(layout.install());
        chmod(&layout.install().join("Contents/Info.plist"), 0o777);

        go(&manifest, &layout, &chan).unwrap();
        let mode = fs::metadata(layout.tree().join("Contents/Info.plist"))
            .unwrap()
            .permissions()
            .mode()
            & 0o777;
        assert_eq!(mode, 0o644);
    }

    #[test]
    #[cfg(unix)]
    fn symlinks_are_created_as_links_not_as_copies() {
        let tmp = Tmp::new("links");
        let source = tmp.join("source");
        fixture(&source);
        let manifest = manifest_of(&source, "0.2.0");
        assert_eq!(manifest.links.len(), 2, "the fixture carries two links");
        let chan = channel_of(&manifest, &source);
        let layout = layout_in(&tmp);

        go(&manifest, &layout, &chan).unwrap();
        let link = layout
            .tree()
            .join("Contents/Frameworks/Foo.framework/Versions/Current");
        assert!(fs::symlink_metadata(&link).unwrap().is_symlink());
        assert_eq!(fs::read_link(&link).unwrap().to_str().unwrap(), "A");
        // And nothing fetched a link: a link has no body.
        assert!(chan
            .opens()
            .iter()
            .all(|(u, _)| !u.contains("Versions/Current")));
    }

    #[test]
    fn a_body_cut_in_half_resumes_where_it_stopped() {
        let tmp = Tmp::new("resume");
        let source = tmp.join("source");
        fixture(&source);
        let manifest = manifest_of(&source, "0.2.0");
        let mut chan = channel_of(&manifest, &source);
        chan.cut_after = Some(100 * 1024);
        let layout = layout_in(&tmp);

        go(&manifest, &layout, &chan).unwrap();

        let big = manifest
            .files
            .iter()
            .find(|f| f.path.ends_with("big.bin"))
            .unwrap();
        let offsets: Vec<u64> = chan
            .opens()
            .into_iter()
            .filter(|(u, _)| *u == big.url(BASE))
            .map(|(_, from)| from)
            .collect();
        assert_eq!(
            offsets,
            vec![0, 100 * 1024],
            "the second attempt must ask for the rest, not the whole file again"
        );
        assert_eq!(
            fs::read(layout.tree().join("Contents/Resources/big.bin")).unwrap(),
            vec![7u8; 200 * 1024]
        );
    }

    #[test]
    fn a_channel_that_ignores_range_does_not_produce_a_file_that_is_too_long() {
        // The silent failure the `offset` field exists for: appending a whole
        // body to a partial file. The digest would then never match, and
        // retrying would append again.
        let tmp = Tmp::new("norange");
        let source = tmp.join("source");
        fixture(&source);
        let manifest = manifest_of(&source, "0.2.0");
        let mut chan = channel_of(&manifest, &source);
        chan.ignore_range = true;
        let layout = layout_in(&tmp);
        layout.prepare_dirs().unwrap();

        // Half a body left over from an earlier attempt.
        let big = manifest
            .files
            .iter()
            .find(|f| f.path.ends_with("big.bin"))
            .unwrap();
        write(&layout.parts().join(&big.sha256), &vec![7u8; 100 * 1024]);

        go(&manifest, &layout, &chan).unwrap();
        let built = layout.tree().join("Contents/Resources/big.bin");
        assert_eq!(fs::metadata(&built).unwrap().len(), big.size);
        assert_eq!(fs::read(&built).unwrap(), vec![7u8; 200 * 1024]);
    }

    #[test]
    fn a_blob_whose_bytes_do_not_match_its_name_stops_the_update() {
        let tmp = Tmp::new("mangled");
        let source = tmp.join("source");
        fixture(&source);
        let manifest = manifest_of(&source, "0.2.0");
        let mut chan = channel_of(&manifest, &source);
        let program = manifest
            .files
            .iter()
            .find(|f| f.path.ends_with("MacOS/nekko"))
            .unwrap();
        chan.objects
            .insert(program.url(BASE), b"something else entirely".to_vec());
        let layout = layout_in(&tmp);

        let err = go(&manifest, &layout, &chan).unwrap_err();
        match err {
            UpdateError::DigestMismatch { path, .. } => assert_eq!(path, program.path),
            other => panic!("expected a digest mismatch, got {other}"),
        }
        assert!(
            read_ready(&layout).is_none(),
            "nothing may be called ready after a failed preparation"
        );
    }

    #[test]
    fn a_corrupt_installed_file_is_replaced_rather_than_carried_forward() {
        // Same length, different bytes — the case a size comparison misses and
        // the case the per-file digest was chosen for (plan 38 §0.3).
        let tmp = Tmp::new("rot");
        let source = tmp.join("source");
        fixture(&source);
        let manifest = manifest_of(&source, "0.2.0");
        let chan = channel_of(&manifest, &source);
        let layout = layout_in(&tmp);
        fixture(layout.install());
        write(&layout.install().join("Contents/Info.plist"), b"<plist?!>");

        go(&manifest, &layout, &chan).unwrap();
        assert_eq!(
            fs::read(layout.tree().join("Contents/Info.plist")).unwrap(),
            b"<plist/>"
        );
        let plist = manifest
            .files
            .iter()
            .find(|f| f.path.ends_with("Info.plist"))
            .unwrap();
        assert!(
            chan.opens().iter().any(|(u, _)| *u == plist.url(BASE)),
            "the rotted file must be re-fetched, not copied"
        );
    }

    #[test]
    fn stopping_keeps_what_arrived_and_the_next_attempt_resumes_from_it() {
        // An update must never be the reason a call stutters (plan 38 §3.5),
        // so "stop" has to be cheap to say and cheap to recover from.
        let tmp = Tmp::new("stop");
        let source = tmp.join("source");
        fixture(&source);
        let manifest = manifest_of(&source, "0.2.0");
        let chan = channel_of(&manifest, &source);
        let layout = layout_in(&tmp);

        // Stop in the middle of the 200 KiB file: stopping on a small one
        // would leave a part that is already whole, which is a different
        // property (proved below by it not being fetched twice).
        let err = prepare(&manifest, &layout, &chan, &mut |p| {
            if p.bytes_done > 100 * 1024 {
                Control::Stop
            } else {
                Control::Continue
            }
        })
        .unwrap_err();
        assert!(matches!(err, UpdateError::Stopped), "got {err}");
        assert!(read_ready(&layout).is_none());

        let kept: u64 = fs::read_dir(layout.parts())
            .unwrap()
            .map(|e| e.unwrap().metadata().unwrap().len())
            .sum();
        assert!(kept > 0, "what was downloaded must survive the stop");

        let first_run = chan.opens();

        // Second attempt: finishes, asks for the REST of the big file, and does
        // not re-fetch the small ones whose bodies are already whole on disk.
        go(&manifest, &layout, &chan).unwrap();
        let big = manifest
            .files
            .iter()
            .find(|f| f.path.ends_with("big.bin"))
            .unwrap();
        let resumed: Vec<u64> = chan
            .opens()
            .into_iter()
            .skip(first_run.len())
            .filter(|(u, _)| *u == big.url(BASE))
            .map(|(_, from)| from)
            .collect();
        assert_eq!(
            resumed.len(),
            1,
            "the big file should be asked for once more, got {resumed:?}"
        );
        assert!(
            resumed[0] >= 100 * 1024,
            "and from where it stopped, not from zero: {resumed:?}"
        );
        for (url, _) in &first_run {
            if *url == big.url(BASE) {
                continue;
            }
            let total = chan.opens().iter().filter(|(u, _)| u == url).count();
            assert_eq!(total, 1, "{url} was already whole and was fetched again");
        }
        let after = scan_tree(&layout.tree()).unwrap();
        assert_eq!(after, scan_tree(&source).unwrap());
    }

    #[test]
    fn preparing_a_version_that_is_already_staged_does_nothing() {
        let tmp = Tmp::new("idempotent");
        let source = tmp.join("source");
        fixture(&source);
        let manifest = manifest_of(&source, "0.2.0");
        let chan = channel_of(&manifest, &source);
        let layout = layout_in(&tmp);

        let first = go(&manifest, &layout, &chan).unwrap();
        let opens = chan.opens().len();
        let second = go(&manifest, &layout, &chan).unwrap();
        assert_eq!(first, second);
        assert_eq!(
            chan.opens().len(),
            opens,
            "the second run must fetch nothing"
        );
    }

    #[test]
    fn a_tree_left_over_from_an_interrupted_build_is_rebuilt_not_patched() {
        // Otherwise "the tree holds the manifest and nothing else" would need a
        // diff to be true — and a stray file inside a macOS bundle breaks its
        // signature.
        let tmp = Tmp::new("leftover");
        let source = tmp.join("source");
        fixture(&source);
        let manifest = manifest_of(&source, "0.2.0");
        let chan = channel_of(&manifest, &source);
        let layout = layout_in(&tmp);
        layout.prepare_dirs().unwrap();
        write(&layout.tree().join("Contents/stray.txt"), b"from before");

        go(&manifest, &layout, &chan).unwrap();
        assert!(!layout.tree().join("Contents/stray.txt").exists());
    }

    #[test]
    fn a_blob_the_channel_does_not_have_is_reported_once_not_hammered() {
        let tmp = Tmp::new("missing");
        let source = tmp.join("source");
        fixture(&source);
        let manifest = manifest_of(&source, "0.2.0");
        let mut chan = channel_of(&manifest, &source);
        let program = manifest
            .files
            .iter()
            .find(|f| f.path.ends_with("MacOS/nekko"))
            .unwrap();
        chan.objects.remove(&program.url(BASE));
        let layout = layout_in(&tmp);

        let err = go(&manifest, &layout, &chan).unwrap_err();
        match err {
            UpdateError::Transport(e) => assert!(!e.is_retryable(), "404 is not retryable"),
            other => panic!("expected a transport error, got {other}"),
        }
        let tries = chan
            .opens()
            .iter()
            .filter(|(u, _)| *u == program.url(BASE))
            .count();
        assert_eq!(tries, 1, "a missing file must not be asked for again");
    }

    #[test]
    fn two_paths_with_the_same_bytes_are_downloaded_once() {
        let tmp = Tmp::new("dup");
        let source = tmp.join("source");
        fixture(&source);
        let manifest = manifest_of(&source, "0.2.0");
        let chan = channel_of(&manifest, &source);
        let layout = layout_in(&tmp);

        go(&manifest, &layout, &chan).unwrap();
        let dup = manifest
            .files
            .iter()
            .find(|f| f.path.ends_with("dup-a.txt"))
            .unwrap();
        assert_eq!(
            chan.opens()
                .iter()
                .filter(|(u, _)| *u == dup.url(BASE))
                .count(),
            1
        );
        assert_eq!(
            fs::read(layout.tree().join("Contents/Resources/dup-b.txt")).unwrap(),
            b"twice over"
        );
    }

    #[test]
    #[cfg(unix)]
    fn staging_on_another_volume_is_refused_before_anything_is_downloaded() {
        // The swap is a rename, and rename does not cross volumes. /dev is a
        // different filesystem on every unix, which makes this testable
        // without mounting anything.
        let tmp = Tmp::new("volume");
        let layout = Layout::new("/dev", tmp.join("staging"));
        match layout.prepare_dirs().unwrap_err() {
            UpdateError::DifferentVolume { .. } => {}
            other => panic!("expected a volume refusal, got {other}"),
        }
    }

    #[test]
    fn the_readiness_flag_says_what_is_ready() {
        let tmp = Tmp::new("flag");
        let source = tmp.join("source");
        fixture(&source);
        let manifest = manifest_of(&source, "0.3.1");
        let chan = channel_of(&manifest, &source);
        let layout = layout_in(&tmp);

        let ready = go(&manifest, &layout, &chan).unwrap();
        let on_disk = read_ready(&layout).expect("the flag must be readable back");
        assert_eq!(on_disk, ready);
        assert_eq!(on_disk.version, Version::parse("0.3.1").unwrap());
        assert_eq!(on_disk.platform, Platform::Macos);
        assert!(on_disk.built_at > 0);
        // And the flag lives beside the tree, never inside it.
        assert!(!layout.ready_file().starts_with(layout.tree()));
    }

    #[test]
    fn progress_ends_where_it_said_it_would() {
        let tmp = Tmp::new("progress");
        let source = tmp.join("source");
        fixture(&source);
        let manifest = manifest_of(&source, "0.2.0");
        let chan = channel_of(&manifest, &source);
        let layout = layout_in(&tmp);

        let mut last = Progress {
            bytes_done: 0,
            bytes_total: 0,
            files_done: 0,
            files_total: 0,
        };
        prepare(&manifest, &layout, &chan, &mut |p| {
            last = *p;
            Control::Continue
        })
        .unwrap();
        assert_eq!(last.files_done, manifest.files.len());
        assert_eq!(last.bytes_done, last.bytes_total);
    }
}

#[cfg(test)]
mod resume_tests {
    //! Tests for the part of the design a failing test found: the staging tree
    //! is itself the resume state, so an interrupted preparation does not
    //! re-download what it already built.

    use super::tests_support::*;
    use super::*;
    use crate::scan::scan_tree;

    #[test]
    fn the_plan_says_where_every_entry_comes_from() {
        let tmp = Tmp::new("buckets");
        let source = tmp.join("source");
        fixture(&source);
        let manifest = manifest_of(&source, "0.2.0");
        let layout = layout_in(&tmp);
        layout.prepare_dirs().unwrap();

        // One entry already correct in the tree, one already correct in the
        // installation, one present in the tree but with the wrong bytes.
        write(
            &layout.tree().join("Contents/Info.plist"),
            &fs::read(source.join("Contents/Info.plist")).unwrap(),
        );
        write(
            &layout.install().join("Contents/Resources/dup-a.txt"),
            &fs::read(source.join("Contents/Resources/dup-a.txt")).unwrap(),
        );
        write(&layout.tree().join("Contents/MacOS/nekko"), b"stale bytes");

        let plan = plan(&manifest, &layout);
        let paths = |v: &Vec<FileEntry>| v.iter().map(|e| e.path.clone()).collect::<Vec<_>>();
        assert_eq!(paths(&plan.keep), vec!["Contents/Info.plist".to_owned()]);
        assert_eq!(
            paths(&plan.take),
            vec!["Contents/Resources/dup-a.txt".to_owned()]
        );
        assert!(
            paths(&plan.fetch).contains(&"Contents/MacOS/nekko".to_owned()),
            "a staged file with the wrong bytes must not be kept: {:?}",
            paths(&plan.fetch)
        );
    }

    #[test]
    fn an_empty_directory_left_behind_is_pruned() {
        // A manifest cannot describe a directory, so one that no file needs any
        // more must not survive into a bundle whose signature covers it.
        let tmp = Tmp::new("emptydir");
        let source = tmp.join("source");
        fixture(&source);
        let manifest = manifest_of(&source, "0.2.0");
        let chan = channel_of(&manifest, &source);
        let layout = layout_in(&tmp);
        layout.prepare_dirs().unwrap();
        fs::create_dir_all(layout.tree().join("Contents/Gone/Deeper")).unwrap();

        go(&manifest, &layout, &chan).unwrap();
        assert!(!layout.tree().join("Contents/Gone").exists());
        assert_eq!(
            scan_tree(&layout.tree()).unwrap(),
            scan_tree(&source).unwrap()
        );
    }

    #[test]
    fn a_directory_where_this_release_wants_a_file_is_cleared() {
        // Previous releases can have had a directory at a path this one calls a
        // file. Both `rename` and `copy` fail against a directory, so the
        // update would stop with an io error that says nothing.
        let tmp = Tmp::new("conflict");
        let source = tmp.join("source");
        fixture(&source);
        let manifest = manifest_of(&source, "0.2.0");
        let chan = channel_of(&manifest, &source);
        let layout = layout_in(&tmp);
        layout.prepare_dirs().unwrap();
        fs::create_dir_all(layout.tree().join("Contents/Info.plist")).unwrap();
        write(
            &layout.tree().join("Contents/Info.plist/surprise.txt"),
            b"!",
        );

        go(&manifest, &layout, &chan).unwrap();
        assert_eq!(
            fs::read(layout.tree().join("Contents/Info.plist")).unwrap(),
            b"<plist/>"
        );
    }

    #[test]
    fn a_staged_tree_for_another_version_contributes_what_it_can() {
        // Two releases in a row: the second preparation starts from the first
        // one's tree and only fetches what actually differs between them.
        let tmp = Tmp::new("twoversions");
        let source = tmp.join("source");
        fixture(&source);
        let first = manifest_of(&source, "0.2.0");
        let chan_one = channel_of(&first, &source);
        let layout = layout_in(&tmp);
        go(&first, &layout, &chan_one).unwrap();

        write(&source.join("Contents/MacOS/nekko"), b"the program, again");
        #[cfg(unix)]
        chmod(&source.join("Contents/MacOS/nekko"), 0o755);
        let second = manifest_of(&source, "0.3.0");
        let chan_two = channel_of(&second, &source);
        go(&second, &layout, &chan_two).unwrap();

        let fetched: Vec<String> = chan_two.opens().into_iter().map(|(u, _)| u).collect();
        assert_eq!(
            fetched.len(),
            1,
            "only the changed file should cross the wire: {fetched:?}"
        );
        assert_eq!(read_ready(&layout).unwrap().version, second.version);
        assert_eq!(
            scan_tree(&layout.tree()).unwrap(),
            scan_tree(&source).unwrap()
        );
    }
}
