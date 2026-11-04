#!/usr/bin/env bash
# Builds the static web checker into web/: the WebAssembly checker, Go's
# loader script, and the example CSV. Serve web/ as a static site.
#
# Cloudflare Pages: build command "web/build.sh", output directory "web".
set -euo pipefail
cd "$(dirname "$0")/.."
GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o web/feeddoctor.wasm ./cmd/feeddoctor-wasm
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" web/wasm_exec.js 2>/dev/null \
  || cp "$(go env GOROOT)/misc/wasm/wasm_exec.js" web/wasm_exec.js
cp testdata/dev_store_products.csv web/sample.csv
echo "built web/ ($(du -h web/feeddoctor.wasm | cut -f1) checker)"
