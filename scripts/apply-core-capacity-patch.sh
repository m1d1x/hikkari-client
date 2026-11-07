#!/usr/bin/env bash
# The same pinned core + capacity advice control API on every build surface.
set -euo pipefail
repo="$(cd "$(dirname "$0")/.." && pwd)"
core="${1:?usage: apply-core-capacity-patch.sh CORE_SOURCE}"
git -C "$core" apply --check "$repo/engine/core-patches/capacity-v1.patch"
git -C "$core" apply "$repo/engine/core-patches/capacity-v1.patch"
