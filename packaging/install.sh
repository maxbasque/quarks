#!/usr/bin/env bash
# Install Quark's for the current user: binary in ~/.local/bin, a systemd --user
# service for the server, and a .desktop entry for the app window.
# Re-run any time to update. Nothing here needs root.
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
bin_dir="$HOME/.local/bin"
app_dir="$HOME/.local/share/applications"
icon_dir="$HOME/.local/share/icons/hicolor/scalable/apps"
unit_dir="$HOME/.config/systemd/user"
cfg_dir="${XDG_CONFIG_HOME:-$HOME/.config}/quarks"

echo "==> building"
( cd "$repo" && go build -o "$bin_dir/quarks" ./cmd/quarks )

echo "==> installing desktop entry + icon"
mkdir -p "$app_dir" "$icon_dir"
install -m 644 "$repo/packaging/quarks.desktop" "$app_dir/quarks.desktop"
install -m 644 "$repo/internal/web/static/favicon.svg" "$icon_dir/quarks.svg"
update-desktop-database "$app_dir" 2>/dev/null || true

echo "==> installing systemd --user service"
mkdir -p "$unit_dir"
install -m 644 "$repo/packaging/quarks.service" "$unit_dir/quarks.service"
systemctl --user daemon-reload
systemctl --user enable --now quarks.service

if [ ! -f "$cfg_dir/config.yaml" ]; then
  echo "==> seeding $cfg_dir/config.yaml from config.example.yaml"
  mkdir -p "$cfg_dir"
  install -m 644 "$repo/config.example.yaml" "$cfg_dir/config.yaml"
fi

echo
echo "Done. Server: systemctl --user status quarks   ·   http://localhost:7373"
echo "Launch the window from your app menu (\"Quark's\"), or:"
echo "  flatpak run com.google.Chrome --app=http://localhost:7373"
