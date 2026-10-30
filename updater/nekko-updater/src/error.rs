//! Concrete error type for the update path.
//!
//! Every variant names what was wrong with the *manifest*, never "something
//! failed": a rejected update is a security event, and "signature check
//! failed" and "this manifest is for Windows" call for very different
//! reactions from whoever reads the log.

use thiserror::Error;

/// Why a manifest was not accepted.
#[derive(Debug, Error, PartialEq, Eq)]
pub enum ManifestError {
    /// The envelope or the payload inside it is not the JSON we expect.
    #[error("malformed manifest: {0}")]
    Malformed(String),

    /// The envelope names a key the client does not carry. Distinct from a bad
    /// signature on purpose: this is what a client sees after the signing key
    /// rotated past the two it was built with, and the cure is an update
    /// through some other channel, not a retry.
    #[error("manifest signed with unknown key id {0:?}")]
    UnknownKey(String),

    /// The signature does not match the payload under the named key. Either
    /// the file was altered in flight or it was signed by someone else.
    #[error("manifest signature does not verify")]
    BadSignature,

    /// Signed, but for another platform or another channel. Worth its own
    /// variant because the signature check PASSED — the manifest is genuine,
    /// it is simply not ours, which is what serving the wrong file at the
    /// right URL looks like.
    #[error("manifest is for {found}, expected {expected}")]
    WrongTarget { expected: String, found: String },

    /// A file entry the updater must not act on: a path that escapes the
    /// install directory, a digest that is not a sha256, a duplicate path, an
    /// empty file list.
    #[error("invalid file entry: {0}")]
    InvalidFile(String),

    /// `minimum_version` above the version the manifest itself ships — a
    /// manifest that mandates a build nobody can obtain.
    #[error("minimum_version {minimum} is above the shipped version {version}")]
    ImpossibleMinimum { minimum: String, version: String },

    /// A version string that is not `major.minor.patch`.
    #[error("malformed version {0:?}")]
    BadVersion(String),
}

/// Why an update could not be prepared (38.4).
///
/// Separate from [`ManifestError`] because the questions are different: a
/// rejected manifest is a trust event, a failed preparation is a disk or a
/// network that did not cooperate. Mixing them would make "is this worth
/// alarming about" unanswerable from the variant.
#[derive(Debug, Error)]
pub enum UpdateError {
    /// Bytes did not arrive. Carries the transport's own verdict, including
    /// whether it is worth retrying.
    #[error("fetching: {0}")]
    Transport(#[from] crate::transport::TransportError),

    /// The channel served something that is not a manifest we accept. Kept as
    /// its own variant rather than flattened: a signature that does not verify
    /// is a different kind of news from a disk that is full.
    #[error("{0}")]
    Manifest(#[from] ManifestError),

    /// A downloaded body did not hash to the name it was fetched under, twice
    /// in a row. Either the channel is serving a mangled blob or something in
    /// between is rewriting bytes; in both cases the update stops rather than
    /// installing what it cannot identify.
    #[error("{path}: expected sha256 {expected}, got {got}")]
    DigestMismatch {
        path: String,
        expected: String,
        got: String,
    },

    /// The staging tree came out different from what the manifest describes —
    /// a missing file, a size that does not match. The last check before a
    /// tree is called ready, and the one that must never be skipped: the
    /// alternative is a half-built application swapped in as if it were whole.
    #[error("staging is incomplete: {0}")]
    Incomplete(String),

    /// Staging is not on the same volume as the installation, so the swap
    /// (38.5) could not be a rename. Refused here, while nothing has been
    /// downloaded yet, rather than at the last metre where the only options
    /// left are a slow copy or a failed update.
    #[error(
        "staging {staging} is on a different volume than {install} — the swap must be a rename"
    )]
    DifferentVolume { staging: String, install: String },

    /// The manifest asks for a symlink on a platform where creating one needs
    /// privileges we deliberately do not ask for. Windows releases carry no
    /// links; a Windows manifest that does is a build fault.
    #[error("{path}: this platform cannot create symlinks without privileges")]
    LinksUnsupported { path: String },

    /// This version already failed to run on this machine and was rolled
    /// back. Preparing it again would reinstall it, fail again and roll back
    /// again — every few hours, for as long as the channel serves it.
    #[error("version {version} was rolled back on this installation and is not tried again")]
    Refused { version: String },

    /// The caller asked to stop — a call started, the user quit, the machine
    /// went to battery. Not a failure: what has been downloaded stays, and the
    /// next attempt resumes from it.
    #[error("preparation stopped by the caller")]
    Stopped,

    /// A filesystem operation failed, named by what was being attempted.
    #[error("{what}: {why}")]
    Io { what: String, why: String },
}

impl UpdateError {
    /// An io failure, labelled with what was being attempted — the label is
    /// the whole value of the variant, since "No such file or directory" on its
    /// own names neither the file nor the step.
    pub fn io(what: impl Into<String>, why: std::io::Error) -> Self {
        UpdateError::Io {
            what: what.into(),
            why: why.to_string(),
        }
    }
}

