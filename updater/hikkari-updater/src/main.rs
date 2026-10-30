//! `hikkari-updater` — run by the Hikkari service, as SYSTEM, to keep the
//! installation current. One command per invocation; every outcome is one
//! JSON object per line on stdout, diagnostics go to stderr.
//!
//! ```text
//!   <root>\                         Program Files\Hikkari
//!       hikkari-updater.exe         this binary — lives OUTSIDE app\
//!       app\                        the installation; app\VERSION names it
//!       app.old\                    previous version while the new one is on trial
//!       .staging\                   tree\, parts\, READY / PENDING / REFUSED, cache
//! ```
//!
//! ```text
//!   status     installed version, staged version, version on trial
//!   check      ask the channel; nothing is downloaded
//!   update     check, then stage the new version beside the installation
//!   apply      swap the staged version in        (nothing inside app\ may run)
//!   recover    first thing at every start: repair / count the trial / roll back
//!   confirm    the new version works: drop app.old
//!   rollback   the new version did not come up at all: put app.old back now
//! ```
//!
//! The caller (the service) serialises invocations: two of these running at
//! once against one root would race on the staging directory.
//!
//! All decisions — what to download, what is trusted, when to roll back — are
//! `nekko_updater`'s. This binary only says where things are and how bytes
//! travel.

mod http;

use std::fs;
use std::io::Write;
use std::path::{Path, PathBuf};
use std::process::ExitCode;

use nekko_updater::check::{poll, CachedManifest, ChannelConfig, Poll};
use nekko_updater::manifest::{Channel, Platform, UpdateDecision, Version};
use nekko_updater::stage::{self, Control, Layout, Progress};
use nekko_updater::swap::{self, Recovery};
use nekko_updater::TrustedKey;
use serde_json::{json, Value};

/// The installation directory, relative to the root.
const APP_DIR: &str = "app";
/// The staging directory — a sibling of `app\`, never inside it.
const STAGING_DIR: &str = ".staging";
/// Plain-text version of the installed tree, shipped inside it (and therefore
/// covered by the manifest like every other file). Read from the tree rather
/// than compiled into this binary: the updater lives outside `app\` and is
/// replaced far less often than what it updates.
const VERSION_FILE: &str = "VERSION";
/// The last verified manifest envelope and its ETag, so an unchanged channel
/// answers 304 instead of resending ~300 KB.
const CACHE_ENVELOPE: &str = "manifest.envelope";
const CACHE_ETAG: &str = "manifest.etag";

/// Every Hikkari manifest is a Windows manifest — also when the tests run this
/// binary on Linux.
const PLATFORM: Platform = Platform::Windows;

/// Channel root and trusted keys are fixed at build time:
///
/// * `HIKKARI_UPDATE_URL`  — e.g. `https://update.example.com`
/// * `HIKKARI_UPDATE_KEYS` — `id:hex[,id:hex]`, current key and next one
///
/// A release build without them can still recover, confirm and roll back; it
/// just has nowhere to look for updates and says so.
const BUILT_URL: Option<&str> = option_env!("HIKKARI_UPDATE_URL");
const BUILT_KEYS: Option<&str> = option_env!("HIKKARI_UPDATE_KEYS");

/// Error code (and message prefix) of a build without an update channel.
const NO_CHANNEL: &str = "no_channel";

const USAGE: &str = "usage: hikkari-updater <status|check|update|apply|recover|confirm|rollback> \
                     [--root DIR] [--channel stable|beta] [--proxy URL]";

struct Options {
    root: PathBuf,
    channel: Channel,
    proxy: Option<String>,
    base_url: Option<String>,
    trusted: Vec<TrustedKey>,
}

impl Options {
    fn layout(&self) -> Layout {
        Layout::new(self.root.join(APP_DIR), self.root.join(STAGING_DIR))
    }

