//! nekko-updater — how a release describes itself, and why a client believes it.
//!
//! ```text
//!   CI:     nekko-manifest publish ──▶ latest.json + manifest.json + blobs/ ──▶ CDN
//!                                                                              │
//!   client: channel::assess ──▶ signature::verify ──▶ manifest::decide ◀────────┘
//!           (is it worth a       (bytes as given)     (against installed)
//!            327 KB fetch?)
//! ```
//!
//! Roadmap 38.2 is the format, the signature and the decision; 38.3 the channel
//! those three are fetched from ([`channel`]) — its layout, the cheap version
//! probe that keeps a 327 KB manifest off every poll, and the strict limit on
//! what an unsigned probe may conclude; 38.4 the preparation ([`stage`]) —
//! which files to take from the installation and which to download, resumed by
//! digest, assembled into a complete tree beside the running application, and
//! called ready only after it has been verified. The bytes arrive through a
//! seam ([`transport`]) whose real implementation lives in `nekko-core`. The
//! last metre of the swap is 38.5 and arrives here later.
//!
//! Three properties the shape exists for:
//!
//! * **A manifest is trusted for its signature, never for its origin.** Which
//!   is what makes code-signing a separate, later, purely operational concern
//!   (plan 38 §0.4) instead of a blocker for this architecture.
//! * **The bytes that were signed are the bytes that are parsed.** There is no
//!   canonicalisation step to get wrong on one side.
//! * **A verified manifest cannot ask for something dangerous.** Path
//!   validation, digest form and duplicate entries are checked inside
//!   verification, so no later stage has to remember to.

pub mod channel;
pub mod check;
pub mod error;
pub mod manifest;
pub mod scan;
pub mod signature;
pub mod stage;
pub mod swap;
pub mod transport;

pub use channel::{assess, ProbeVerdict, VersionProbe};
pub use check::{poll, CachedManifest, ChannelConfig, Poll};
pub use error::{ManifestError, UpdateError};
pub use manifest::{
    decide, Channel, FileEntry, LinkEntry, Manifest, Platform, UpdateDecision, Version,
};
pub use scan::{scan_tree, ScanError, Scanned};
pub use signature::{sign, verify, Expect, TrustedKey};
pub use stage::{plan, prepare, read_ready, Control, Layout, Plan, Progress, Ready};
pub use swap::{apply, confirm, recover, Applied, Pending, Recovery};
pub use transport::{Body, Document, TransportError, UpdateTransport};
