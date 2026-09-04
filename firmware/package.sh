#!/usr/bin/env bash
set -euo pipefail
firmware_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_dir=$(cd "$firmware_dir/.." && pwd)
artifact_dir=${FIRMWARE_ARTIFACT_DIR:-"$repo_dir/dist/firmware"}
artifact_dir=$(cd "$artifact_dir" && pwd)
(cd "$artifact_dir" && shasum -a 256 -c SHA256SUMS)
stage_dir=$(mktemp -d "${TMPDIR:-/tmp}/photoframe-release.XXXXXX")
bundle_dir="$stage_dir/photopainter73-sd-wifi-push-dev"
mkdir -p "$bundle_dir/bootloader" "$bundle_dir/partition_table"
for file in photoframe-merged.bin esp32-photoframe.bin ota_data_initial.bin flash_args flasher_args.json upstream.env UPSTREAM-LICENSE bootloader/bootloader.bin partition_table/partition-table.bin; do
  cp "$artifact_dir/$file" "$bundle_dir/$file"
done
cp "$firmware_dir/FLASHING.md" "$bundle_dir/README.md"
cp "$firmware_dir/wifi.example.json" "$firmware_dir/verify_device.py" "$bundle_dir/"
cp "$repo_dir/scripts/push-photoframe.sh" "$bundle_dir/"
cp "$repo_dir/docs/preview.png" "$bundle_dir/"
git -C "$repo_dir" rev-parse HEAD > "$bundle_dir/SOURCE_REVISION.txt"
cd "$bundle_dir"
shasum -a 256 photoframe-merged.bin esp32-photoframe.bin ota_data_initial.bin \
  flash_args flasher_args.json upstream.env UPSTREAM-LICENSE bootloader/bootloader.bin \
  partition_table/partition-table.bin README.md wifi.example.json verify_device.py \
  push-photoframe.sh preview.png SOURCE_REVISION.txt > SHA256SUMS
shasum -a 256 -c SHA256SUMS
cd "$stage_dir"
zip -q -r photopainter73-sd-wifi-push-dev.zip photopainter73-sd-wifi-push-dev
mv photopainter73-sd-wifi-push-dev.zip "$repo_dir/dist/photopainter73-sd-wifi-push-dev.zip"
echo "Release archive: $repo_dir/dist/photopainter73-sd-wifi-push-dev.zip"