    fn channel_config(&self) -> Result<ChannelConfig, String> {
        let base_url = self.base_url.clone().ok_or(format!(
            "{NO_CHANNEL}: built without HIKKARI_UPDATE_URL — this binary has no update channel"
        ))?;
        if self.trusted.is_empty() {
            return Err(format!(
                "{NO_CHANNEL}: built without HIKKARI_UPDATE_KEYS — no manifest could be trusted"
            ));
        }
        Ok(ChannelConfig {
            base_url,
            platform: PLATFORM,
            channel: self.channel,
            trusted: self.trusted.clone(),
        })
    }
}

fn main() -> ExitCode {
    log::set_logger(&StderrLog).ok();
    log::set_max_level(log::LevelFilter::Info);

    let args: Vec<String> = std::env::args().skip(1).collect();
    let Some((command, rest)) = args.split_first() else {
        eprintln!("{USAGE}");
        return ExitCode::from(2);
    };
    let opts = match parse_options(rest) {
        Ok(opts) => opts,
        Err(why) => {
            eprintln!("hikkari-updater: {why}\n{USAGE}");
            return ExitCode::from(2);
        }
    };

    let result = match command.as_str() {
        "status" => status(&opts),
        "check" => check(&opts).map(|_| ()),
        "update" => update(&opts),
        "apply" => apply(&opts),
        "recover" => recover(&opts),
        "confirm" => confirm(&opts),
        "rollback" => rollback(&opts),
        other => {
            eprintln!("hikkari-updater: unknown command {other}\n{USAGE}");
            return ExitCode::from(2);
        }
    };
    match result {
        Ok(()) => ExitCode::SUCCESS,
        Err(message) => {
            // A build with no channel is not failing; the service shows it
            // as "updates unavailable" rather than as an error.
            let code = if message.starts_with(NO_CHANNEL) {
                NO_CHANNEL
            } else {
                "failed"
            };
            emit(json!({ "event": "error", "command": command, "code": code, "message": message }));
            ExitCode::FAILURE
        }
    }
}

fn parse_options(args: &[String]) -> Result<Options, String> {
    let mut opts = Options {
        root: default_root()?,
        channel: Channel::Stable,
        proxy: None,
        base_url: BUILT_URL.map(str::to_owned),
        trusted: match BUILT_KEYS {
            Some(spec) => parse_keys(spec)?,
            None => Vec::new(),
        },
    };
    let mut it = args.iter();
    while let Some(flag) = it.next() {
        let mut value = || {
            it.next()
                .cloned()
                .ok_or_else(|| format!("{flag} needs a value"))
        };
        match flag.as_str() {
            "--root" => opts.root = PathBuf::from(value()?),
            "--proxy" => opts.proxy = Some(value()?),
            "--channel" => {
                opts.channel = match value()?.as_str() {
                    "stable" => Channel::Stable,
                    "beta" => Channel::Beta,
                    other => return Err(format!("unknown channel {other}")),
                }
            }
            // Pointing the updater at another channel or key is for tests and
            // local channels only. A release binary runs as SYSTEM and its
            // trust must be exactly what it was built with.
            #[cfg(debug_assertions)]
            "--base-url" => opts.base_url = Some(value()?),
            #[cfg(debug_assertions)]
            "--trust" => opts.trusted = parse_keys(&value()?)?,
            other => return Err(format!("unknown option {other}")),
        }
    }
    Ok(opts)
}

/// The directory holding this executable: the updater ships beside `app\`.
fn default_root() -> Result<PathBuf, String> {
    let exe = std::env::current_exe().map_err(|e| format!("finding this executable: {e}"))?;
    exe.parent()
        .map(Path::to_path_buf)
        .ok_or_else(|| "this executable has no parent directory".to_owned())
}

