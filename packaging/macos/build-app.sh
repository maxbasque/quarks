#!/usr/bin/env bash
# Build Quark's.app and Quarks-<version>.dmg into dist/. Runs on macOS only —
# it needs deno, the Xcode command-line tools (lipo), sips, iconutil, codesign
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

echo "==> building the app (Apple silicon + Intel)"
for target in aarch64-apple-darwin x86_64-apple-darwin; do
  ( cd "$repo" && make --no-print-directory mac TARGET=$target OUT="$out/quarks-$target" )
done
lipo -create -output "$app/Contents/MacOS/Quarks" "$out/quarks-aarch64-apple-darwin" "$out/quarks-x86_64-apple-darwin"
rm "$out/quarks-aarch64-apple-darwin" "$out/quarks-x86_64-apple-darwin"

echo "==> adding the webview library"
# the binary loads the one matching its architecture from Contents/Frameworks
"$repo/packaging/fetch-libwebview.sh" "$app/Contents/Frameworks" libwebview.aarch64.dylib libwebview.x86_64.dylib

echo "==> assembling the bundle"
sed "s/__VERSION__/$version/g" "$here/Info.plist" > "$app/Contents/Info.plist"

iconset="$out/AppIcon.iconset"
mkdir -p "$iconset"
src="$repo/src/web/static/icon-512.png"
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
