#!/bin/sh
set -eu
cd "$(dirname "$0")"
plugin_version=$(sed -n 's/^const version = "\([^"]*\)"/\1/p' main.go)
test -n "$plugin_version"
test -f panel.html
test -f frame.wasm.gz
test -f wasm_exec.js
mkdir -p dist
CGO_ENABLED=1 go test ./...
CGO_ENABLED=1 go build -buildvcs=false -trimpath -buildmode=c-shared -o "dist/photopainter-v${plugin_version}.so" .
