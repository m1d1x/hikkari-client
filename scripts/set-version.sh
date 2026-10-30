#!/usr/bin/env bash
# set-version.sh — stamp one release version into every place that carries it.
#
#   bash scripts/set-version.sh 1.2.3 [BUILD]
#
# The version lives in three files and they must agree: app/pubspec.yaml (the
# GUI and, through the bundle step, app\VERSION — what the updater reports as
# installed), updater/Cargo.toml (the updater binaries) and updater/Cargo.lock
# (so `cargo build --locked` still passes). The Go binaries take it from
# pubspec via -ldflags in the workflow. A channel that advertises a version the
# installed tree does not report is an update every client downloads and then
# decides it already has — so this is the only writer.
#
# BUILD is pubspec's +N counter (Windows file version); it is not part of the
# update version, which is strictly MAJOR.MINOR.PATCH (manifest::Version).
set -euo pipefail

VERSION="${1:-}"
BUILD="${2:-1}"
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] ||
	{ echo "set-version: expected MAJOR.MINOR.PATCH, got '$VERSION'" >&2; exit 2; }
[[ "$BUILD" =~ ^[0-9]+$ ]] ||
	{ echo "set-version: build number must be an integer, got '$BUILD'" >&2; exit 2; }

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# pubspec: the one `version:` line at column 0.
sed -i.bak -E "s/^version:[[:space:]].*/version: $VERSION+$BUILD/" app/pubspec.yaml

# Cargo.toml: `version` inside [workspace.package] only.
awk -v v="$VERSION" '
	/^\[/ { section = $0 }
	section == "[workspace.package]" && /^version[[:space:]]*=/ { $0 = "version = \"" v "\"" }
	{ print }
' updater/Cargo.toml >updater/Cargo.toml.new && mv updater/Cargo.toml.new updater/Cargo.toml

# Cargo.lock: the entries of the two workspace crates, nothing from crates.io.
awk -v v="$VERSION" '
	/^name = "(nekko-updater|hikkari-updater)"$/ { ours = 1; print; next }
	ours && /^version = / { $0 = "version = \"" v "\""; ours = 0 }
	{ print }
' updater/Cargo.lock >updater/Cargo.lock.new && mv updater/Cargo.lock.new updater/Cargo.lock

rm -f app/pubspec.yaml.bak

# Read back: a stamp that silently matched nothing is the failure this guards.
grep -q "^version: $VERSION+$BUILD\$" app/pubspec.yaml ||
	{ echo "set-version: pubspec.yaml was not updated" >&2; exit 1; }
[[ "$(grep -c "^version = \"$VERSION\"\$" updater/Cargo.toml)" == 1 ]] ||
	{ echo "set-version: updater/Cargo.toml was not updated" >&2; exit 1; }
[[ "$(grep -A1 -E '^name = "(nekko|hikkari)-updater"$' updater/Cargo.lock | grep -c "^version = \"$VERSION\"\$")" == 2 ]] ||
	{ echo "set-version: updater/Cargo.lock was not updated" >&2; exit 1; }

echo "version $VERSION (build $BUILD) stamped into pubspec.yaml, Cargo.toml, Cargo.lock"
