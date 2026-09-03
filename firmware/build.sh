#!/usr/bin/env bash
set -euo pipefail
firmware_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "$firmware_dir/upstream.env"
: "${IDF_PATH:?Activate the pinned ESP-IDF environment with export.sh first}"
if [[ $(git -C "$IDF_PATH" rev-parse HEAD) != "$FRAME_FW_IDF_REV" ]]; then
  echo "Use ESP-IDF revision $FRAME_FW_IDF_REV (tested 6.0.3)." >&2
  exit 1
fi
source_dir=$(bash "$firmware_dir/prepare.sh")
artifact_dir=${FIRMWARE_ARTIFACT_DIR:-"$firmware_dir/../dist/firmware"}
mkdir -p "$artifact_dir"
artifact_dir=$(cd "$artifact_dir" && pwd)
cd "$source_dir/webapp"
npm ci --ignore-scripts
npm test
npm run build
cd "$source_dir/process-cli"
npm ci
cd "$source_dir"
python3 build.py --board "$FRAME_FW_BOARD" --step splash
python3 build.py --board "$FRAME_FW_BOARD" --step firmware "-DFIRMWARE_VERSION=$FRAME_FW_VERSION"
cd build
python3 -m esptool --chip esp32s3 merge-bin -o "$artifact_dir/photoframe-merged.bin" @flash_args
mkdir -p "$artifact_dir/bootloader" "$artifact_dir/partition_table"
cp esp32-photoframe.bin ota_data_initial.bin flash_args flasher_args.json "$artifact_dir/"
cp bootloader/bootloader.bin "$artifact_dir/bootloader/"
cp partition_table/partition-table.bin "$artifact_dir/partition_table/"
cp "$firmware_dir/upstream.env" "$artifact_dir/"
cp "$source_dir/LICENSE" "$artifact_dir/UPSTREAM-LICENSE"
cd "$artifact_dir"
shasum -a 256 photoframe-merged.bin esp32-photoframe.bin bootloader/bootloader.bin \
  partition_table/partition-table.bin ota_data_initial.bin flash_args flasher_args.json \
  upstream.env UPSTREAM-LICENSE > SHA256SUMS
echo "Artifacts: $artifact_dir"
