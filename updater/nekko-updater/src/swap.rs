//! The last metre: putting the prepared version in place, and surviving it not
//! working (38.5).
//!
//! ```text
//!   quit:   Nekko.app ──▶ Nekko.app.old        (rename 1)
//!           staging/tree ──▶ Nekko.app         (rename 2)   ← microseconds apart
//!           PENDING.json written                            ← "this is on trial"
//!   start:  recover() ──▶ Confirmed | OnTrial | RolledBack
//!   healthy: confirm() ──▶ .old deleted, PENDING gone
//! ```
//!
//! # Why two renames and not a copy
//!
//! A directory rename inside one volume is a metadata operation: the window in
//! which neither name resolves is microseconds wide, and nothing is half-
//! written at any point. A copy would spend a minute with the installation in
//! an undefined state — and on Unix an open file is held by inode, so
//! renaming the directory of the *running* application is allowed, which is
//! what makes this legal at all (plan 38 §1.2, and §0.6 on why no permanent
//! launcher process is needed for it).
//!
//! [`Layout::prepare_dirs`] refused a staging directory on another volume back
//! when nothing had been downloaded yet, precisely so that this step cannot
//! fail for that reason.
//!
//! # Why the old version is kept until something says the new one works
//!
//! "The update installed" and "the update works" are different claims, and
//! only the second one matters. So the swap leaves the previous version beside
//! the new one under `.old`, writes down that a version is on trial, and
//! deletes nothing until someone calls [`confirm`]. Two starts that never
//! reach confirmation spend the new version's chances, and the next start
//! puts the old one back.
//!
//! ⚠️ **What the counter can and cannot cover.** It covers a version that
//! starts and then falls over — the common shape of a bad release. It cannot
//! cover a version that does not load at all (a broken dylib, a bad
//! signature): nothing of ours runs, so nothing counts. That case has exactly
//! one moment where a live process of ours exists — the outgoing one, which is
//! performing the swap — and covering it is that process's job: spawn the new
//! binary, watch it for a few seconds, roll back if it dies. That supervision
//! is platform work and lives in `nekko-core`; the policy it applies is here.
//!
//! # Windows
//!
//! The same two renames, under one precondition the caller owns: nothing
//! inside the installation may be running or held open. Windows refuses to
//! rename a directory while a file in it is mapped as a running `.exe` or a
//! loaded `.dll`, or while a process has it as its working directory. So the
//! swap is performed by a process that lives OUTSIDE the tree, after
//! everything inside it has exited — in Hikkari that is the service, which
//! sits beside `app\` and stops the GUI and the core before calling [`apply`].
//!
//! That precondition is also why the first rename is the one expected to fail
//! on Windows (a straggler still holding a file), and why its failure must
//! leave no trial record behind.

use std::fs;
use std::path::{Path, PathBuf};

use serde::{Deserialize, Serialize};

use crate::error::UpdateError;
use crate::manifest::Version;
use crate::stage::Layout;
use crate::stage::{read_ready, unix_now};

/// How many starts a new version gets to prove itself. The start AFTER they
/// are used up is the one that rolls back.
///
/// The off-by-one matters and is deliberate: [`recover`] runs before the
/// application is up, so a start that rolled back would not have been a
/// chance to confirm. Two chances, then the third start finds the old version
/// in place.
///
/// Two, not one: a single failed start can be the machine (a full disk, a lid
/// closed during launch), and rolling back on it would undo a good release.
/// Two in a row is a pattern. Not five, either — every attempt is a person
/// watching the application die.
pub const MAX_ATTEMPTS: u32 = 2;

/// Suffix the previous version is kept under until the new one is confirmed.
const OLD_SUFFIX: &str = ".old";

/// The Windows swap helper, shipped beside `nekko.exe` by the release
/// packaging and by nothing else.
///
/// Its presence is also what makes a Windows directory an *installation* at
/// all (plan 38 §0.7): the swap cannot happen without it, so a copy that lacks
/// it — a `cargo test` binary, `flutter run`, a bare `flutter build windows` —
/// has nothing to update with and must not try. Same convention as Discord's
/// `Update.exe` and Telegram's `Updater.exe`.
///
/// ⚠️ "Updater" in the name trips Windows' installer detection, which elevates
/// an exe called *update*/*setup*/*install* that declares no execution level.
/// The binary MUST embed a manifest with `requestedExecutionLevel asInvoker`,
/// or every update starts with a UAC prompt (plan 38 §1.3).
pub const WINDOWS_HELPER: &str = "NekkoUpdater.exe";

