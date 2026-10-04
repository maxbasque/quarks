#!/usr/bin/env bash
# Fetch the webview shared library the dashboard window loads, from
# webview_deno's release builds, and check it against a pinned SHA-256.
#
#   fetch-libwebview.sh <dest dir> <file>...
#   fetch-libwebview.sh ~/.local/lib/quarks libwebview.x86_64.so
#
# The Linux builds are GTK 4 + WebKitGTK 6.0 (Fedora/Bazzite ship both); the
# macOS builds use the system WKWebView. Keep the version equal to
# LIBWEBVIEW_VERSION in src/window/webview.ts.
set -euo pipefail

version="0.9.0"
dest="${1:?usage: fetch-libwebview.sh <dest dir> <file>...}"
shift

sha256_of() {
  case "$1" in
    libwebview.x86_64.so)     echo 5a66b75e14e9360d5c24ba53574e5ed5b1b476843623721aae8fc47a15a69a42 ;;
    libwebview.aarch64.so)    echo e9ee12c1fe6f0c587c37cca827586bf5c202fd7e04afcd01b780c993878d132a ;;
    libwebview.x86_64.dylib)  echo 2e2fd6f7654bcddd963fca632c152fe4d41bbc9327100ebe4b8251c379180b41 ;;
    libwebview.aarch64.dylib) echo 1bd58e657fb0a3d1e57b365720cb9c1a1b5cc46749f3c7edd4e06a815f6aa875 ;;
    *) echo "fetch-libwebview: no pinned hash for $1" >&2; exit 1 ;;
  esac
}

hash_file() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1
  else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

mkdir -p "$dest"
for f in "$@"; do
  want="$(sha256_of "$f")"
  if [ -f "$dest/$f" ] && [ "$(hash_file "$dest/$f")" = "$want" ]; then
    continue # already there
  fi
  tmp="$(mktemp)"
  curl -fsSL -o "$tmp" "https://github.com/webview/webview_deno/releases/download/$version/$f"
  got="$(hash_file "$tmp")"
  if [ "$got" != "$want" ]; then
    rm -f "$tmp"
    echo "fetch-libwebview: $f has SHA-256 $got, expected $want" >&2
    exit 1
  fi
  install -m 644 "$tmp" "$dest/$f"
  rm -f "$tmp"
done
