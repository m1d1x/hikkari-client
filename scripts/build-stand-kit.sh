#!/usr/bin/env bash
# build-stand-kit.sh — the matrix stand's kit for the hub's vantages.
#
#   bash scripts/build-stand-kit.sh OUT_DIR VERSION
#
# The hub measures relays from its observation points on the core users run
# (hikkari-control internal/stand, plans/2026-11-04-tspu-detection-research.md).
# A vantage is a Linux box, so the kit is that core and the client's own link
# parser, both linux-amd64, built from this tree:
#
#   OUT_DIR/blobs/<sha256>               sing-box-lx from our fork
#                                        m1d1x/sing-box-lx at SING_BOX_LX_TAG,
#                                        and engine/cmd/census-parse
#   OUT_DIR/stand/linux-amd64.json       {"version", "core", "files": {
#                                        "sing-box": {"sha256", "size"},
#                                        "census-parse": {"sha256", "size"}}}
#
# The layout is the update channel's (scripts/publish-update-channel.sh):
# blobs are shared, content-addressed, and the manifest names them by hash.
# "core" is how every row of the dataset names the core: the fork's tag, which
# already holds our patches, as hikkari-control `ops matrix` names the core it
# builds itself.
#
# Environment: SING_BOX_LX_TAG (required), as build-windows.yml pins it.
set -euo pipefail

OUT="${1:-}"
VERSION="${2:-}"
: "${SING_BOX_LX_TAG:?is not set}"
[[ -n "$OUT" && -n "$VERSION" ]] || { echo "usage: $0 OUT_DIR VERSION" >&2; exit 1; }

REPO="$(cd "$(dirname "$0")/.." && pwd)"
mkdir -p "$OUT/blobs" "$OUT/stand"
OUT="$(cd "$OUT" && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

git clone -q --depth 1 --branch "$SING_BOX_LX_TAG" https://github.com/m1d1x/sing-box-lx "$WORK/sbx"
(
	cd "$WORK/sbx"
	git submodule update -q --init --depth 1 submodules/gvisor submodules/sing-tun submodules/utls submodules/wireguard-go
	TAGS=$(make -s -f Makefile.lx lx-print-tags)
	CORE_VERSION="$(tr -d '[:space:]' < upstream.version)-${SING_BOX_LX_TAG#*-}"
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -tags "$TAGS" \
		-ldflags "-X 'github.com/sagernet/sing-box/constant.Version=$CORE_VERSION' -checklinkname=0 -s -w -buildid=" \
		-o "$WORK/sing-box" ./cmd/sing-box
)
(
	cd "$REPO/engine"
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -buildid=" \
		-o "$WORK/census-parse" ./cmd/census-parse
)

CORE_LABEL="sing-box-lx $SING_BOX_LX_TAG"

entry() {
	local f="$1" sum size
	sum="$(sha256sum "$f" | cut -d' ' -f1)"
	size="$(stat -c %s "$f")"
	cp "$f" "$OUT/blobs/$sum"
	printf '{"sha256":"%s","size":%s}' "$sum" "$size"
}

printf '{"version":"%s","core":"%s","files":{"sing-box":%s,"census-parse":%s}}\n' \
	"$VERSION" "$CORE_LABEL" "$(entry "$WORK/sing-box")" "$(entry "$WORK/census-parse")" \
	> "$OUT/stand/linux-amd64.json"
cat "$OUT/stand/linux-amd64.json"
