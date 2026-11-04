#!/usr/bin/env bash
# Builds the static web checker into web/: the WebAssembly checker, Go's
# loader script, and the example CSV. Serve web/ as a static site.
#
# On Vercel, vercel.json runs this. When the build image has no Go, the
# script downloads GO_VERSION first.
set -euo pipefail
cd "$(dirname "$0")/.."
if ! command -v go >/dev/null 2>&1; then
  version="${GO_VERSION:-1.24.7}"
  echo "go not found; downloading go$version"
  mkdir -p .tools
  curl -fsSL "https://go.dev/dl/go$version.linux-amd64.tar.gz" | tar -xz -C .tools
  export PATH="$PWD/.tools/go/bin:$PATH"
fi
GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o web/feeddoctor.wasm ./cmd/feeddoctor-wasm
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" web/wasm_exec.js 2>/dev/null \
  || cp "$(go env GOROOT)/misc/wasm/wasm_exec.js" web/wasm_exec.js
cp testdata/dev_store_products.csv web/sample.csv
echo "built web/ ($(du -h web/feeddoctor.wasm | cut -f1) checker)"
