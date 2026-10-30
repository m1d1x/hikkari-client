//! The real transport behind `nekko_updater::transport::UpdateTransport`.
//!
//! Blocking `ureq`: the updater is a short-lived process that fetches one
//! thing at a time, and the staging code reads bodies with blocking I/O.
//!
//! When the tunnel is up, traffic from this process goes through it like any
//! other (the TUN captures it). `proxy` exists for the case where routing
//! sends it direct and the update host is blocked on the direct path.

use std::io::Read;
use std::time::Duration;

use nekko_updater::transport::{Body, Document, TransportError, UpdateTransport};

/// A probe or a manifest: small, so one deadline covers the whole exchange.
const DOCUMENT_TIMEOUT: Duration = Duration::from_secs(30);
/// Blob bodies can be large; only the gaps between reads are bounded.
const CONNECT_TIMEOUT: Duration = Duration::from_secs(15);
const READ_TIMEOUT: Duration = Duration::from_secs(30);
/// A manifest is ~300 KB. Anything far past that is not one, and reading it
/// into memory unbounded would let a broken mirror exhaust it.
const MAX_DOCUMENT: u64 = 16 * 1024 * 1024;

pub struct HttpChannel {
    agent: ureq::Agent,
}

impl HttpChannel {
    pub fn new(proxy: Option<&str>) -> Result<Self, String> {
        let mut builder = ureq::AgentBuilder::new()
            .timeout_connect(CONNECT_TIMEOUT)
            .timeout_read(READ_TIMEOUT)
            .user_agent(concat!("hikkari-updater/", env!("CARGO_PKG_VERSION")));
        if let Some(proxy) = proxy {
            let proxy = ureq::Proxy::new(proxy).map_err(|e| format!("bad proxy {proxy}: {e}"))?;
            builder = builder.proxy(proxy);
        }
        Ok(HttpChannel {
            agent: builder.build(),
        })
    }
}

impl UpdateTransport for HttpChannel {
    fn get(&self, url: &str, if_none_match: Option<&str>) -> Result<Document, TransportError> {
        let mut request = self.agent.get(url).timeout(DOCUMENT_TIMEOUT);
        if let Some(etag) = if_none_match {
            request = request.set("If-None-Match", etag);
        }
        let response = call(request, url)?;
        if response.status() == 304 {
            return Ok(Document::NotModified);
        }
        let etag = response.header("ETag").map(str::to_owned);
        let mut bytes = Vec::new();
        response
            .into_reader()
            .take(MAX_DOCUMENT + 1)
            .read_to_end(&mut bytes)
            .map_err(|e| TransportError::Interrupted {
                url: url.to_owned(),
                why: e.to_string(),
            })?;
        if bytes.len() as u64 > MAX_DOCUMENT {
            return Err(TransportError::Interrupted {
                url: url.to_owned(),
                why: format!("larger than {MAX_DOCUMENT} bytes — not a channel document"),
            });
        }
        Ok(Document::Body { bytes, etag })
    }

    fn open(&self, url: &str, from: u64) -> Result<Body, TransportError> {
        let mut request = self.agent.get(url);
        if from > 0 {
            request = request.set("Range", &format!("bytes={from}-"));
        }
        let response = call(request, url)?;
        // 200 to a Range request means the server sent the whole body; the
        // staging code starts over from offset 0 in that case.
        let (offset, total) = if response.status() == 206 {
            response
                .header("Content-Range")
                .and_then(parse_content_range)
                .unwrap_or((0, None))
        } else {
            (
                0,
                response
                    .header("Content-Length")
                    .and_then(|v| v.trim().parse().ok()),
            )
        };
        Ok(Body {
            offset,
            total,
            read: Box::new(response.into_reader()),
        })
    }
}

fn call(request: ureq::Request, url: &str) -> Result<ureq::Response, TransportError> {
    match request.call() {
        Ok(response) => Ok(response),
        Err(ureq::Error::Status(code, _)) => Err(TransportError::Status {
            url: url.to_owned(),
            code,
        }),
        Err(ureq::Error::Transport(e)) => Err(TransportError::Unreachable {
            url: url.to_owned(),
            why: e.to_string(),
        }),
    }
}

/// `bytes 100-199/200` → `(100, Some(200))`; `/*` leaves the total unknown.
fn parse_content_range(value: &str) -> Option<(u64, Option<u64>)> {
    let range = value.trim().strip_prefix("bytes ")?;
    let (span, total) = range.split_once('/')?;
    let (start, _end) = span.split_once('-')?;
    Some((start.trim().parse().ok()?, total.trim().parse().ok()))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_content_range_says_where_the_body_starts() {
        assert_eq!(
            parse_content_range("bytes 100-199/200"),
            Some((100, Some(200)))
        );
        assert_eq!(parse_content_range("bytes 512-1023/*"), Some((512, None)));
    }

    #[test]
    fn an_unparseable_content_range_does_not_become_a_guessed_offset() {
        assert_eq!(parse_content_range("items 1-2/3"), None);
        assert_eq!(parse_content_range("bytes abc-def/200"), None);
        assert_eq!(parse_content_range(""), None);
    }
}
