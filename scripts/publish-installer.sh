#!/usr/bin/env bash
# Publish immutable installer, stable download URL, then live release metadata.
# Called by each release workflow only after its signed update channel is live.
set -euo pipefail
INSTALLER="${1:?installer file required}"
VERSION="${2:?version required}"
PLATFORM="${3:?platform required}"
: "${HIKKARI_UPDATE_URL:?}" "${HIKKARI_DEPLOY_HOST:?}"
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ && -s "$INSTALLER" ]] || exit 2
case "$PLATFORM" in
  macos) filename="hikkari-$VERSION.pkg"; alias="hikkari.pkg" ;;
  windows) filename="hikkari-setup-$VERSION.exe"; alias="hikkari-setup.exe" ;;
  *) exit 2 ;;
esac
base="${HIKKARI_UPDATE_URL%/}"
deploy_root="${HIKKARI_DEPLOY_ROOT:-}"
remote="${HIKKARI_DEPLOY_HOST}:${deploy_root:+${deploy_root%/}/}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
curl -fsS --retry 3 --max-time 30 "$base/stable/$PLATFORM/latest.json" > "$tmp/channel.json"
# Never replace the download with an older release, including a late CI retry.
previous_code="$(curl -sS --retry 3 --max-time 30 -w '%{http_code}' "$base/download/latest-$PLATFORM.json" -o "$tmp/previous.json")"
case "$previous_code" in
  200) ;;
  404) printf '{}' > "$tmp/previous.json" ;;
  *) printf 'Cannot read previous installer: HTTP %s\n' "$previous_code" >&2; exit 1 ;;
esac
python3 - "$tmp" "$VERSION" <<'PY'
import json, pathlib, sys
root, version = pathlib.Path(sys.argv[1]), sys.argv[2]
if json.loads((root/'channel.json').read_text()).get('version') != version:
    raise SystemExit('installer does not match the live signed update channel')
previous = json.loads((root/'previous.json').read_text()).get('version')
if previous and tuple(map(int, previous.split('.'))) > tuple(map(int, version.split('.'))):
    raise SystemExit('refusing to replace a newer installer')
PY
mkdir "$tmp/download"
cp "$INSTALLER" "$tmp/download/$filename"
python3 - "$tmp/download" "$filename" "$VERSION" "$PLATFORM" "$base" <<'PY'
import hashlib, json, pathlib, sys
root, filename, version, platform, base = sys.argv[1:]
p = pathlib.Path(root, filename)
digest = hashlib.sha256()
with p.open('rb') as source:
    for chunk in iter(lambda: source.read(1024 * 1024), b''):
        digest.update(chunk)
meta = dict(version=version, url=f'{base}/download/{filename}', sha256=digest.hexdigest(), size=p.stat().st_size)
pathlib.Path(root, f'latest-{platform}.json').write_text(json.dumps(meta)+'\n')
PY
rsync_args=(-a --delay-updates -e 'ssh -o BatchMode=yes')
rsync "${rsync_args[@]}" "$tmp/download/" "$remote"download/ --include "$filename" --exclude '*'
curl -fsS --retry 3 --max-time 300 "$base/download/$filename" -o "$tmp/public-installer"
python3 - "$tmp" "$PLATFORM" <<'PY'
import hashlib, json, pathlib, sys
root = pathlib.Path(sys.argv[1])
meta = json.loads((root/'download'/f'latest-{sys.argv[2]}.json').read_text())
p = root/'public-installer'
digest = hashlib.sha256()
with p.open('rb') as source:
    for chunk in iter(lambda: source.read(1024 * 1024), b''):
        digest.update(chunk)
if p.stat().st_size != meta['size'] or digest.hexdigest() != meta['sha256']:
    raise SystemExit('public installer differs from the tested artifact')
PY
cp "$INSTALLER" "$tmp/download/$alias"
rsync "${rsync_args[@]}" --checksum "$tmp/download/$alias" "$remote"download/
curl -fsS --retry 3 --max-time 300 "$base/download/$alias" -o "$tmp/public-alias"
cmp "$INSTALLER" "$tmp/public-alias"
rsync "${rsync_args[@]}" "$tmp/download/latest-$PLATFORM.json" "$remote"download/
curl -fsS --retry 3 --max-time 30 "$base/download/latest-$PLATFORM.json" -o "$tmp/published.json"
cmp "$tmp/download/latest-$PLATFORM.json" "$tmp/published.json"
printf 'Published %s %s: %s/download/%s\n' "$PLATFORM" "$VERSION" "$base" "$alias"
