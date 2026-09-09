#!/usr/bin/env bash
# Remove everything install.sh added. Leaves your config and cache alone;
# pass --purge to delete those too.
set -euo pipefail

bin_dir="$HOME/.local/bin"
app_dir="$HOME/.local/share/applications"
icon_dir="$HOME/.local/share/icons/hicolor"
unit_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
cfg_dir="${XDG_CONFIG_HOME:-$HOME/.config}/quarks"
cache_dir="${XDG_CACHE_HOME:-$HOME/.cache}/quarks"

if command -v systemctl >/dev/null 2>&1 && systemctl --user show-environment >/dev/null 2>&1; then
  systemctl --user disable --now quarks.service 2>/dev/null || true
fi
rm -f "$unit_dir/quarks.service"
command -v systemctl >/dev/null 2>&1 && systemctl --user daemon-reload 2>/dev/null || true

rm -f "$bin_dir/quarks" "$bin_dir/quarks-open"
rm -f "$app_dir/quarks.desktop"
rm -f "$icon_dir/scalable/apps/quarks.svg" \
      "$icon_dir/192x192/apps/quarks.png" \
      "$icon_dir/512x512/apps/quarks.png"
update-desktop-database "$app_dir" >/dev/null 2>&1 || true
gtk-update-icon-cache -f -t "$icon_dir" >/dev/null 2>&1 || true
kbuildsycoca6 >/dev/null 2>&1 || kbuildsycoca5 >/dev/null 2>&1 || true

echo "Removed binary, helper, service and launcher."

if [ "${1:-}" = "--purge" ]; then
  rm -rf "$cfg_dir" "$cache_dir"
  echo "Purged $cfg_dir and $cache_dir."
else
  echo "Kept $cfg_dir and $cache_dir (pass --purge to remove)."
fi
