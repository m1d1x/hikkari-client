//! The seam the update path fetches through.
//!
//! # Why this is a trait and not `reqwest` right here
//!
//! Everything expensive about 38.4 is logic, not networking: which files to
//! take from the old installation and which to download, resuming a 124 MB
//! body that was cut in half, rebuilding 24 symlinks, refusing to call a tree
//! ready when it is not. Those are exactly the things whose defects are
//! invisible — a staging tree that is wrong by one file produces an
//! application that starts and misbehaves.
//!
//! So the bytes arrive through a two-method trait, the client's real
//! implementation lives in `nekko-core` beside the HTTP stack it already
//! carries (one `reqwest` for the whole core, see its `Cargo.toml`), and every
//! test in this crate drives the same logic against an in-memory channel: no
//! sockets, no ports, no timing.
//!
//! The trait is blocking on purpose. Preparing an update is background work
//! that copies hundreds of megabytes; `nekko-core` runs it inside
//! `spawn_blocking` and implements these two methods on top of its async
//! client. An async trait here would push `tokio` and a TLS stack into a crate
//! that is otherwise pure logic, and buy nothing — there is no concurrency to
//! express, files are fetched one at a time by design (§3.5: an update must
//! never be the reason a call stutters).

use std::io::Read;

use thiserror::Error;

/// How the update path gets bytes out of the channel (38.3).
pub trait UpdateTransport: Send + Sync {
    /// Fetch a small document whole: the version probe or the manifest.
    ///
    /// `if_none_match` carries the ETag of the copy already held, and
    /// [`Document::NotModified`] is the answer that saves re-reading a 327 KB
    /// manifest that has not changed.
    fn get(&self, url: &str, if_none_match: Option<&str>) -> Result<Document, TransportError>;

    /// Open a blob body for streaming, asking to start at `from` bytes.
    ///
    /// `from` is a request, not a guarantee — see [`Body::offset`].
    fn open(&self, url: &str, from: u64) -> Result<Body, TransportError>;
}

/// The answer to [`UpdateTransport::get`].
pub enum Document {
    /// The ETag matched: what the caller already has is current.
    NotModified,
    Body {
        bytes: Vec<u8>,
        /// The validator to send back next time, when the channel offers one.
        etag: Option<String>,
    },
}

/// A blob body, streaming.
pub struct Body {
    /// The offset the bytes actually start at: `from` when the range request
    /// was honoured (206), **zero** when the server ignored it and sent the
    /// whole object (200).
    ///
    /// ⚠️ This field exists because getting it wrong is silent. A caller that
    /// assumes the range was honoured appends a whole body to a partial file,
    /// producing a file that is too long and whose digest is wrong — which
    /// looks exactly like corruption in transit, and which retrying cannot
    /// fix, because the retry appends again. So the transport reports where
    /// the body begins and the caller truncates to match.
    pub offset: u64,
    /// Total size of the object, when the channel states it. Compared against
    /// the manifest; never trusted instead of it.
    pub total: Option<u64>,
    pub read: Box<dyn Read + Send>,
}

/// Why bytes did not arrive.
///
/// Split by what the caller should do, not by which layer complained: a 404 is
/// worth a log line and no retry, a dropped connection is worth resuming.
#[derive(Debug, Error, PartialEq, Eq)]
pub enum TransportError {
    /// The channel answered, but not with the document — 404 for a blob the
    /// manifest promised, 403 from a misconfigured bucket, an HTML error page.
    #[error("{url} answered {code}")]
    Status { url: String, code: u16 },

    /// Nothing answered: DNS, TLS, connect, timeout. The common case on a
    /// laptop that just woke up, and never a reason to give up on the update.
    #[error("{url} unreachable: {why}")]
    Unreachable { url: String, why: String },

    /// The body started and stopped. Distinct from [`Self::Unreachable`]
    /// because whatever arrived is on disk and worth resuming from.
    #[error("{url} cut off: {why}")]
    Interrupted { url: String, why: String },
}

