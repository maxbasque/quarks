#!/usr/bin/env bash
# Build quarks-window (the native WebKitGTK dashboard window) into the path
# given as $1. It needs the WebKitGTK and GTK headers:
#
#   - if this machine has them (pkg-config finds webkit2gtk-4.1), it builds here;
#   - otherwise it builds in a toolbox container, $QUARKS_TOOLBOX (default
#     quarks-build), creating it with the headers on first use. That's the
#     route on Fedora Atomic / Bazzite, whose host is read-only. The binary
#     only needs the WebKitGTK *runtime*, which the host already has.
set -euo pipefail

out="${1:?usage: build-window.sh <output path>}"
repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
shim="$repo/packaging/linux/pkgconfig" # webkit2gtk-4.0 -> 4.1, see the .pc file
box="${QUARKS_TOOLBOX:-quarks-build}"
flags=(build -tags quarkswindow -trimpath -ldflags=-s\ -w -o "$out" ./cmd/quarks-window)

if pkg-config --exists webkit2gtk-4.1 gtk+-3.0 2>/dev/null; then
  ( cd "$repo" && PKG_CONFIG_PATH="$shim" CGO_ENABLED=1 go "${flags[@]}" )
  exit 0
fi

if ! command -v toolbox >/dev/null 2>&1; then
  echo "build-window: install the WebKitGTK and GTK development packages" >&2
  echo "  (Fedora: webkit2gtk4.1-devel gtk3-devel; Debian/Ubuntu: libwebkit2gtk-4.1-dev libgtk-3-dev)" >&2
  exit 1
fi

if ! toolbox list --containers 2>/dev/null | grep -qw "$box"; then
  echo "==> one-time: creating the '$box' toolbox with the WebKitGTK headers"
  . /etc/os-release
  toolbox create -y --distro fedora --release "${VERSION_ID:-44}" "$box"
  toolbox run -c "$box" sudo dnf install -y -q webkit2gtk4.1-devel gtk3-devel gcc gcc-c++ pkgconf-pkg-config
fi

# the host's Go, seen from inside the container
go_bin="/run/host$(realpath "$(command -v go)")"
( cd "$repo" && toolbox run -c "$box" env PKG_CONFIG_PATH="$shim" CGO_ENABLED=1 "$go_bin" "${flags[@]}" )
