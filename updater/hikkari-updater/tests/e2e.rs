//! The whole update path, end to end, through the real binary and real HTTP:
//! publish a release → check → stage → swap → trial → confirm or roll back.
//!
//! Runs on the Windows CI runner as well, which is where the swap's rename
//! semantics actually matter.

use std::collections::HashMap;
use std::fs;
use std::io::{BufRead, BufReader, Write};
use std::net::{TcpListener, TcpStream};
use std::path::{Path, PathBuf};
use std::process::Command;
use std::sync::atomic::{AtomicU32, Ordering};
use std::sync::{Arc, Mutex};

use nekko_updater::channel::VersionProbe;
use nekko_updater::manifest::{Channel, Manifest, Platform, Version};
use nekko_updater::{scan_tree, signature};
use serde_json::Value;

// ---------------------------------------------------------------------------
// Scene

struct Tmp(PathBuf);

impl Tmp {
    fn new(label: &str) -> Tmp {
        static N: AtomicU32 = AtomicU32::new(0);
        let path = std::env::temp_dir().join(format!(
            "hikkari-e2e-{label}-{}-{}",
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

fn write_tree(dir: &Path, files: &[(&str, &str)]) {
    for (path, body) in files {
        let path = dir.join(path);
        fs::create_dir_all(path.parent().unwrap()).unwrap();
        fs::write(path, body).unwrap();
    }
}

fn read(path: &Path) -> String {
    fs::read_to_string(path).unwrap()
}

const V1: &[(&str, &str)] = &[
    ("VERSION", "1.0.0"),
    ("nekkobox.exe", "gui 1.0.0"),
    ("sing-box.exe", "the same core in both releases"),
];

const V2: &[(&str, &str)] = &[
    ("VERSION", "1.1.0"),
    ("nekkobox.exe", "gui 1.1.0"),
    ("sing-box.exe", "the same core in both releases"),
    ("data/flutter_assets/new.bin", "only in 1.1.0"),
];

/// The same layout `nekko-manifest publish` writes: blobs, then the signed
/// manifest, then the probe.
fn publish(
    release: &Path,
    version: &str,
    minimum: &str,
    base_url: &str,
    out: &Path,
    key_hex: &str,
) {
    let signing = signing_key(key_hex);
    let scanned = scan_tree(release).unwrap();
    let manifest = Manifest {
        version: Version::parse(version).unwrap(),
        channel: Channel::Stable,
        platform: Platform::Windows,
        minimum_version: Version::parse(minimum).unwrap(),
        published_at: 1_761_800_000,
        base_url: base_url.to_owned(),
        files: scanned.files,
        links: scanned.links,
    };
    let envelope = signature::sign(&manifest, "test-1", &signing).unwrap();

    let blobs = out.join("blobs");
    fs::create_dir_all(&blobs).unwrap();
    for entry in &manifest.files {
        fs::copy(release.join(&entry.path), blobs.join(&entry.sha256)).unwrap();
    }
    let prefix = out.join("stable/windows");
    fs::create_dir_all(&prefix).unwrap();
    fs::write(prefix.join("manifest.json"), &envelope).unwrap();
    let probe = VersionProbe {
        version: manifest.version,
        platform: manifest.platform,
        channel: manifest.channel,
        published_at: manifest.published_at,
    };
    fs::write(prefix.join("latest.json"), probe.to_bytes().unwrap()).unwrap();
}

fn new_key() -> (String, String) {
    let signing = signature::generate_signing_key();
    (
        hex::encode(signing.to_bytes()),
        hex::encode(signing.verifying_key().to_bytes()),
    )
}

fn signing_key(private_hex: &str) -> ed25519_dalek::SigningKey {
    let bytes: [u8; 32] = hex::decode(private_hex).unwrap().try_into().unwrap();
    ed25519_dalek::SigningKey::from_bytes(&bytes)
}

// ---------------------------------------------------------------------------
// A channel server: static files, Range, ETag/304, and a request log.

struct ChannelServer {
    url: String,
    hits: Arc<Mutex<HashMap<String, u32>>>,
}

impl ChannelServer {
    fn serve(root: &Path) -> ChannelServer {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let url = format!("http://{}", listener.local_addr().unwrap());
        let hits = Arc::new(Mutex::new(HashMap::new()));
        let root = root.to_path_buf();
        let log = hits.clone();
        std::thread::spawn(move || {
            for stream in listener.incoming().flatten() {
                let root = root.clone();
                let log = log.clone();
                std::thread::spawn(move || handle(stream, &root, &log));
            }
        });
        ChannelServer { url, hits }
    }

    fn hits(&self, path: &str) -> u32 {
        *self.hits.lock().unwrap().get(path).unwrap_or(&0)
    }
}

fn handle(stream: TcpStream, root: &Path, log: &Mutex<HashMap<String, u32>>) {
    let mut reader = BufReader::new(stream.try_clone().unwrap());
    let mut line = String::new();
    if reader.read_line(&mut line).is_err() {
        return;
    }
    let path = line.split_whitespace().nth(1).unwrap_or("/").to_owned();
    let mut headers = HashMap::new();
    loop {
        let mut h = String::new();
        if reader.read_line(&mut h).unwrap_or(0) == 0 || h == "\r\n" {
            break;
        }
        if let Some((k, v)) = h.split_once(':') {
            headers.insert(k.trim().to_ascii_lowercase(), v.trim().to_owned());
        }
    }
    *log.lock().unwrap().entry(path.clone()).or_insert(0) += 1;

    let mut out = stream;
    let file = root.join(path.trim_start_matches('/'));
    let body = match (path.contains(".."), fs::read(&file)) {
        (false, Ok(body)) => body,
        _ => {
            let _ = out.write_all(
                b"HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n",
            );
            return;
        }
    };
    let etag = format!(
        "\"{}-{}\"",
        body.len(),
        body.iter().map(|&b| b as u32).sum::<u32>()
    );
    if headers.get("if-none-match") == Some(&etag) {
        let _ = write!(
            out,
            "HTTP/1.1 304 Not Modified\r\nETag: {etag}\r\nConnection: close\r\n\r\n"
        );
        return;
    }
    let from: usize = headers
        .get("range")
        .and_then(|r| r.strip_prefix("bytes="))
        .and_then(|r| r.trim_end_matches('-').parse().ok())
        .unwrap_or(0)
        .min(body.len());
    let status = if from > 0 {
        "206 Partial Content"
    } else {
        "200 OK"
    };
    let _ = write!(
        out,
        "HTTP/1.1 {status}\r\nContent-Length: {}\r\nContent-Range: bytes {from}-{}/{}\r\nETag: {etag}\r\nConnection: close\r\n\r\n",
        body.len() - from,
        body.len().saturating_sub(1),
        body.len()
    );
    let _ = out.write_all(&body[from..]);
}

// ---------------------------------------------------------------------------
// Running the binary

struct Updater {
    root: PathBuf,
    url: String,
    trust: String,
}

impl Updater {
    /// Runs one command; returns its events and whether it exited 0.
    fn run(&self, command: &str) -> (Vec<Value>, bool) {
        let out = Command::new(env!("CARGO_BIN_EXE_hikkari-updater"))
            .arg(command)
            .arg("--root")
            .arg(&self.root)
            .args(["--base-url", &self.url, "--trust", &self.trust])
            .output()
            .unwrap();
        let events = String::from_utf8(out.stdout)
            .unwrap()
            .lines()
            .map(|l| serde_json::from_str(l).unwrap())
            .collect();
        (events, out.status.success())
    }

    /// The single event of a given kind; panics with the full output otherwise.
    fn ok(&self, command: &str, event: &str) -> Value {
        let (events, success) = self.run(command);
        assert!(success, "{command} failed: {events:?}");
        events
            .into_iter()
            .find(|e| e["event"] == event)
            .unwrap_or_else(|| panic!("{command} emitted no {event}"))
    }

    fn app(&self) -> PathBuf {
        self.root.join("app")
    }
}

fn scene(label: &str, minimum: &str) -> (Tmp, ChannelServer, Updater) {
    let tmp = Tmp::new(label);
    let root = tmp.0.join("Hikkari");
    write_tree(&root.join("app"), V1);
    write_tree(&tmp.0.join("build-1.1.0"), V2);

    let channel_dir = tmp.0.join("channel");
    fs::create_dir_all(&channel_dir).unwrap();
    let server = ChannelServer::serve(&channel_dir);
    let (private, public) = new_key();
    publish(
        &tmp.0.join("build-1.1.0"),
        "1.1.0",
        minimum,
        &server.url,
        &channel_dir,
        &private,
    );

    let updater = Updater {
        root,
        url: server.url.clone(),
        trust: format!("test-1:{public}"),
    };
    (tmp, server, updater)
}

// ---------------------------------------------------------------------------

#[test]
fn a_release_goes_from_the_channel_to_a_confirmed_installation() {
    let (_tmp, server, up) = scene("happy", "0.0.0");

    assert_eq!(up.ok("status", "status")["installed"], "1.0.0");

    let checked = up.ok("check", "checked");
    assert_eq!(checked["result"], "available");
    assert_eq!(checked["version"], "1.1.0");
    assert!(
        !up.root.join(".staging/READY.json").exists(),
        "check downloads nothing"
    );

    let ready = up.ok("update", "ready");
    assert_eq!(ready["version"], "1.1.0");
    assert_eq!(ready["mandatory"], false);
    // Unchanged files come from the installation, not the network.
    let core = scan_tree(&up.app()).unwrap();
    let core_digest = &core
        .files
        .iter()
        .find(|f| f.path == "sing-box.exe")
        .unwrap()
        .sha256;
    assert_eq!(server.hits(&format!("/blobs/{core_digest}")), 0);
    // The running installation is untouched until the swap.
    assert_eq!(read(&up.app().join("VERSION")), "1.0.0");

    let applied = up.ok("apply", "applied");
    assert_eq!(
        (applied["from"].as_str(), applied["to"].as_str()),
        (Some("1.0.0"), Some("1.1.0"))
    );
    assert_eq!(read(&up.app().join("VERSION")), "1.1.0");
    assert_eq!(
        read(&up.app().join("data/flutter_assets/new.bin")),
        "only in 1.1.0"
    );
    assert_eq!(read(&up.root.join("app.old/VERSION")), "1.0.0");

    let trial = up.ok("recover", "recovered");
    assert_eq!(trial["result"], "on_trial");
    assert_eq!(trial["attempt"], 1);

    assert_eq!(up.ok("confirm", "confirmed")["version"], "1.1.0");
    assert!(!up.root.join("app.old").exists());
    assert_eq!(up.ok("recover", "recovered")["result"], "nothing");

    // Nothing new on the channel: the manifest is answered from the cache.
    let manifest_hits = server.hits("/stable/windows/manifest.json");
    assert_eq!(up.ok("check", "checked")["result"], "up_to_date");
    assert_eq!(server.hits("/stable/windows/manifest.json"), manifest_hits);
}

#[test]
fn a_version_that_never_confirms_is_rolled_back_and_not_offered_again() {
    let (_tmp, _server, up) = scene("rollback", "0.0.0");
    up.ok("update", "ready");
    up.ok("apply", "applied");

    assert_eq!(up.ok("recover", "recovered")["attempt"], 1);
    assert_eq!(up.ok("recover", "recovered")["attempt"], 2);
    let rolled = up.ok("recover", "recovered");
    assert_eq!(rolled["result"], "rolled_back");
    assert_eq!(read(&up.app().join("VERSION")), "1.0.0");

    let (events, success) = up.run("update");
    assert!(
        !success,
        "a refused version must not be staged again: {events:?}"
    );
    assert!(!up.root.join(".staging/READY.json").exists());
}

#[test]
fn the_supervisor_can_roll_back_a_version_that_did_not_start_at_all() {
    let (_tmp, _server, up) = scene("forced", "0.0.0");
    up.ok("update", "ready");
    up.ok("apply", "applied");

    let rolled = up.ok("rollback", "rolled_back");
    assert_eq!(rolled["from"], "1.0.0");
    assert_eq!(read(&up.app().join("VERSION")), "1.0.0");
    assert_eq!(up.ok("recover", "recovered")["result"], "nothing");
}

#[test]
fn a_release_below_whose_minimum_we_are_is_mandatory() {
    let (_tmp, _server, up) = scene("mandatory", "1.1.0");
    assert_eq!(up.ok("check", "checked")["result"], "mandatory");
    assert_eq!(up.ok("update", "ready")["mandatory"], true);
}

#[test]
fn a_manifest_signed_by_another_key_stages_nothing() {
    let (_tmp, _server, mut up) = scene("forged", "0.0.0");
    let (_, stranger) = new_key();
    up.trust = format!("test-1:{stranger}");

    let (events, success) = up.run("update");
    assert!(!success);
    assert_eq!(events.last().unwrap()["event"], "error");
    assert!(!up.root.join(".staging/READY.json").exists());
    assert_eq!(read(&up.app().join("VERSION")), "1.0.0");
}

#[test]
fn applying_with_nothing_staged_moves_nothing() {
    let (_tmp, _server, up) = scene("nothing", "0.0.0");
    let (_, success) = up.run("apply");
    assert!(!success);
    assert_eq!(read(&up.app().join("VERSION")), "1.0.0");
    assert!(!up.root.join("app.old").exists());
}
