#!/usr/bin/env bash
# Package Pad2Go.app into a drag-to-install disk image: the app next to an
# Applications shortcut. Run build-macos-app.sh first.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
app="${1:-$root/dist/Pad2Go.app}"
dmg="${2:-$root/dist/Pad2Go.dmg}"
stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
ditto "$app" "$stage/Pad2Go.app"
ln -s /Applications "$stage/Applications"
rm -f "$dmg"
hdiutil create -volname Pad2Go -srcfolder "$stage" -fs HFS+ -format UDZO -ov "$dmg" >/dev/null
echo "built $dmg"
