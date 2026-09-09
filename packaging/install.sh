#!/usr/bin/env bash
# Install Quark's for the current user:
#   - binary       -> ~/.local/bin/quarks
#   - open helper  -> ~/.local/bin/quarks-open
#   - service      -> systemd --user unit (starts the server at login)
#   - launcher     -> ~/.local/share/applications/quarks.desktop (+ icon)
#
# Re-run any time to update. Nothing here needs root. Undo with uninstall.sh.
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
bin_dir="$HOME/.local/bin"
app_dir="$HOME/.local/share/applications"
icon_dir="$HOME/.local/share/icons/hicolor"
unit_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
cfg_dir="${XDG_CONFIG_HOME:-$HOME/.config}/quarks"

# go may live in Homebrew's prefix, which isn't always on a non-login PATH.
if ! command -v go >/dev/null 2>&1; then
  for p in /home/linuxbrew/.linuxbrew/bin /opt/homebrew/bin /usr/local/go/bin; do
    [ -x "$p/go" ] && PATH="$p:$PATH"
  done
fi
command -v go >/dev/null || { echo "install: 'go' not found — 'brew install go'"; exit 1; }

echo "==> building quarks"
mkdir -p "$bin_dir"
( cd "$repo" && go build -o "$bin_dir/quarks" ./cmd/quarks )
install -m 755 "$repo/packaging/quarks-open" "$bin_dir/quarks-open"

echo "==> installing launcher + icons"
mkdir -p "$app_dir" "$icon_dir/scalable/apps" "$icon_dir/192x192/apps" "$icon_dir/512x512/apps"
install -m 644 "$repo/internal/web/static/favicon.svg"   "$icon_dir/scalable/apps/quarks.svg"
install -m 644 "$repo/internal/web/static/icon-192.png"  "$icon_dir/192x192/apps/quarks.png"
install -m 644 "$repo/internal/web/static/icon-512.png"  "$icon_dir/512x512/apps/quarks.png"

# a user-local icon theme dir needs its own index.theme or some loaders skip it
[ -f "$icon_dir/index.theme" ] || cp -f /usr/share/icons/hicolor/index.theme "$icon_dir/index.theme" 2>/dev/null || \
  printf '[Icon Theme]\nName=Hicolor\nDirectories=scalable/apps,192x192/apps,512x512/apps\n\n[scalable/apps]\nSize=48\nType=Scalable\nMinSize=8\nMaxSize=512\nContext=Applications\n\n[192x192/apps]\nSize=192\nContext=Applications\n\n[512x512/apps]\nSize=512\nContext=Applications\n' > "$icon_dir/index.theme"

# Install the launcher, then pin Icon= to an absolute path so it resolves even
# before icon caches refresh.
install -m 644 "$repo/packaging/quarks.desktop" "$app_dir/quarks.desktop"
sed -i "s|^Icon=quarks$|Icon=$icon_dir/scalable/apps/quarks.svg|" "$app_dir/quarks.desktop"

update-desktop-database "$app_dir" >/dev/null 2>&1 || true
gtk-update-icon-cache -f -t "$icon_dir" >/dev/null 2>&1 || true
kbuildsycoca6 >/dev/null 2>&1 || kbuildsycoca5 >/dev/null 2>&1 || true

echo "==> installing systemd --user service"
mkdir -p "$unit_dir"
install -m 644 "$repo/packaging/quarks.service" "$unit_dir/quarks.service"
if command -v systemctl >/dev/null 2>&1 && systemctl --user show-environment >/dev/null 2>&1; then
  systemctl --user daemon-reload
  systemctl --user enable quarks.service
  systemctl --user restart quarks.service   # pick up a rebuilt binary on re-run
  systemctl --user --no-pager --lines=0 status quarks.service || true
else
  echo "   (no systemd --user session here — start the server yourself: quarks &)"
fi

if [ ! -f "$cfg_dir/config.yaml" ]; then
  echo "==> seeding $cfg_dir/config.yaml"
  mkdir -p "$cfg_dir"
  install -m 644 "$repo/config.example.yaml" "$cfg_dir/config.yaml"
fi

cat <<EOF

Installed.
  server   : systemctl --user status quarks      (http://localhost:7373)
  window   : launch "Quark's" from your app menu, or run: quarks-open
  config   : $cfg_dir/config.yaml   (hot-reloaded on save)
  secrets  : $cfg_dir/secrets.yaml  (chmod 600; see secrets.example.yaml)

If ~/.local/bin isn't on your PATH, add it to your shell profile.
To keep the server running when logged out:  loginctl enable-linger $USER
EOF
