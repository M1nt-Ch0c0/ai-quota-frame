#!/usr/bin/env bash
set -euo pipefail
if [[ $# != 2 ]]; then
  echo "Usage: PHOTOFRAME_PUSH_TOKEN=... $0 http://device-ip image.png" >&2
  exit 2
fi
: "${PHOTOFRAME_PUSH_TOKEN:?Set the dedicated device push code, not FRAME_ACCESS_TOKEN}"
if [[ ${#PHOTOFRAME_PUSH_TOKEN} -lt 32 || ${#PHOTOFRAME_PUSH_TOKEN} -gt 128 || "$PHOTOFRAME_PUSH_TOKEN" == *$'\n'* || "$PHOTOFRAME_PUSH_TOKEN" == *$'\r'* ]]; then
  echo 'Invalid push code length or newline' >&2
  exit 2
fi
device_url=${1%/}
image_file=$2
[[ -f "$image_file" ]] || { echo 'Image file not found' >&2; exit 2; }
magic=$(od -An -tx1 -N8 "$image_file" | tr -d ' \n')
case "$magic" in
  89504e470d0a1a0a) image_type=image/png ;;
  ffd8ff*) image_type=image/jpeg ;;
  *) echo 'Only PNG/JPEG images are supported' >&2; exit 2 ;;
esac
curl --fail-with-body --silent --show-error --connect-timeout 5 --max-time 90 \
  --request POST --header "Authorization: Bearer $PHOTOFRAME_PUSH_TOKEN" \
  --header "Content-Type: $image_type" --data-binary "@$image_file" "$device_url/api/push"
printf '\n'