/// A version that has been installed but has not yet proved it runs.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Pending {
    /// What was installed before the swap — what a rollback goes back to.
    pub from: Version,
    /// What is on trial.
    pub to: Version,
    /// Starts observed since the swap, this one included.
    pub attempts: u32,
    pub applied_at: i64,
}

/// The swap happened.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Applied {
    pub from: Version,
    pub to: Version,
    /// Where the previous version is, until [`confirm`].
    pub previous: PathBuf,
}

/// What the start found, and what was done about it.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Recovery {
    /// No update is in flight. The overwhelmingly common case.
    Nothing,
    /// An interrupted swap was finished. Only reachable by losing power in the
    /// microseconds between the two renames — and worth handling anyway,
    /// because the alternative is an installation with no application in it.
    Repaired,
    /// A new version is on trial and this is attempt `attempts` of
    /// [`MAX_ATTEMPTS`]. The caller confirms it with [`confirm`] once the
    /// application is actually working.
    OnTrial { pending: Pending },
    /// The new version used up its attempts; the previous one is back in
    /// place. The caller has to relaunch to be running it — this process is
    /// still the bytes that failed.
    RolledBack { from: Version, to: Version },
}

fn pending_file(layout: &Layout) -> PathBuf {
    layout.work().join("PENDING.json")
}

/// `…/Nekko.app` → `…/Nekko.app.old`.
fn previous_path(install: &Path) -> Result<PathBuf, UpdateError> {
    let name = install
        .file_name()
        .ok_or_else(|| UpdateError::Incomplete(format!("{} has no file name", install.display())))?
        .to_string_lossy()
        .into_owned();
    let parent = install.parent().ok_or_else(|| {
        UpdateError::Incomplete(format!("{} has no parent directory", install.display()))
    })?;
    Ok(parent.join(format!("{name}{OLD_SUFFIX}")))
}

/// Read the pending record, if there is one that parses.
pub fn read_pending(layout: &Layout) -> Option<Pending> {
    let bytes = fs::read(pending_file(layout)).ok()?;
    serde_json::from_slice(&bytes).ok()
}

/// Put the staged version in place. Call this on the way out, never while the
/// application is doing anything a user can see.
///
/// `installed` is the version being replaced — recorded so a rollback knows
/// what it is going back to without having to read the old bundle.
pub fn apply(layout: &Layout, installed: Version) -> Result<Applied, UpdateError> {
    let ready = read_ready(layout).ok_or_else(|| {
        UpdateError::Incomplete("no readiness flag — nothing is staged to apply".to_owned())
    })?;
    let tree = layout.tree();
    if !tree.is_dir() {
        return Err(UpdateError::Incomplete(format!(
            "{} is not there, but its readiness flag is — the staging directory has been \
             tampered with; delete it and prepare again",
            tree.display()
        )));
    }

    let install = layout.install().to_path_buf();
    let previous = previous_path(&install)?;
    // A leftover from a swap whose confirmation never came: it cannot be kept,
    // and it cannot be the rollback target of THIS swap either, because the
    // version it holds is two releases back.
    if previous.exists() {
        remove_tree(&previous)?;
    }

    // ⚠️ The record goes down BEFORE the renames, and the reason is a crash
    // AFTER them, not between them. Losing power between the two renames is
    // recognised from the disk alone — there is no application where the
    // application belongs — but losing it after the second rename with no
    // record written would leave the new version installed and NOT on trial:
    // the next start would see a `.old` with nothing pending, read it as
    // leftover disk, and delete the only copy the rollback could come from.
    // Written first, the worst case is a record describing a swap that never
    // happened, which `recover` resolves from the disk and discards.
    let pending = Pending {
        from: installed,
        to: ready.version,
        attempts: 0,
        applied_at: unix_now(),
    };
    write_pending(layout, &pending)?;

    if let Err(e) = fs::rename(&install, &previous) {
        // Nothing has moved, so the record written above describes a swap
        // that did not happen. Left in place, the next start would count the
        // OLD version as on trial and eventually "roll back" to nothing.
        // On Windows this is the common failure: a file inside the
        // installation still held open by a process that has not exited.
        let _ = fs::remove_file(pending_file(layout));
        return Err(UpdateError::io(
            format!("moving {} aside", install.display()),
            e,
        ));
    }
    if let Err(e) = fs::rename(&tree, &install) {
        // The window this guards is microseconds wide and it is still worth
        // guarding: without it, a failure here leaves no application at the
        // path the shortcut, the firewall rule and the user all point at.
        let _ = fs::rename(&previous, &install);
        let _ = fs::remove_file(pending_file(layout));
        return Err(UpdateError::io(
            format!("moving the staged version to {}", install.display()),
            e,
        ));
    }

    // The flag said "a tree is ready to install". It has been installed.
    let _ = fs::remove_file(layout.ready_file());
    log::info!(
        target: "updater",
        "swapped {} -> {}, on trial",
        pending.from,
        pending.to
    );

    Ok(Applied {
        from: pending.from,
        to: pending.to,
        previous,
    })
}

