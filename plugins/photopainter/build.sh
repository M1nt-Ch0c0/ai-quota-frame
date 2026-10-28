#!/bin/sh
set -eu
cd "$(dirname "$0")"
(cd ../../web && npm ci --ignore-scripts && npm run build)
cp ../../web/dist/panel.html panel.html
cp ../../web/dist/frame.wasm.gz frame.wasm.gz
cp ../../web/dist/wasm_exec.js wasm_exec.js
./build-native.sh