/// `id:hex[,id:hex]`.
fn parse_keys(spec: &str) -> Result<Vec<TrustedKey>, String> {
    spec.split(',')
        .filter(|s| !s.trim().is_empty())
        .map(|pair| {
            let (id, hex) = pair
                .split_once(':')
                .ok_or_else(|| format!("key {pair:?} is not id:hex"))?;
            TrustedKey::from_hex(id.trim(), hex).map_err(|e| format!("key {id}: {e}"))
        })
        .collect()
}

fn installed_version(layout: &Layout) -> Result<Version, String> {
    let path = layout.install().join(VERSION_FILE);
    let text = fs::read_to_string(&path).map_err(|e| format!("reading {}: {e}", path.display()))?;
    Version::parse(text.trim()).map_err(|e| format!("{}: {e}", path.display()))
}

// ---------------------------------------------------------------------------
// Commands

fn status(opts: &Options) -> Result<(), String> {
    let layout = opts.layout();
    let pending = swap::read_pending(&layout);
    // Whether the next `recover` renames directories (a rollback), as
    // opposed to only counting a start. The service stops the core around
    // a recover only in the first case: a counted start must not drop the
    // tunnel.
    let recover_moves = pending
        .as_ref()
        .is_some_and(|p| p.attempts + 1 > swap::MAX_ATTEMPTS);
    emit(json!({
        "event": "status",
        "installed": installed_version(&layout)?.to_string(),
        "ready": stage::read_ready(&layout).map(|r| r.version.to_string()),
        "pending": pending,
        "recover_moves": recover_moves,
    }));
    Ok(())
}

/// What a check found that is worth acting on.
struct Found {
    manifest: nekko_updater::Manifest,
    mandatory: bool,
}

fn check(opts: &Options) -> Result<Option<Found>, String> {
    let layout = opts.layout();
    let installed = installed_version(&layout)?;
    let cfg = opts.channel_config()?;
    let transport = http::HttpChannel::new(opts.proxy.as_deref())?;
    let cached = read_cache(&layout);

    let found = poll(&transport, &cfg, installed, cached.as_ref()).map_err(|e| e.to_string())?;
    let (result, found) = match found {
        Poll::UpToDate => ("up_to_date", None),
        Poll::ChannelBehind => ("channel_behind", None),
        Poll::WrongTarget => ("wrong_target", None),
        Poll::Found {
            manifest,
            decision,
            envelope,
            etag,
        } => {
            write_cache(&layout, &envelope, etag.as_deref());
            match decision {
                UpdateDecision::UpToDate => ("up_to_date", None),
                UpdateDecision::Stale => ("channel_behind", None),
                UpdateDecision::Available => (
                    "available",
                    Some(Found {
                        manifest,
                        mandatory: false,
                    }),
                ),
                UpdateDecision::Mandatory => (
                    "mandatory",
                    Some(Found {
                        manifest,
                        mandatory: true,
                    }),
                ),
            }
        }
    };
    emit(json!({
        "event": "checked",
        "installed": installed.to_string(),
        "result": result,
        "version": found.as_ref().map(|f| f.manifest.version.to_string()),
    }));
    Ok(found)
}

fn update(opts: &Options) -> Result<(), String> {
    let Some(found) = check(opts)? else {
        return Ok(());
    };
    let layout = opts.layout();
    let transport = http::HttpChannel::new(opts.proxy.as_deref())?;

    // One line per whole percent: enough for a progress bar, few enough that
    // the service is not parsing thousands of lines for one download.
    let mut last_percent = None;
    let mut report = |p: &Progress| {
        let percent = (p.bytes_done * 100)
            .checked_div(p.bytes_total)
            .unwrap_or(100);
        if last_percent != Some(percent) {
            last_percent = Some(percent);
            emit(json!({
                "event": "progress",
                "bytes_done": p.bytes_done,
                "bytes_total": p.bytes_total,
                "files_done": p.files_done,
                "files_total": p.files_total,
            }));
        }
        Control::Continue
    };
    let ready = stage::prepare(&found.manifest, &layout, &transport, &mut report)
        .map_err(|e| e.to_string())?;
    emit(json!({
        "event": "ready",
        "version": ready.version.to_string(),
        "mandatory": found.mandatory,
    }));
    Ok(())
}

