#!/usr/bin/env bash
# publish-update-channel.sh — put a signed channel tree on the update server.
#
#   bash scripts/publish-update-channel.sh CHANNEL_DIR VERSION PLATFORM
#
# CHANNEL_DIR is what `nekko-manifest publish --out` wrote (the release jobs in
# .github/workflows/build-{windows,macos}.yml make it on the runner where the
# tree it signs was built); PLATFORM is windows or macos:
#
#   blobs/<sha256>                       every file body, content-addressed,
#                                        shared by every platform and version
#   stable/PLATFORM/manifest.json        the signed envelope
#   stable/PLATFORM/manifest-X.Y.Z.json  the same bytes, kept for rollback
#   stable/PLATFORM/latest.json          the cheap probe clients poll
#
# The server only has to serve that directory as static files over HTTPS; an
# update must keep working when anything else on the box is down.
#
# The deploy key on the server is pinned to rsync inside the channel root
# (authorized_keys: command="/usr/bin/rrsync /srv/hikkari/update",restrict),
# so the CI secret can write channel files and nothing else; remote paths
# below are relative to that root.
#
# Environment (all required):
#   HIKKARI_UPDATE_URL   public base URL, the one baked into hikkari-updater
#   HIKKARI_DEPLOY_HOST  ssh destination (deploy@host), key already in ssh-agent
set -euo pipefail

CHANNEL_DIR="${1:-}"
VERSION="${2:-}"
PLATFORM="${3:-}"

say() { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
die() {
	printf '\033[1;31mfail:\033[0m %s\n' "$*" >&2
	exit 1
}

[[ -n "$VERSION" && ( "$PLATFORM" == windows || "$PLATFORM" == macos ) ]] ||
	die "usage: $0 CHANNEL_DIR VERSION windows|macos"
[[ -d "$CHANNEL_DIR/blobs" && -f "$CHANNEL_DIR/stable/$PLATFORM/latest.json" ]] ||
	die "$CHANNEL_DIR is not a $PLATFORM channel tree"
: "${HIKKARI_UPDATE_URL:?is not set}" "${HIKKARI_DEPLOY_HOST:?is not set}"

BASE_URL="${HIKKARI_UPDATE_URL%/}"
HOST="$HIKKARI_DEPLOY_HOST"
PREFIX="stable/$PLATFORM"

# ⚠️ Order is load-bearing: bodies, then manifests, then the probe. The probe
# is the only thing that makes a client fetch, so it is the last to exist; a
# manifest must not be readable before the blobs it names. One rsync of the
# whole tree would not guarantee that order, so these are three.
#
# ⚠️ NO --delete: blobs/ is shared by every published version, and a client on
# the previous release (or rolling back to it) resolves its files from the
# same directory. The tree only grows; pruning is a separate, deliberate act.
RSYNC=(rsync -a --delay-updates --human-readable -e "ssh -o BatchMode=yes")

say "publishing hikkari $VERSION for $PLATFORM to $HOST ($BASE_URL)"

# rsync creates only the last path component; stable/ must exist first.
"${RSYNC[@]}" --include '*/' --exclude '*' "$CHANNEL_DIR/" "$HOST:"
say "1/3 blobs"
"${RSYNC[@]}" "$CHANNEL_DIR/blobs/" "$HOST:blobs/"
say "2/3 manifests"
"${RSYNC[@]}" --exclude latest.json "$CHANNEL_DIR/$PREFIX/" "$HOST:$PREFIX/"
say "3/3 probe"
"${RSYNC[@]}" "$CHANNEL_DIR/$PREFIX/latest.json" "$HOST:$PREFIX/latest.json"

# Prove it from the public edge, not from "rsync exited 0": the claim is that a
# client polling this URL is told about this version and can fetch its files.
say "verifying $BASE_URL/$PREFIX/latest.json"
probe="$(curl -fsS --max-time 20 "$BASE_URL/$PREFIX/latest.json" || true)"
reported="$(printf '%s' "$probe" | sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
[[ "$reported" == "$VERSION" ]] ||
	die "the channel reports version '${reported:-<none>}', expected '$VERSION'
     what came back: ${probe:-<nothing>}"

manifest_code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 20 "$BASE_URL/$PREFIX/manifest.json" || echo 000)"
[[ "$manifest_code" == 200 ]] || die "manifest.json came back $manifest_code"
first_blob="$(find "$CHANNEL_DIR/blobs" -type f -exec basename {} \; | head -1)"
blob_code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 20 "$BASE_URL/blobs/$first_blob" || echo 000)"
[[ "$blob_code" == 200 ]] || die "blob $first_blob came back $blob_code from $BASE_URL/blobs/"

say "published: $BASE_URL serves hikkari $VERSION for $PLATFORM"
say "rollback is a file copy on the box (as root), no key and no rebuild:"
say "  cd /srv/hikkari/update/$PREFIX && cp manifest-<older>.json manifest.json"