/// The first thing to run at every start, before anything the user can see.
///
/// Three jobs, in this order: finish a swap that was interrupted, count a
/// start against a version on trial, and roll back one that has used up its
/// attempts.
pub fn recover(layout: &Layout) -> Result<Recovery, UpdateError> {
    let install = layout.install().to_path_buf();
    let previous = previous_path(&install)?;

    // Interrupted between the two renames: the old version is aside and the
    // new one never arrived. Recognisable without ambiguity — there is no
    // application where the application belongs.
    if !install.exists() && previous.exists() {
        fs::rename(&previous, &install)
            .map_err(|e| UpdateError::io(format!("restoring {}", install.display()), e))?;
        let _ = fs::remove_file(pending_file(layout));
        log::warn!(
            target: "updater",
            "a swap was interrupted between the two renames; the previous version is back"
        );
        return Ok(Recovery::Repaired);
    }

    let mut pending = match read_pending(layout) {
        Some(pending) => pending,
        None => {
            // No trial in progress, so a leftover previous version is just
            // disk: a confirmation that could not delete it, or a rollback
            // that was interrupted after putting the old bytes back.
            if previous.exists() {
                remove_tree(&previous)?;
            }
            return Ok(Recovery::Nothing);
        }
    };

    pending.attempts += 1;
    if pending.attempts > MAX_ATTEMPTS {
        let rolled = rollback(layout, &pending)?;
        return Ok(rolled);
    }
    write_pending(layout, &pending)?;
    log::info!(
        target: "updater",
        "started {} on trial, attempt {} of {}",
        pending.to,
        pending.attempts,
        MAX_ATTEMPTS
    );
    Ok(Recovery::OnTrial { pending })
}

/// The application is working: keep the new version and reclaim the disk.
///
/// What counts as working is the caller's judgement and deliberately not this
/// module's — "the window is up and the socket authenticated" is a product
/// question. Calling it too early makes the rollback useless; the cost of
/// calling it a little late is nothing at all.
pub fn confirm(layout: &Layout) -> Result<Option<Pending>, UpdateError> {
    let pending = read_pending(layout);
    let previous = previous_path(layout.install())?;
    if previous.exists() {
        remove_tree(&previous)?;
    }
    let _ = fs::remove_file(pending_file(layout));
    // The staged tree has become the installation; anything left under those
    // names is a duplicate of the release that is now running.
    let _ = fs::remove_dir_all(layout.tree());
    let _ = fs::remove_file(layout.ready_file());
    if let Some(p) = &pending {
        log::info!(target: "updater", "confirmed {}, previous version reclaimed", p.to);
    }
    Ok(pending)
}

