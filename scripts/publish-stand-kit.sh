#!/usr/bin/env bash
# publish-stand-kit.sh — put the stand's kit (scripts/build-stand-kit.sh) on
# the update server, beside the channel.
#
#   bash scripts/publish-stand-kit.sh KIT_DIR
#
# Blobs first, the manifest last: a vantage reads the manifest and fetches
# what it names, so the manifest must not exist before its blobs. No --delete:
# blobs/ is shared with the update channel.
#
# Environment (all required), as for publish-update-channel.sh:
#   HIKKARI_UPDATE_URL   public base URL
#   HIKKARI_DEPLOY_HOST  ssh destination (deploy@host), key already in ssh-agent
set -euo pipefail

KIT="${1:-}"
[[ -d "$KIT/blobs" && -f "$KIT/stand/linux-amd64.json" ]] || { echo "usage: $0 KIT_DIR (a build-stand-kit.sh tree)" >&2; exit 1; }
: "${HIKKARI_UPDATE_URL:?is not set}" "${HIKKARI_DEPLOY_HOST:?is not set}"
BASE_URL="${HIKKARI_UPDATE_URL%/}"
RSYNC=(rsync -a --delay-updates --human-readable -e "ssh -o BatchMode=yes")

"${RSYNC[@]}" --include '*/' --exclude '*' "$KIT/" "$HIKKARI_DEPLOY_HOST:"
"${RSYNC[@]}" "$KIT/blobs/" "$HIKKARI_DEPLOY_HOST:blobs/"
"${RSYNC[@]}" "$KIT/stand/linux-amd64.json" "$HIKKARI_DEPLOY_HOST:stand/linux-amd64.json"

# Prove it from the public edge: the manifest is this one and its blobs come.
want="$(cat "$KIT/stand/linux-amd64.json")"
got="$(curl -fsS --max-time 20 "$BASE_URL/stand/linux-amd64.json" || true)"
[[ "$got" == "$want" ]] || { echo "the edge serves another stand manifest: ${got:-<nothing>}" >&2; exit 1; }
for sum in $(grep -o '"sha256":"[0-9a-f]*"' <<<"$want" | cut -d'"' -f4); do
	code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 60 "$BASE_URL/blobs/$sum" || echo 000)"
	[[ "$code" == 200 ]] || { echo "blob $sum came back $code" >&2; exit 1; }
done
echo "published: $BASE_URL/stand/linux-amd64.json"
