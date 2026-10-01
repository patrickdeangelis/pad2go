#!/usr/bin/env bash
# Cross-compile pad2go.exe (GUI, no console) from macOS/Linux using MinGW-w64
# in Docker. WebView2 and the C++ runtime are linked statically, so the
# result imports only Windows system DLLs. packaging/windows/winres.json embeds
# the icon as resource 32512 (IDI_APPLICATION), which Explorer shows and
# webview loads for the window and taskbar.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
out="${1:-$root/dist}"
mkdir -p "$out"
docker run --rm -e DEBIAN_FRONTEND=noninteractive -e GOFLAGS=-buildvcs=false \
  -v "$root":/src -v "$out":/out -w /src golang:1.26 sh -c '
    apt-get update >/dev/null && apt-get install -y --no-install-recommends gcc-mingw-w64-x86-64 g++-mingw-w64-x86-64 >/dev/null
    trap "rm -f cmd/pad2go/rsrc_windows_amd64.syso" EXIT
    go run github.com/tc-hib/go-winres@v0.3.3 make --in packaging/windows/winres.json --arch amd64 --out cmd/pad2go/rsrc
    CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc CXX=x86_64-w64-mingw32-g++ \
    CGO_CXXFLAGS="-w -I/src/packaging/windows/include" \
    go build -trimpath -ldflags "-s -w -H windowsgui -X main.version='"${VERSION:-dev}"'" -o /out/pad2go.exe ./cmd/pad2go'
echo "built $out/pad2go.exe"