/// Which step of an update a failure belongs to.
///
/// The message a failure carries is for a person reading a log line; this is
/// for everything else — deciding whether it is worth alarming about, counting
/// failures by kind, showing "the download did not finish" rather than the
/// text of an `io::Error`. Without it "fetching: …", "expected sha256 …" and
/// "creating /…: Read-only file system" are one undifferentiated string, and
/// whoever reads them has to parse prose to learn which half of the update
/// broke.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum UpdateStage {
    /// The channel could not be asked whether there is anything newer. The
    /// ordinary case — no network, a captive portal, a CDN hiccup — and the
    /// one that needs nobody to do anything.
    Check,
    /// The manifest is not signed by anyone this build trusts. A trust event,
    /// not a hiccup: either the file was altered in flight or it was signed by
    /// someone else, and no number of retries changes that.
    Trust,
    /// The bytes did not arrive.
    Download,
    /// The bytes arrived and are not what the manifest describes — a digest
    /// that does not match, a tree that came out incomplete, a manifest that
    /// is genuine but malformed. Nothing is installed on this answer.
    Verify,
    /// The disk or the layout refused: no room, a read-only volume, staging on
    /// another volume than the installation, a link this platform cannot make.
    Staging,
    /// The update was refused before it began — a version already rolled back
    /// on this machine, a platform that cannot swap in place.
    Refused,
}

/// Which half of the loop was running when a failure happened.
///
/// It exists for exactly one ambiguity: bytes that do not arrive mean
/// [`UpdateStage::Check`] while asking whether an update exists and
/// [`UpdateStage::Download`] while fetching one. Every other failure names its
/// own step regardless of when it happened.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum UpdatePhase {
    /// Asking the channel what it serves.
    Check,
    /// Building the staging tree.
    Prepare,
}

impl UpdateError {
    /// Which step this failure belongs to. See [`UpdatePhase`] for why the
    /// phase is a parameter rather than something read off the error.
    pub fn stage(&self, phase: UpdatePhase) -> UpdateStage {
        match self {
            UpdateError::Manifest(e) => e.stage(),
            UpdateError::DigestMismatch { .. } | UpdateError::Incomplete(_) => UpdateStage::Verify,
            UpdateError::DifferentVolume { .. }
            | UpdateError::LinksUnsupported { .. }
            | UpdateError::Io { .. } => UpdateStage::Staging,
            UpdateError::Refused { .. } => UpdateStage::Refused,
            // Transport is the ambiguous one, and `Stopped` is not a failure
            // at all — it never reaches a failure report. Both take the stage
            // of the phase they happened in rather than inventing one.
            UpdateError::Transport(_) | UpdateError::Stopped => match phase {
                UpdatePhase::Check => UpdateStage::Check,
                UpdatePhase::Prepare => UpdateStage::Download,
            },
        }
    }
}

impl ManifestError {
    /// A rejected manifest is either "we do not trust this" or "this is not a
    /// manifest we can read". The first is the security event the module doc
    /// is about; the rest are a build or a channel fault.
    pub fn stage(&self) -> UpdateStage {
        match self {
            ManifestError::BadSignature | ManifestError::UnknownKey(_) => UpdateStage::Trust,
            ManifestError::Malformed(_)
            | ManifestError::WrongTarget { .. }
            | ManifestError::InvalidFile(_)
            | ManifestError::ImpossibleMinimum { .. }
            | ManifestError::BadVersion(_) => UpdateStage::Verify,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn bytes_that_do_not_arrive_name_the_phase_they_failed_in() {
        let err = || {
            UpdateError::Transport(crate::transport::TransportError::Unreachable {
                url: "https://update.nekkochat.com/latest.json".into(),
                why: "dns".into(),
            })
        };
        assert_eq!(err().stage(UpdatePhase::Check), UpdateStage::Check);
        assert_eq!(err().stage(UpdatePhase::Prepare), UpdateStage::Download);
    }

    #[test]
    fn everything_else_names_its_own_step_in_either_phase() {
        let cases = [
            (
                UpdateError::Manifest(ManifestError::BadSignature),
                UpdateStage::Trust,
            ),
            (
                UpdateError::Manifest(ManifestError::UnknownKey("k1".into())),
                UpdateStage::Trust,
            ),
            (
                UpdateError::Manifest(ManifestError::Malformed("{".into())),
                UpdateStage::Verify,
            ),
            (
                UpdateError::DigestMismatch {
                    path: "a".into(),
                    expected: "b".into(),
                    got: "c".into(),
                },
                UpdateStage::Verify,
            ),
            (
                UpdateError::Incomplete("missing a".into()),
                UpdateStage::Verify,
            ),
            (
                UpdateError::io("creating /x", std::io::Error::other("full")),
                UpdateStage::Staging,
            ),
            (
                UpdateError::DifferentVolume {
                    staging: "/a".into(),
                    install: "/b".into(),
                },
                UpdateStage::Staging,
            ),
            (
                UpdateError::LinksUnsupported { path: "a".into() },
                UpdateStage::Staging,
            ),
            (
                UpdateError::Refused {
                    version: "1.2.3".into(),
                },
                UpdateStage::Refused,
            ),
        ];
        for (err, want) in cases {
            assert_eq!(err.stage(UpdatePhase::Check), want, "{err}");
            assert_eq!(err.stage(UpdatePhase::Prepare), want, "{err}");
        }
    }
}
