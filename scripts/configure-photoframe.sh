#!/usr/bin/env bash
set -Eeuo pipefail

die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

log() {
  printf '%s\n' "$*"
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

require_environment() {
  local name=$1
  [[ -n ${!name:-} ]] || die "$name is required"
}

strip_trailing_slashes() {
  local value=$1
  while [[ $value == */ ]]; do
    value=${value%/}
  done
  printf '%s' "$value"
}

read_http_header() {
  local header_file=$1
  local header_name=$2
  python3 - "$header_file" "$header_name" <<'PY'
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
target = sys.argv[2].lower() + ":"
value = ""
for line in path.read_text(encoding="iso-8859-1").splitlines():
    if line.lower().startswith(target):
        value = line.split(":", 1)[1].strip()
print(value)
PY
}

show_error_body() {
  local body_file=$1
  if [[ -s $body_file ]]; then
    printf 'response: ' >&2
    head -c 500 "$body_file" >&2
    printf '\n' >&2
  fi
}

require_command curl
require_command python3
require_environment DEVICE_URL
require_environment FRAME_URL
require_environment FRAME_TOKEN

DEVICE_URL=$(strip_trailing_slashes "$DEVICE_URL")
ROTATE_CRON=${ROTATE_CRON:-'*/10 * *'}
export DEVICE_URL FRAME_URL FRAME_TOKEN ROTATE_CRON
readonly DEVICE_URL FRAME_URL FRAME_TOKEN ROTATE_CRON

python3 - <<'PY'
import ipaddress
import os
import re
import sys
from urllib.parse import urlsplit


def fail(message: str) -> None:
    print(f"error: {message}", file=sys.stderr)
    raise SystemExit(1)


def parse_cron_uint(value: str) -> int:
    if not value or not value.isascii() or not value.isdigit():
        fail("ROTATE_CRON contains a malformed number")
    parsed = int(value)
    if parsed > 100_000:
        fail("ROTATE_CRON contains an excessively large number")
    return parsed


def validate_cron_field(field: str, low: int, high: int) -> None:
    if not field or len(field) >= 32:
        fail("ROTATE_CRON contains an empty or overlong field")
    for term in field.split(","):
        if not term:
            fail("ROTATE_CRON contains an empty list item")

        step_parts = term.split("/")
        if len(step_parts) > 2:
            fail("ROTATE_CRON contains more than one step separator")
        base = step_parts[0]
        step = 1
        if len(step_parts) == 2:
            step = parse_cron_uint(step_parts[1])
            if step <= 0:
                fail("ROTATE_CRON step must be greater than zero")

        if base == "*":
            start, end = low, high
        elif base.count("-") == 1:
            start_text, end_text = base.split("-", 1)
            start = parse_cron_uint(start_text)
            end = parse_cron_uint(end_text)
        elif "-" not in base:
            start = parse_cron_uint(base)
            end = high if len(step_parts) == 2 else start
        else:
            fail("ROTATE_CRON contains a malformed range")

        if start < low or end > high or start > end:
            fail(f"ROTATE_CRON value must be between {low} and {high}")


device_url = os.environ["DEVICE_URL"]
frame_url = os.environ["FRAME_URL"]
frame_token = os.environ["FRAME_TOKEN"]
rotate_cron = os.environ["ROTATE_CRON"]

for name, value in (("DEVICE_URL", device_url), ("FRAME_URL", frame_url)):
    if any(character in value for character in "\r\n"):
        fail(f"{name} must not contain a newline")
    parsed = urlsplit(value)
    if parsed.scheme not in {"http", "https"} or not parsed.hostname:
        fail(f"{name} must be an absolute http(s) URL")
    if parsed.username is not None or parsed.password is not None:
        fail(f"{name} must not contain embedded credentials")

device = urlsplit(device_url)
if device.path not in {"", "/"} or device.query or device.fragment:
    fail("DEVICE_URL must be only the device origin, for example http://192.168.1.50")

frame = urlsplit(frame_url)
if frame.path != "/api/v1/frame.png":
    fail("FRAME_URL path must be /api/v1/frame.png")
if frame.fragment:
    fail("FRAME_URL must not contain a URL fragment")

host = (frame.hostname or "").lower()
if host in {"localhost", "0.0.0.0", "::", "::1"}:
    fail("FRAME_URL must use the host LAN address, not a loopback or bind address")
try:
    address = ipaddress.ip_address(host)
except ValueError:
    address = None
if address is not None and (address.is_loopback or address.is_unspecified):
    fail("FRAME_URL must use the host LAN address, not a loopback or bind address")

if frame_token != frame_token.strip():
    fail("FRAME_TOKEN must not have leading or trailing whitespace")
if any(character in frame_token for character in "\r\n"):
    fail("FRAME_TOKEN must not contain a newline")

if any(character in rotate_cron for character in "\r\n") or len(rotate_cron) >= 64:
    fail("ROTATE_CRON must be shorter than 64 characters and stay on one line")
fields = re.split(r"[ \t]+", rotate_cron.strip(" \t"))
if len(fields) != 3:
    fail("ROTATE_CRON must contain exactly 3 fields: minute hour day-of-week")
validate_cron_field(fields[0], 0, 59)
validate_cron_field(fields[1], 0, 23)
validate_cron_field(fields[2], 0, 7)
PY

umask 077
work_dir=$(mktemp -d /tmp/configure-photoframe.XXXXXX)
cleanup() {
  rm -rf -- "$work_dir"
}
trap cleanup EXIT

readonly auth_headers="$work_dir/frame-auth.headers"
readonly frame_headers="$work_dir/frame.headers"
readonly frame_body="$work_dir/frame.png"
readonly conditional_headers="$work_dir/conditional.headers"
readonly conditional_body="$work_dir/conditional.body"
readonly system_headers="$work_dir/system.headers"
readonly system_body="$work_dir/system.json"
readonly config_before_headers="$work_dir/config-before.headers"
readonly config_before_body="$work_dir/config-before.json"
readonly config_payload="$work_dir/config-payload.json"
readonly patch_headers="$work_dir/patch.headers"
readonly patch_body="$work_dir/patch.json"
readonly config_after_headers="$work_dir/config-after.headers"
readonly config_after_body="$work_dir/config-after.json"

printf 'Authorization: Bearer %s\n' "$FRAME_TOKEN" >"$auth_headers"
printf 'X-Display-Width: 800\n' >>"$auth_headers"
printf 'X-Display-Height: 480\n' >>"$auth_headers"
printf 'X-Display-Orientation: landscape\n' >>"$auth_headers"

readonly -a curl_common=(
  --silent
  --show-error
  --noproxy '*'
  --connect-timeout 5
  --max-time 30
)

log '[1/5] validating the host frame endpoint (PNG, Bearer, ETag, 304)'
if ! frame_status=$(curl "${curl_common[@]}" \
  --header "@$auth_headers" \
  --dump-header "$frame_headers" \
  --output "$frame_body" \
  --write-out '%{http_code}' \
  "$FRAME_URL"); then
  die 'could not reach FRAME_URL'
fi
if [[ $frame_status != 200 ]]; then
  show_error_body "$frame_body"
  die "FRAME_URL returned HTTP $frame_status; expected 200"
fi

content_type=$(read_http_header "$frame_headers" Content-Type)
if [[ ${content_type%%;*} != image/png ]]; then
  die "FRAME_URL returned Content-Type '$content_type'; expected image/png"
fi
python3 - "$frame_body" <<'PY'
import pathlib
import sys

if pathlib.Path(sys.argv[1]).read_bytes()[:8] != b"\x89PNG\r\n\x1a\n":
    print("error: FRAME_URL response is not a PNG file", file=sys.stderr)
    raise SystemExit(1)
PY

etag=$(read_http_header "$frame_headers" ETag)
[[ -n $etag ]] || die 'FRAME_URL did not return an ETag header'

conditional_status=''
for attempt in 1 2; do
  if ! conditional_status=$(curl "${curl_common[@]}" \
    --header "@$auth_headers" \
    --header "If-None-Match: $etag" \
    --dump-header "$conditional_headers" \
    --output "$conditional_body" \
    --write-out '%{http_code}' \
    "$FRAME_URL"); then
    die 'conditional request to FRAME_URL failed'
  fi
  if [[ $conditional_status == 304 ]]; then
    break
  fi
  if [[ $conditional_status == 200 && $attempt == 1 ]]; then
    etag=$(read_http_header "$conditional_headers" ETag)
    [[ -n $etag ]] || die 'conditional 200 response did not return a replacement ETag'
    continue
  fi
  show_error_body "$conditional_body"
  die "conditional FRAME_URL request returned HTTP $conditional_status; expected 304"
done
[[ $conditional_status == 304 ]] || die 'FRAME_URL did not produce a stable 304 response'

log '[2/5] reading device identity before making changes'
if ! system_status=$(curl "${curl_common[@]}" \
  --dump-header "$system_headers" \
  --output "$system_body" \
  --write-out '%{http_code}' \
  "$DEVICE_URL/api/system-info"); then
  die 'could not reach DEVICE_URL; wake the frame with BOOT and check its IP'
fi
if [[ $system_status != 200 ]]; then
  show_error_body "$system_body"
  die "device system-info returned HTTP $system_status; expected 200"
fi
system_summary=$(python3 - "$system_body" <<'PY'
import json
import pathlib
import sys

try:
    payload = json.loads(pathlib.Path(sys.argv[1]).read_text())
except (OSError, json.JSONDecodeError) as error:
    print(f"error: device system-info is not valid JSON: {error}", file=sys.stderr)
    raise SystemExit(1)

expected_board = "waveshare_photopainter_73"
if payload.get("project_name") != "esp32-photoframe":
    print("error: device is not running esp32-photoframe", file=sys.stderr)
    raise SystemExit(1)
if payload.get("board_name") != expected_board:
    print(
        f"error: device board is {payload.get('board_name')!r}; expected {expected_board!r}",
        file=sys.stderr,
    )
    raise SystemExit(1)
if payload.get("width") != 800 or payload.get("height") != 480:
    print("error: device resolution is not 800x480", file=sys.stderr)
    raise SystemExit(1)
print(f"{payload['board_name']} / {payload.get('version', 'unknown')} / 800x480")
PY
)
log "      $system_summary"

log '[3/5] reading and validating the current device config'
if ! config_before_status=$(curl "${curl_common[@]}" \
  --dump-header "$config_before_headers" \
  --output "$config_before_body" \
  --write-out '%{http_code}' \
  "$DEVICE_URL/api/config"); then
  die 'could not read the current device config'
fi
if [[ $config_before_status != 200 ]]; then
  show_error_body "$config_before_body"
  die "device config returned HTTP $config_before_status; expected 200"
fi
python3 - "$config_before_body" <<'PY'
import json
import pathlib
import sys

try:
    payload = json.loads(pathlib.Path(sys.argv[1]).read_text())
except (OSError, json.JSONDecodeError) as error:
    print(f"error: current device config is not valid JSON: {error}", file=sys.stderr)
    raise SystemExit(1)
if not isinstance(payload, dict):
    print("error: current device config must be a JSON object", file=sys.stderr)
    raise SystemExit(1)
PY

python3 - "$config_payload" <<'PY'
import json
import os
import pathlib
import sys

payload = {
    "display_orientation": "landscape",
    "display_rotation_deg": 180,
    "auto_rotate": True,
    "rotate_cron": [os.environ["ROTATE_CRON"]],
    "rotation_mode": "url",
    "image_url": os.environ["FRAME_URL"],
    "access_token": os.environ["FRAME_TOKEN"],
    "http_header_key": "",
    "http_header_value": "",
    "save_downloaded_images": False,
    "deep_sleep_enabled": True,
}
pathlib.Path(sys.argv[1]).write_text(json.dumps(payload, separators=(",", ":")))
PY

log '[4/5] applying the URL-rotation config'
if ! patch_status=$(curl "${curl_common[@]}" \
  --request PATCH \
  --header 'Content-Type: application/json' \
  --data-binary "@$config_payload" \
  --dump-header "$patch_headers" \
  --output "$patch_body" \
  --write-out '%{http_code}' \
  "$DEVICE_URL/api/config"); then
  die 'device config PATCH failed'
fi
if [[ $patch_status != 200 ]]; then
  show_error_body "$patch_body"
  die "device config PATCH returned HTTP $patch_status; expected 200"
fi
python3 - "$patch_body" <<'PY'
import json
import pathlib
import sys

try:
    payload = json.loads(pathlib.Path(sys.argv[1]).read_text())
except (OSError, json.JSONDecodeError) as error:
    print(f"error: config PATCH response is not valid JSON: {error}", file=sys.stderr)
    raise SystemExit(1)
if payload.get("status") != "success":
    print("error: device did not acknowledge the config update", file=sys.stderr)
    raise SystemExit(1)
PY

log '[5/5] reading the config back and comparing every managed field'
if ! config_after_status=$(curl "${curl_common[@]}" \
  --dump-header "$config_after_headers" \
  --output "$config_after_body" \
  --write-out '%{http_code}' \
  "$DEVICE_URL/api/config"); then
  die 'could not read the updated device config'
fi
if [[ $config_after_status != 200 ]]; then
  show_error_body "$config_after_body"
  die "updated device config returned HTTP $config_after_status; expected 200"
fi
EXPECTED_CONFIG="$config_payload" python3 - "$config_after_body" <<'PY'
import json
import os
import pathlib
import sys

try:
    actual = json.loads(pathlib.Path(sys.argv[1]).read_text())
    expected = json.loads(pathlib.Path(os.environ["EXPECTED_CONFIG"]).read_text())
except (OSError, json.JSONDecodeError) as error:
    print(f"error: could not verify updated config: {error}", file=sys.stderr)
    raise SystemExit(1)

different = [key for key, value in expected.items() if actual.get(key) != value]
if different:
    print(
        "error: device did not retain these config fields: " + ", ".join(different),
        file=sys.stderr,
    )
    raise SystemExit(1)
PY

log 'PhotoFrame configuration verified.'
log "Trigger one physical refresh when ready: curl --fail-with-body --request POST '$DEVICE_URL/api/rotate'"
