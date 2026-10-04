#!/usr/bin/env bash
# Build Quark's.app and Quarks-<version>.dmg into dist/. Runs on macOS only —
# it needs the Xcode command-line tools (cgo, lipo), sips, iconutil, codesign
# and hdiutil. The release workflow runs it on GitHub's macOS machines.
#
#   packaging/macos/build-app.sh 1.0.0
set -euo pipefail

version="${1:?usage: build-app.sh <version>}"
repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
here="$repo/packaging/macos"
out="$repo/dist"
app="$out/Quark's.app"

rm -rf "$out"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"

# The oldest macOS the app runs on. Go itself needs 13 (Ventura); clang and
# ld read this for the C, C++ (webview) and link steps alike. Keep it equal
# to LSMinimumSystemVersion in Info.plist.
export MACOSX_DEPLOYMENT_TARGET=13.0

echo "==> building the app (Apple silicon + Intel)"
for arch in arm64 amd64; do
  ( cd "$repo" && CGO_ENABLED=1 GOOS=darwin GOARCH=$arch \
      go build -trimpath -ldflags="-s -w" -o "$out/quarks-$arch" ./cmd/quarks-mac ) 2>&1 | tee "$out/build-$arch.log"
  # code compiled for a newer macOS links with only a warning, then fails to
  # launch on older Macs — make that a build failure
  if grep -q "built for newer" "$out/build-$arch.log"; then
    echo "error: some code targets a newer macOS than $MACOSX_DEPLOYMENT_TARGET" >&2
    exit 1
  fi
  rm "$out/build-$arch.log"
done
lipo -create -output "$app/Contents/MacOS/Quarks" "$out/quarks-arm64" "$out/quarks-amd64"
rm "$out/quarks-arm64" "$out/quarks-amd64"

echo "==> assembling the bundle"
sed "s/__VERSION__/$version/g" "$here/Info.plist" > "$app/Contents/Info.plist"
cp "$repo/config.example.yaml" "$app/Contents/Resources/config.example.yaml"

iconset="$out/AppIcon.iconset"
mkdir -p "$iconset"
src="$repo/internal/web/static/icon-512.png"
for s in 16 32 128 256 512; do
  sips -z $s $s "$src" --out "$iconset/icon_${s}x${s}.png" >/dev/null
  sips -z $((s * 2)) $((s * 2)) "$src" --out "$iconset/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns "$iconset" -o "$app/Contents/Resources/AppIcon.icns"
rm -rf "$iconset"

# Ad-hoc signature: no Apple developer account, so macOS still asks for
# "Open Anyway" once, but the bundle is consistent and Apple silicon runs it.
codesign --force --deep --sign - "$app"
codesign --verify --deep --strict "$app"

echo "==> packaging the disk image"
dmg="$out/Quarks-$version.dmg"
stage="$out/dmg"
mkdir -p "$stage"
cp -R "$app" "$stage/"
# The classic "drag the app onto Applications" window. create-dmg lays it out
# through Finder; if that fails (it needs a GUI session), fall back to a plain
# image that still has the Applications shortcut next to the app.
if command -v create-dmg >/dev/null 2>&1 &&
  create-dmg --volname "Quark's" --window-size 540 360 --icon-size 128 \
    --icon "Quark's.app" 140 170 --app-drop-link 400 170 --hide-extension "Quark's.app" \
    --no-internet-enable "$dmg" "$stage"; then
  :
else
  echo "   (create-dmg unavailable or failed; building a plain image)"
  rm -f "$dmg"
  ln -s /Applications "$stage/Applications"
  hdiutil create -volname "Quark's" -srcfolder "$stage" -ov -format UDZO "$dmg" >/dev/null
fi
rm -rf "$stage"

echo "built: $dmg"
