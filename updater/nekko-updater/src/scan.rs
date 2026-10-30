//! Reading a packaged application off the disk and describing it.
//!
//! This is the generator's half of the format, and it lives in the library
//! rather than in `bin/nekko-manifest.rs` for two reasons. One: the release
//! pipeline and the updater then agree by construction — what [`scan_tree`]
//! records is what [`crate::stage::prepare`] rebuilds, and the crate's own
//! tests can walk that round trip over a real directory instead of asserting
//! against hand-written entries. Two: a manifest is trustworthy only if the
//! bytes the signer signed are the bytes the verifier checks, and the same
//! argument applies one level down — what the signer *describes* had better be
//! produced by the same code that the verifier's consumer consumes.

use std::fs;
use std::path::Path;

use sha2::{Digest, Sha256};
use thiserror::Error;

use crate::manifest::{FileEntry, LinkEntry};

/// A tree, as the manifest will describe it.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Scanned {
    pub files: Vec<FileEntry>,
    pub links: Vec<LinkEntry>,
}

/// Why a directory could not be described.
#[derive(Debug, Error, PartialEq, Eq)]
pub enum ScanError {
    #[error("{what}: {why}")]
    Io { what: String, why: String },

    /// A manifest lists files and links and nothing else, so an empty
    /// directory is a piece of the tree that cannot be written down — and an
    /// updater rebuilding from that manifest would simply not create it.
    ///
    /// Refused at release time rather than shipped: our bundles have none
    /// today, and if one appears we want to hear about it from the build and
    /// not from a user whose application will not start.
    #[error(
        "{path} is empty — a manifest cannot describe an empty directory, so the updater would \
         not recreate it. Remove it, or teach the format about directories."
    )]
    EmptyDirectory { path: String },

    #[error("link target of {path} is not valid UTF-8")]
    LinkNotUtf8 { path: String },
}

impl ScanError {
    fn io(what: impl Into<String>, why: std::io::Error) -> Self {
        ScanError::Io {
            what: what.into(),
            why: why.to_string(),
        }
    }
}

/// Walk `root`, hash every regular file, record every symlink.
///
/// Entries come back sorted by path, so two runs over the same tree produce
/// the same manifest — directory order is not guaranteed by any filesystem,
/// and a diff between two releases should show what changed rather than how
/// the disk felt.
///
/// ⚠️ Symlinks are recorded, never followed. A real `nekko.app` holds 24 of
/// them across 990 files — every embedded `.framework` ships the
/// `Versions/Current` pair Apple's bundle layout requires — so a manifest that
/// followed them would list the same bytes twice under two paths, and an
/// updater rebuilding from it would write two real files where the bundle needs
/// a link.
pub fn scan_tree(root: &Path) -> Result<Scanned, ScanError> {
    let mut out = Scanned::default();
    walk(root, root, &mut out)?;
    out.files.sort_by(|a, b| a.path.cmp(&b.path));
    out.links.sort_by(|a, b| a.path.cmp(&b.path));
    Ok(out)
}

fn walk(root: &Path, dir: &Path, out: &mut Scanned) -> Result<(), ScanError> {
    let entries =
        fs::read_dir(dir).map_err(|e| ScanError::io(format!("reading {}", dir.display()), e))?;
    let mut seen = 0usize;
    for entry in entries {
        seen += 1;
        let entry = entry.map_err(|e| ScanError::io(format!("reading {}", dir.display()), e))?;
        let path = entry.path();
        // symlink_metadata, not metadata: the latter follows the link and would
        // report the target's type, which is exactly what must not happen here.
        let meta = fs::symlink_metadata(&path)
            .map_err(|e| ScanError::io(format!("stat {}", path.display()), e))?;
        let relative = relative_to(root, &path)?;

        if meta.file_type().is_symlink() {
            let target = fs::read_link(&path)
                .map_err(|e| ScanError::io(format!("reading link {}", path.display()), e))?;
            let target = target.to_str().ok_or_else(|| ScanError::LinkNotUtf8 {
                path: path.display().to_string(),
            })?;
            out.links.push(LinkEntry {
                path: relative,
                target: target.replace('\\', "/"),
            });
            continue;
        }
        if meta.is_dir() {
            walk(root, &path, out)?;
            continue;
        }

        out.files.push(FileEntry {
            path: relative,
            sha256: digest(&path)?,
            size: meta.len(),
            executable: is_executable(&meta),
        });
    }

    if seen == 0 {
        return Err(ScanError::EmptyDirectory {
            path: dir.display().to_string(),
        });
    }
    Ok(())
}

/// Install-root-relative path with `/` separators on every platform.
fn relative_to(root: &Path, path: &Path) -> Result<String, ScanError> {
    let relative = path.strip_prefix(root).map_err(|_| ScanError::Io {
        what: path.display().to_string(),
        why: format!("is outside {}", root.display()),
    })?;
    Ok(relative
        .components()
        .map(|c| c.as_os_str().to_string_lossy().into_owned())
        .collect::<Vec<_>>()
        .join("/"))
}

fn digest(path: &Path) -> Result<String, ScanError> {
    let mut file = fs::File::open(path)
        .map_err(|e| ScanError::io(format!("opening {}", path.display()), e))?;
    let mut hasher = Sha256::new();
    std::io::copy(&mut file, &mut hasher)
        .map_err(|e| ScanError::io(format!("hashing {}", path.display()), e))?;
    Ok(hex::encode(hasher.finalize()))
}

/// Whether the packaged file carries an execute bit.
///
/// Each release is built on its own runner, on the platform it packages, so
/// "Windows has no mode" costs nothing here: `.exe` and `.dll` need no bit, and
/// a macOS bundle is never assembled on Windows.
#[cfg(unix)]
fn is_executable(meta: &fs::Metadata) -> bool {
    use std::os::unix::fs::PermissionsExt;
    meta.permissions().mode() & 0o111 != 0
}

#[cfg(not(unix))]
fn is_executable(_meta: &fs::Metadata) -> bool {
    false
}