impl TransportError {
    /// Whether trying again, now, could plausibly do better.
    ///
    /// A status code could not: the file that is missing will still be missing
    /// in a second, and hammering the CDN over a bad release is how a bad
    /// release becomes an outage. The schedule (a poll every few hours) is the
    /// retry for that class.
    pub fn is_retryable(&self) -> bool {
        match self {
            TransportError::Status { code, .. } => {
                // 5xx and 429 are the channel asking for patience; 4xx is the
                // channel saying no.
                *code >= 500 || *code == 429
            }
            TransportError::Unreachable { .. } | TransportError::Interrupted { .. } => true,
        }
    }
}

#[cfg(test)]
pub(crate) mod fake {
    //! An in-memory channel, so the logic above can be tested without a
    //! socket: the fake can also misbehave in the specific ways a real CDN
    //! does — ignore a range request, cut a body in half, hand back a blob
    //! whose bytes do not match its name.

    use std::collections::HashMap;
    use std::io::Cursor;
    use std::sync::Mutex;

    use super::*;

    #[derive(Default)]
    pub struct FakeChannel {
        /// url → body.
        pub objects: HashMap<String, Vec<u8>>,
        /// url → ETag, when the channel offers one.
        pub etags: HashMap<String, String>,
        /// Serve at most this many bytes per `open`, then report the body as
        /// cut off. `None` serves everything.
        pub cut_after: Option<u64>,
        /// Pretend not to understand `Range`: answer every request with the
        /// whole object from zero.
        pub ignore_range: bool,
        /// Every url passed to `open`, in order — so a test can prove a file
        /// was not fetched twice, or was resumed rather than restarted.
        pub opened: Mutex<Vec<(String, u64)>>,
        /// Every url passed to `get`, with the ETag that was sent — so a test
        /// can prove a 327 KB manifest was not fetched to learn nothing.
        pub got: Mutex<Vec<(String, Option<String>)>>,
    }

    impl FakeChannel {
        pub fn opens(&self) -> Vec<(String, u64)> {
            self.opened.lock().unwrap().clone()
        }

        pub fn gets(&self) -> Vec<(String, Option<String>)> {
            self.got.lock().unwrap().clone()
        }
    }

    impl UpdateTransport for FakeChannel {
        fn get(&self, url: &str, if_none_match: Option<&str>) -> Result<Document, TransportError> {
            self.got
                .lock()
                .unwrap()
                .push((url.to_owned(), if_none_match.map(str::to_owned)));
            let body = self
                .objects
                .get(url)
                .ok_or_else(|| TransportError::Status {
                    url: url.to_owned(),
                    code: 404,
                })?;
            let etag = self.etags.get(url).cloned();
            if let (Some(sent), Some(have)) = (if_none_match, etag.as_deref()) {
                if sent == have {
                    return Ok(Document::NotModified);
                }
            }
            Ok(Document::Body {
                bytes: body.clone(),
                etag,
            })
        }

        fn open(&self, url: &str, from: u64) -> Result<Body, TransportError> {
            self.opened.lock().unwrap().push((url.to_owned(), from));
            let body = self
                .objects
                .get(url)
                .ok_or_else(|| TransportError::Status {
                    url: url.to_owned(),
                    code: 404,
                })?;
            let total = body.len() as u64;
            let offset = if self.ignore_range {
                0
            } else {
                from.min(total)
            };
            let mut slice = body[offset as usize..].to_vec();
            if let Some(cut) = self.cut_after {
                slice.truncate(cut as usize);
            }
            Ok(Body {
                offset,
                total: Some(total),
                read: Box::new(Cursor::new(slice)),
            })
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_missing_blob_is_not_worth_retrying_but_a_broken_pipe_is() {
        assert!(!TransportError::Status {
            url: "u".into(),
            code: 404
        }
        .is_retryable());
        assert!(TransportError::Status {
            url: "u".into(),
            code: 503
        }
        .is_retryable());
        assert!(TransportError::Status {
            url: "u".into(),
            code: 429
        }
        .is_retryable());
        assert!(TransportError::Interrupted {
            url: "u".into(),
            why: "eof".into()
        }
        .is_retryable());
    }
}