fn apply(opts: &Options) -> Result<(), String> {
    let layout = opts.layout();
    let installed = installed_version(&layout)?;
    let applied = swap::apply(&layout, installed).map_err(|e| e.to_string())?;
    emit(json!({
        "event": "applied",
        "from": applied.from.to_string(),
        "to": applied.to.to_string(),
    }));
    Ok(())
}

fn recover(opts: &Options) -> Result<(), String> {
    let layout = opts.layout();
    let event = match swap::recover(&layout).map_err(|e| e.to_string())? {
        Recovery::Nothing => json!({ "event": "recovered", "result": "nothing" }),
        Recovery::Repaired => json!({ "event": "recovered", "result": "repaired" }),
        Recovery::OnTrial { pending } => json!({
            "event": "recovered",
            "result": "on_trial",
            "from": pending.from.to_string(),
            "to": pending.to.to_string(),
            "attempt": pending.attempts,
            "of": swap::MAX_ATTEMPTS,
        }),
        Recovery::RolledBack { from, to } => json!({
            "event": "recovered",
            "result": "rolled_back",
            "from": from.to_string(),
            "to": to.to_string(),
        }),
    };
    emit(event);
    Ok(())
}

fn confirm(opts: &Options) -> Result<(), String> {
    let pending = swap::confirm(&opts.layout()).map_err(|e| e.to_string())?;
    emit(json!({
        "event": "confirmed",
        "version": pending.map(|p| p.to.to_string()),
    }));
    Ok(())
}

/// For the supervisor: the new version did not even come up, so waiting for
/// the start counter to run out would just be two more failed launches.
fn rollback(opts: &Options) -> Result<(), String> {
    let layout = opts.layout();
    let pending = swap::read_pending(&layout).ok_or("no version is on trial")?;
    match swap::rollback(&layout, &pending).map_err(|e| e.to_string())? {
        Recovery::RolledBack { from, to } => emit(json!({
            "event": "rolled_back",
            "from": from.to_string(),
            "to": to.to_string(),
        })),
        other => return Err(format!("rollback ended in {other:?}")),
    }
    Ok(())
}

// ---------------------------------------------------------------------------
// Plumbing

fn read_cache(layout: &Layout) -> Option<CachedManifest> {
    let envelope = fs::read(layout.work().join(CACHE_ENVELOPE)).ok()?;
    let etag = fs::read_to_string(layout.work().join(CACHE_ETAG)).ok();
    Some(CachedManifest { envelope, etag })
}

/// Best effort: a cache that cannot be written costs one full download next
/// time, never correctness — the cached bytes are re-verified on every use.
fn write_cache(layout: &Layout, envelope: &[u8], etag: Option<&str>) {
    let dir = layout.work();
    if fs::create_dir_all(dir).is_err() {
        return;
    }
    let _ = fs::write(dir.join(CACHE_ENVELOPE), envelope);
    match etag {
        Some(etag) => {
            let _ = fs::write(dir.join(CACHE_ETAG), etag);
        }
        None => {
            let _ = fs::remove_file(dir.join(CACHE_ETAG));
        }
    }
}

fn emit(event: Value) {
    let mut out = std::io::stdout().lock();
    let _ = writeln!(out, "{event}");
    let _ = out.flush();
}

/// `nekko_updater` narrates itself through `log`; the service keeps stderr in
/// its own log file.
struct StderrLog;

impl log::Log for StderrLog {
    fn enabled(&self, metadata: &log::Metadata) -> bool {
        metadata.level() <= log::Level::Info
    }

    fn log(&self, record: &log::Record) {
        if self.enabled(record.metadata()) {
            eprintln!("{} {}: {}", record.level(), record.target(), record.args());
        }
    }

    fn flush(&self) {}
}
