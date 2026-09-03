#!/usr/bin/env bash
set -euo pipefail
firmware_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "$firmware_dir/upstream.env"
source_dir=${FIRMWARE_SOURCE_DIR:-"$firmware_dir/.work/esp32-photoframe"}
patch_file="$firmware_dir/patches/0001-sd-wifi-auth-push.patch"
if [[ ! -d "$source_dir/.git" ]]; then
  if [[ -e "$source_dir" ]]; then
    echo "Refusing an existing non-Git source directory: $source_dir" >&2
    exit 1
  fi
  mkdir -p "$(dirname "$source_dir")"
  git clone --filter=blob:none --no-checkout "$FRAME_FW_UPSTREAM_URL" "$source_dir" >&2
  git -C "$source_dir" checkout --detach "$FRAME_FW_UPSTREAM_REV" >&2
fi
if [[ $(git -C "$source_dir" rev-parse HEAD) != "$FRAME_FW_UPSTREAM_REV" ]]; then
  echo "Source must be at the pinned upstream revision; no checkout was changed." >&2
  exit 1
fi
if git -C "$source_dir" apply --reverse --check "$patch_file" 2>/dev/null; then
  : # Already patched; leave it untouched.
else
  if ! git -C "$source_dir" diff --quiet || ! git -C "$source_dir" diff --cached --quiet || [[ -n $(git -C "$source_dir" ls-files --others --exclude-standard) ]]; then
    echo "Source contains other edits; refusing to overwrite them." >&2
    exit 1
  fi
  git -C "$source_dir" apply --check "$patch_file"
  git -C "$source_dir" apply "$patch_file"
fi
if [[ -e "$source_dir/dependencies.lock" ]] && ! cmp -s "$firmware_dir/dependencies.lock" "$source_dir/dependencies.lock"; then
  echo "Dependency lock differs; use a fresh FIRMWARE_SOURCE_DIR." >&2
  exit 1
fi
cp "$firmware_dir/dependencies.lock" "$source_dir/dependencies.lock"
printf '%s\n' "$source_dir"