/// Put the previous version back. Used by [`recover`] when the attempts run
/// out, and available to the supervising process when the new version does not
/// come up at all.
pub fn rollback(layout: &Layout, pending: &Pending) -> Result<Recovery, UpdateError> {
    let install = layout.install().to_path_buf();
    let previous = previous_path(&install)?;
    if !previous.exists() {
        // Nothing to go back to. Clearing the record is all that is left —
        // counting attempts forever against a rollback that cannot happen
        // would mean an application that reports a failed update at every
        // start and can do nothing about it.
        let _ = fs::remove_file(pending_file(layout));
        return Err(UpdateError::Incomplete(format!(
            "{} is gone, so version {} cannot be rolled back to",
            previous.display(),
            pending.from
        )));
    }

    // The failed version is moved aside rather than deleted: it is the only
    // copy of the bytes that would explain what went wrong, and it is where a
    // crash report's addresses point.
    let failed = layout.work().join(format!("failed-{}", pending.to));
    if failed.exists() {
        remove_tree(&failed)?;
    }
    if install.exists() {
        fs::rename(&install, &failed)
            .map_err(|e| UpdateError::io("moving the failed version aside", e))?;
    }
    fs::rename(&previous, &install)
        .map_err(|e| UpdateError::io(format!("restoring {}", install.display()), e))?;
    let _ = fs::remove_file(pending_file(layout));
    let _ = fs::remove_file(layout.ready_file());
    refuse(layout, pending.to)?;
    log::error!(
        target: "updater",
        "rolled back to {} after {} failed starts of {}",
        pending.from,
        pending.attempts,
        pending.to
    );
    Ok(Recovery::RolledBack {
        from: pending.from,
        to: pending.to,
    })
}

fn write_pending(layout: &Layout, pending: &Pending) -> Result<(), UpdateError> {
    fs::create_dir_all(layout.work())
        .map_err(|e| UpdateError::io(format!("creating {}", layout.work().display()), e))?;
    let bytes = serde_json::to_vec_pretty(pending)
        .map_err(|e| UpdateError::Incomplete(format!("serialising the pending record: {e}")))?;
    let path = pending_file(layout);
    let tmp = path.with_extension("tmp");
    fs::write(&tmp, &bytes)
        .map_err(|e| UpdateError::io(format!("writing {}", tmp.display()), e))?;
    fs::rename(&tmp, &path).map_err(|e| UpdateError::io(format!("placing {}", path.display()), e))
}

