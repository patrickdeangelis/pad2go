#!/usr/bin/env bash
# Build the Windows installer (pad2go-setup.exe) around a pad2go.exe from
# packaging/build-windows.sh. It bundles the ViGEmBus driver setup, which it
# runs only when no working ViGEmBus is found. NSIS runs in Docker.
#   VERSION=1.2.3 packaging/build-windows-installer.sh dist/pad2go.exe pad2go-setup.exe
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
exe="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"
out="$(cd "$(dirname "$2")" && pwd)/$(basename "$2")"
version="${VERSION:-0.0.0}"
version="${version%%-*}" # NSIS needs a numeric X.Y.Z

# ViGEmBus 1.22.0 is the final release (the project is archived); pinned by hash.
vigem_url=https://github.com/nefarius/ViGEmBus/releases/download/v1.22.0/ViGEmBus_1.22.0_x64_x86_arm64.exe
vigem_sha=89220a7865076b342892f98865f3499fb7c4cfd673159e89d352c360fd014c6a

# Staged next to the output so Docker can mount it on any host.
stage="$(mktemp -d "$(dirname "$out")/.pad2go-installer.XXXXXX")"
trap 'rm -rf "$stage"' EXIT
cp "$exe" "$stage/pad2go.exe"
cp "$root/packaging/pad2go.png" "$root/packaging/windows/installer.nsi" "$stage/"
sed 's/$/\r/' "$root/LICENSE" > "$stage/LICENSE.txt"
sed 's/$/\r/' "$root/packaging/windows/THIRD-PARTY-NOTICES.txt" > "$stage/THIRD-PARTY-NOTICES.txt"

docker run --rm -e DEBIAN_FRONTEND=noninteractive -v "$stage":/s -w /s debian:bookworm-slim sh -c '
    set -e
    apt-get update >/dev/null && apt-get install -y --no-install-recommends ca-certificates curl nsis imagemagick >/dev/null
    curl -fsSL -o ViGEmBus_Setup.exe '"$vigem_url"'
    echo "'"$vigem_sha"'  ViGEmBus_Setup.exe" | sha256sum -c -
    convert pad2go.png -define icon:auto-resize=256,64,48,32,16 pad2go.ico
    makensis -V2 -DVERSION='"$version"' -DDIST=/s -DVIGEMBUS=/s/ViGEmBus_Setup.exe -DOUTFILE=/s/pad2go-installer.exe installer.nsi'
cp "$stage/pad2go-installer.exe" "$out"
echo "built $out"
