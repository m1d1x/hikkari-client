#!/bin/sh
# Builds the Go engine as a c-shared dylib and drops it into the app bundle's
# Contents/Frameworks, where core/nekkobox_ffi.dart looks for it. Also copies
# the sing-box core into Contents/Resources, where vpn.resolveBinary looks.
#
# This is the macOS counterpart of the POST_BUILD copy step in
# windows/runner/CMakeLists.txt: the .dylib is a build artifact, never
# committed, and must be refreshed on every build so the UI and the engine
# can't drift apart.
#
# Run as an Xcode build phase of the Runner target (so both `flutter run` and
# `flutter build macos` get it). Falls back to a prebuilt libnekkobox.dylib at
# the repo root when no Go toolchain is available (CI without Go, say).
set -e

REPO_ROOT="$(cd "$SRCROOT/../.." && pwd)"   # app/macos -> app -> repo root
ENGINE_DIR="$REPO_ROOT/engine"
OUT_DIR="${DERIVED_FILE_DIR:-$REPO_ROOT/engine/build}"
FRAMEWORKS="$BUILT_PRODUCTS_DIR/$PRODUCT_NAME.app/Contents/Frameworks"
LIB="libnekkobox.dylib"

# Xcode starts build phases with a minimal PATH; Go lives in none of it.
export PATH="$PATH:/usr/local/go/bin:/opt/homebrew/bin:/usr/local/bin:$HOME/go/bin"

if command -v go >/dev/null 2>&1; then
  mkdir -p "$OUT_DIR"
  # Build one slice per architecture Xcode asked for, then lipo them together,
  # so a universal Release build gets a universal engine.
  SLICES=""
  for ARCH in ${ARCHS:-$(uname -m)}; do
    case "$ARCH" in
      arm64)  GOARCH=arm64 ;;
      x86_64) GOARCH=amd64 ;;
      *) echo "warning: unsupported arch $ARCH, skipping engine slice"; continue ;;
    esac
    SLICE="$OUT_DIR/$ARCH/$LIB"
    mkdir -p "$OUT_DIR/$ARCH"
    ( cd "$ENGINE_DIR" && \
      CGO_ENABLED=1 GOOS=darwin GOARCH="$GOARCH" \
      CC="$(xcrun -f clang) -arch $ARCH -isysroot $(xcrun --sdk macosx --show-sdk-path) -mmacosx-version-min=${MACOSX_DEPLOYMENT_TARGET:-10.15}" \
      go build -buildmode=c-shared -o "$SLICE" ./cmd/nekkobox-ffi )
    SLICES="$SLICES $SLICE"
  done
  [ -n "$SLICES" ] || { echo "error: no engine slice was built"; exit 1; }
  # shellcheck disable=SC2086
  lipo -create $SLICES -output "$OUT_DIR/$LIB"
elif [ -f "$REPO_ROOT/$LIB" ]; then
  echo "note: no Go toolchain on PATH — using prebuilt $REPO_ROOT/$LIB"
  cp -f "$REPO_ROOT/$LIB" "$OUT_DIR/$LIB"
else
  echo "error: Go toolchain not found and no prebuilt $LIB at $REPO_ROOT" >&2
  exit 1
fi

mkdir -p "$FRAMEWORKS"
cp -f "$OUT_DIR/$LIB" "$FRAMEWORKS/$LIB"
install_name_tool -id "@rpath/$LIB" "$FRAMEWORKS/$LIB" 2>/dev/null || true

# Sign the dylib with whatever identity this configuration uses ("-" = ad-hoc);
# Xcode seals the surrounding bundle after this phase, so nested code must
# already carry its own signature.
codesign --force --timestamp=none \
  --sign "${EXPANDED_CODE_SIGN_IDENTITY:--}" "$FRAMEWORKS/$LIB"

# --- sing-box core -------------------------------------------------------
# Not committed (83 MB, and upstream ships per-platform builds): it lives in
# the gitignored archive/, alongside the Windows sing-box.exe + wintun.dll.
RESOURCES="$BUILT_PRODUCTS_DIR/$PRODUCT_NAME.app/Contents/Resources"
SINGBOX="$REPO_ROOT/archive/sing-box"
if [ -f "$SINGBOX" ]; then
  mkdir -p "$RESOURCES"
  cp -f "$SINGBOX" "$RESOURCES/sing-box"
  chmod +x "$RESOURCES/sing-box"
  codesign --force --timestamp=none \
    --sign "${EXPANDED_CODE_SIGN_IDENTITY:--}" "$RESOURCES/sing-box"
else
  echo "warning: $SINGBOX missing — the app builds but cannot start the VPN core"
fi

# --- pinned Xray transport ------------------------------------------------
XRAY="$REPO_ROOT/archive/xray"
if [ ! -f "$XRAY" ]; then
  echo "error: pinned Xray missing; run python3 bench/fetch_xray.py" >&2
  exit 1
fi
if ! "$XRAY" version | head -n 1 | grep -q '^Xray 26\.10\.21 '; then
  echo "error: expected Xray 26.10.21" >&2
  exit 1
fi
mkdir -p "$RESOURCES"
cp -f "$XRAY" "$RESOURCES/xray"
chmod +x "$RESOURCES/xray"
codesign --force --timestamp=none --sign "${EXPANDED_CODE_SIGN_IDENTITY:--}" "$RESOURCES/xray"
if [ -f "$REPO_ROOT/archive/xray.LICENSE" ]; then
  cp -f "$REPO_ROOT/archive/xray.LICENSE" "$RESOURCES/xray.LICENSE"
fi

# Both files above land in the bundle behind Xcode's back. On a clean build
# the CodeSign step that follows seals them; on an incremental build Xcode
# sees no changed inputs, skips signing, and the bundle is left with a stale
# seal ("a sealed resource is missing or invalid"). Re-sealing here makes the
# result identical either way.
APP_BUNDLE="$BUILT_PRODUCTS_DIR/$PRODUCT_NAME.app"
SIGN_ARGS="--force --timestamp=none --sign ${EXPANDED_CODE_SIGN_IDENTITY:--}"
# Flutter's macOS debug configuration places the Dart VM and preview dylibs
# beside the executable. Sign them before sealing the app bundle; otherwise
# codesign rejects the app because these nested code objects are still unsigned.
if [ -d "$APP_BUNDLE/Contents/MacOS" ]; then
  for DEBUG_DYLIB in "$APP_BUNDLE"/Contents/MacOS/*.dylib; do
    [ -f "$DEBUG_DYLIB" ] || continue
    codesign --force --timestamp=none \
      --sign "${EXPANDED_CODE_SIGN_IDENTITY:--}" "$DEBUG_DYLIB"
  done
fi
if [ -n "$CODE_SIGN_ENTITLEMENTS" ]; then
  SIGN_ARGS="$SIGN_ARGS --entitlements $SRCROOT/$CODE_SIGN_ENTITLEMENTS"
fi
if [ "$ENABLE_HARDENED_RUNTIME" = "YES" ]; then
  SIGN_ARGS="$SIGN_ARGS --options runtime"
fi
# shellcheck disable=SC2086
codesign $SIGN_ARGS "$APP_BUNDLE"