fn remove_tree(path: &Path) -> Result<(), UpdateError> {
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

/// Versions that failed on this installation and are not to be installed
/// again.
///
/// ⚠️ This list is what stops an endless loop, and the loop is not
/// hypothetical: the channel goes on serving the release that was rolled back,
/// so without a memory the next poll would download it, install it, watch it
/// fail and roll it back again — every few hours, for as long as the release
/// stands.
///
/// Refusal is by version number, which has a consequence worth stating: a
/// release fixed and republished under the SAME version stays refused. That is
/// the correct behaviour for a channel where a version names a set of bytes —
/// fix it under a new version. A deliberate "try that again" clears the list
/// with [`forget_refusals`].
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
struct Refusals {
    versions: Vec<String>,
}

/// Room for a handful, so the file cannot grow without bound on a machine
/// where something is deeply wrong.
const REMEMBERED_FAILURES: usize = 8;

fn refusals_file(layout: &Layout) -> PathBuf {
    layout.work().join("REFUSED.json")
}

fn read_refusals(layout: &Layout) -> Refusals {
    fs::read(refusals_file(layout))
        .ok()
        .and_then(|bytes| serde_json::from_slice(&bytes).ok())
        .unwrap_or_default()
}

/// Whether this version has already failed here.
pub fn is_refused(layout: &Layout, version: Version) -> bool {
    read_refusals(layout)
        .versions
        .iter()
        .any(|v| *v == version.to_string())
}

/// Remember that this version does not run on this machine.
pub fn refuse(layout: &Layout, version: Version) -> Result<(), UpdateError> {
    let mut refusals = read_refusals(layout);
    let version = version.to_string();
    refusals.versions.retain(|v| *v != version);
    refusals.versions.push(version);
    if refusals.versions.len() > REMEMBERED_FAILURES {
        let excess = refusals.versions.len() - REMEMBERED_FAILURES;
        refusals.versions.drain(..excess);
    }
    let bytes = serde_json::to_vec_pretty(&refusals)
        .map_err(|e| UpdateError::Incomplete(format!("serialising the refusal list: {e}")))?;
    fs::create_dir_all(layout.work())
        .map_err(|e| UpdateError::io(format!("creating {}", layout.work().display()), e))?;
    let path = refusals_file(layout);
    let tmp = path.with_extension("tmp");
    fs::write(&tmp, &bytes)
        .map_err(|e| UpdateError::io(format!("writing {}", tmp.display()), e))?;
    fs::rename(&tmp, &path).map_err(|e| UpdateError::io(format!("placing {}", path.display()), e))
}

/// Forget every refusal — a person deciding to try an update that failed.
pub fn forget_refusals(layout: &Layout) -> Result<(), UpdateError> {
    match fs::remove_file(refusals_file(layout)) {
        Ok(()) => Ok(()),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(()),
        Err(e) => Err(UpdateError::io("clearing the refusal list", e)),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::manifest::{Channel, Platform};
    use crate::stage::Ready;
    use std::sync::atomic::{AtomicU32, Ordering};

    struct Tmp(PathBuf);

    impl Tmp {
        fn new(label: &str) -> Tmp {
            static N: AtomicU32 = AtomicU32::new(0);
            let path = std::env::temp_dir().join(format!(
                "nekko-swap-{label}-{}-{}",
                std::process::id(),
                N.fetch_add(1, Ordering::Relaxed)
            ));
            let _ = fs::remove_dir_all(&path);
            fs::create_dir_all(&path).unwrap();
            Tmp(path)
        }
    }

    impl Drop for Tmp {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.0);
        }
    }

    fn v(s: &str) -> Version {
        Version::parse(s).unwrap()
    }

    /// An installation holding one marker file, and a staged tree holding
    /// another — so "which version is installed" is answerable by reading one
    /// file, without a bundle.
    fn scene(tmp: &Tmp, staged: Option<&str>) -> Layout {
        let install = tmp.0.join("Nekko.app");
        fs::create_dir_all(install.join("Contents/MacOS")).unwrap();
        fs::write(install.join("Contents/MacOS/nekko"), b"0.1.0").unwrap();
        let layout = Layout::new(&install, tmp.0.join(".nekko-staging"));
        if let Some(version) = staged {
            fs::create_dir_all(layout.tree().join("Contents/MacOS")).unwrap();
            fs::write(
                layout.tree().join("Contents/MacOS/nekko"),
                version.as_bytes(),
            )
            .unwrap();
            let ready = Ready {
                version: v(version),
                platform: Platform::Macos,
                channel: Channel::Stable,
                built_at: 1_757_563_200,
                files: 1,
                links: 0,
            };
            fs::write(
                layout.ready_file(),
                serde_json::to_vec_pretty(&ready).unwrap(),
            )
            .unwrap();
        }
        layout
    }

    fn installed_marker(layout: &Layout) -> String {
        String::from_utf8(fs::read(layout.install().join("Contents/MacOS/nekko")).unwrap()).unwrap()
    }

    #[test]
    fn the_swap_installs_the_staged_version_and_keeps_the_old_one() {
        let tmp = Tmp::new("apply");
        let layout = scene(&tmp, Some("0.2.0"));

        let applied = apply(&layout, v("0.1.0")).unwrap();
        assert_eq!(applied.from, v("0.1.0"));
        assert_eq!(applied.to, v("0.2.0"));
        assert_eq!(installed_marker(&layout), "0.2.0");
        assert_eq!(
            fs::read(applied.previous.join("Contents/MacOS/nekko")).unwrap(),
            b"0.1.0"
        );
        // On trial, and the flag no longer claims a tree is waiting.
        assert_eq!(read_pending(&layout).unwrap().to, v("0.2.0"));
        assert!(crate::stage::read_ready(&layout).is_none());
    }

    #[test]
    fn a_confirmed_start_reclaims_the_disk() {
        let tmp = Tmp::new("confirm");
        let layout = scene(&tmp, Some("0.2.0"));
        let applied = apply(&layout, v("0.1.0")).unwrap();

        assert_eq!(
            recover(&layout).unwrap(),
            Recovery::OnTrial {
                pending: Pending {
                    from: v("0.1.0"),
                    to: v("0.2.0"),
                    attempts: 1,
                    applied_at: read_pending(&layout).unwrap().applied_at,
                }
            }
        );
        let confirmed = confirm(&layout).unwrap().unwrap();
        assert_eq!(confirmed.to, v("0.2.0"));
        assert!(!applied.previous.exists(), "the old version is 245 MB");
        assert!(read_pending(&layout).is_none());
        assert_eq!(installed_marker(&layout), "0.2.0");
        // And a second start is an ordinary one.
        assert_eq!(recover(&layout).unwrap(), Recovery::Nothing);
    }

    #[test]
    fn two_starts_without_confirmation_put_the_old_version_back() {
        let tmp = Tmp::new("rollback");
        let layout = scene(&tmp, Some("0.2.0"));
        apply(&layout, v("0.1.0")).unwrap();

        // Two starts that came up and died before confirming: both were real
        // chances, and both were spent.
        assert!(matches!(
            recover(&layout).unwrap(),
            Recovery::OnTrial { .. }
        ));
        assert!(matches!(
            recover(&layout).unwrap(),
            Recovery::OnTrial { .. }
        ));
        // The third start does not give it a third chance.
        assert_eq!(
            recover(&layout).unwrap(),
            Recovery::RolledBack {
                from: v("0.1.0"),
                to: v("0.2.0")
            }
        );
        assert_eq!(installed_marker(&layout), "0.1.0");
        assert!(read_pending(&layout).is_none());
        // The failed version is kept for whoever has to explain the crash.
        assert_eq!(
            fs::read(layout.work().join("failed-0.2.0/Contents/MacOS/nekko")).unwrap(),
            b"0.2.0"
        );
    }

    #[test]
    fn a_rolled_back_version_is_not_installed_again() {
        // Without this the channel wins by repetition: it goes on serving the
        // broken release, and every poll would reinstall it.
        let tmp = Tmp::new("refuse");
        let layout = scene(&tmp, Some("0.2.0"));
        apply(&layout, v("0.1.0")).unwrap();
        for _ in 0..=MAX_ATTEMPTS {
            recover(&layout).unwrap();
        }

        assert!(is_refused(&layout, v("0.2.0")));
        assert!(!is_refused(&layout, v("0.3.0")), "only the one that failed");
        forget_refusals(&layout).unwrap();
        assert!(
            !is_refused(&layout, v("0.2.0")),
            "a person can say try again"
        );
    }

    #[test]
    fn the_refusal_list_does_not_grow_without_bound() {
        let tmp = Tmp::new("refusals");
        let layout = scene(&tmp, None);
        for patch in 0..(REMEMBERED_FAILURES + 4) {
            refuse(&layout, v(&format!("0.1.{patch}"))).unwrap();
        }
        assert_eq!(read_refusals(&layout).versions.len(), REMEMBERED_FAILURES);
        // The oldest are the ones dropped.
        assert!(!is_refused(&layout, v("0.1.0")));
        assert!(is_refused(&layout, v("0.1.11")));
    }

    #[test]
    fn a_swap_interrupted_between_the_two_renames_is_repaired_at_the_next_start() {
        // Power cut in the microseconds between them: the old version is
        // aside, the new one never arrived, and there is no application where
        // the shortcut points.
        let tmp = Tmp::new("torn");
        let layout = scene(&tmp, Some("0.2.0"));
        let previous = previous_path(layout.install()).unwrap();
        write_pending(
            &layout,
            &Pending {
                from: v("0.1.0"),
                to: v("0.2.0"),
                attempts: 0,
                applied_at: 0,
            },
        )
        .unwrap();
        fs::rename(layout.install(), &previous).unwrap();
        assert!(!layout.install().exists(), "the scene is the torn state");

        assert_eq!(recover(&layout).unwrap(), Recovery::Repaired);
        assert_eq!(installed_marker(&layout), "0.1.0");
        assert!(read_pending(&layout).is_none());
        // Nothing was refused: the new version never got to run, so there is
        // nothing held against it. The staged tree is still there and the next
        // quit applies it again.
        assert!(!is_refused(&layout, v("0.2.0")));
        assert!(layout.tree().is_dir());
    }

    #[test]
    fn applying_with_nothing_staged_is_refused_before_anything_moves() {
        let tmp = Tmp::new("nothing");
        let layout = scene(&tmp, None);
        let err = apply(&layout, v("0.1.0")).unwrap_err();
        assert!(err.to_string().contains("nothing is staged"), "{err}");
        assert_eq!(installed_marker(&layout), "0.1.0");
    }

    #[test]
    fn a_first_rename_that_fails_leaves_no_trial_behind() {
        // On Windows the realistic cause is a file inside the installation
        // still held open; a missing installation fails the same rename
        // portably.
        let tmp = Tmp::new("first-rename");
        let layout = scene(&tmp, Some("0.2.0"));
        fs::remove_dir_all(layout.install()).unwrap();

        assert!(apply(&layout, v("0.1.0")).is_err());
        assert_eq!(read_pending(&layout), None);
        assert!(
            read_ready(&layout).is_some(),
            "the staged version stays ready for a retry"
        );
    }

    #[test]
    fn a_flag_whose_tree_is_gone_does_not_swap_an_empty_directory_in() {
        let tmp = Tmp::new("flagonly");
        let layout = scene(&tmp, Some("0.2.0"));
        fs::remove_dir_all(layout.tree()).unwrap();
        let err = apply(&layout, v("0.1.0")).unwrap_err();
        assert!(err.to_string().contains("tampered"), "{err}");
        assert_eq!(installed_marker(&layout), "0.1.0");
        assert!(read_pending(&layout).is_none());
    }

    #[test]
    fn a_previous_version_left_over_without_a_trial_is_disk_and_is_reclaimed() {
        let tmp = Tmp::new("leftover");
        let layout = scene(&tmp, None);
        let previous = previous_path(layout.install()).unwrap();
        fs::create_dir_all(previous.join("Contents")).unwrap();

        assert_eq!(recover(&layout).unwrap(), Recovery::Nothing);
        assert!(!previous.exists());
    }

    #[test]
    fn a_second_swap_does_not_stack_two_old_versions() {
        let tmp = Tmp::new("twogen");
        let layout = scene(&tmp, Some("0.2.0"));
        let applied = apply(&layout, v("0.1.0")).unwrap();
        // A start that never confirmed, then a second release arrives and is
        // staged and applied.
        recover(&layout).unwrap();
        fs::create_dir_all(layout.tree().join("Contents/MacOS")).unwrap();
        fs::write(layout.tree().join("Contents/MacOS/nekko"), b"0.3.0").unwrap();
        fs::write(
            layout.ready_file(),
            serde_json::to_vec_pretty(&Ready {
                version: v("0.3.0"),
                platform: Platform::Macos,
                channel: Channel::Stable,
                built_at: 0,
                files: 1,
                links: 0,
            })
            .unwrap(),
        )
        .unwrap();

        let second = apply(&layout, v("0.2.0")).unwrap();
        assert_eq!(installed_marker(&layout), "0.3.0");
        assert_eq!(second.previous, applied.previous);
        assert_eq!(
            fs::read(second.previous.join("Contents/MacOS/nekko")).unwrap(),
            b"0.2.0",
            "the kept copy is the version being replaced, not one from two releases ago"
        );
    }

    #[test]
    fn a_rollback_with_nothing_to_go_back_to_says_so_instead_of_trying_forever() {
        let tmp = Tmp::new("nowhere");
        let layout = scene(&tmp, None);
        let pending = Pending {
            from: v("0.1.0"),
            to: v("0.2.0"),
            attempts: 3,
            applied_at: 0,
        };
        write_pending(&layout, &pending).unwrap();
        let err = rollback(&layout, &pending).unwrap_err();
        assert!(err.to_string().contains("cannot be rolled back"), "{err}");
        assert!(
            read_pending(&layout).is_none(),
            "a trial that can never end must not be left running"
        );
    }

    #[test]
    fn confirming_when_nothing_is_pending_is_harmless() {
        let tmp = Tmp::new("noop");
        let layout = scene(&tmp, None);
        assert!(confirm(&layout).unwrap().is_none());
        assert_eq!(installed_marker(&layout), "0.1.0");
    }
}
