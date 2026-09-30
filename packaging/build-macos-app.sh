#!/usr/bin/env bash
# Build Pad2Go.app: the single pad2go executable wrapped in a macOS app bundle
# so a double-click opens the window (not Terminal) and macOS asks for
# Bluetooth permission on behalf of Pad2Go.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
out="${1:-$root/dist}"
version="${VERSION:-dev}"
app="$out/Pad2Go.app"
rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"

(cd "$root" && CGO_CXXFLAGS="-Wno-deprecated-literal-operator" \
  go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$app/Contents/MacOS/pad2go" ./cmd/pad2go)

iconset="$(mktemp -d)/pad2go.iconset"
mkdir -p "$iconset"
for size in 16 32 128 256 512; do
  sips -z $size $size "$root/packaging/pad2go.png" --out "$iconset/icon_${size}x${size}.png" >/dev/null
  sips -z $((size * 2)) $((size * 2)) "$root/packaging/pad2go.png" --out "$iconset/icon_${size}x${size}@2x.png" >/dev/null
done
iconutil -c icns "$iconset" -o "$app/Contents/Resources/pad2go.icns"

cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleExecutable</key><string>pad2go</string>
	<key>CFBundleIdentifier</key><string>io.github.angelispatrick.pad2go</string>
	<key>CFBundleName</key><string>Pad2Go</string>
	<key>CFBundleDisplayName</key><string>Pad2Go</string>
	<key>CFBundleIconFile</key><string>pad2go</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>$version</string>
	<key>LSMinimumSystemVersion</key><string>11.0</string>
	<key>NSHighResolutionCapable</key><true/>
	<key>NSBluetoothAlwaysUsageDescription</key><string>Pad2Go usa o Bluetooth para conectar seus controles.</string>
</dict>
</plist>
PLIST
codesign --force --sign - "$app" 2>/dev/null || true
echo "built $app"
