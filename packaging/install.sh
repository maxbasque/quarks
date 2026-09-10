#!/usr/bin/env bash
# Install Quark's for the current user. Nothing here needs root.
#
#   Linux : binary + quarks-open -> ~/.local/bin
#           systemd --user service, .desktop launcher + hicolor icons
#   macOS : binary + quarks-open -> ~/.local/bin
#           launchd LaunchAgent (~/Library/LaunchAgents)
#
# Re-run any time to update. Undo with uninstall.sh.
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
os="$(uname)"
bin_dir="$HOME/.local/bin"

# go may live in a Homebrew prefix that isn't on a non-login PATH.
if ! command -v go >/dev/null 2>&1; then
  for p in /opt/homebrew/bin /home/linuxbrew/.linuxbrew/bin /usr/local/go/bin; do
    [ -x "$p/go" ] && PATH="$p:$PATH"
  done
fi
command -v go >/dev/null || { echo "install: 'go' not found — 'brew install go'"; exit 1; }

echo "==> building quarks"
mkdir -p "$bin_dir"
( cd "$repo" && go build -trimpath -ldflags="-s -w" -o "$bin_dir/quarks" ./cmd/quarks )
install -m 755 "$repo/packaging/quarks-open" "$bin_dir/quarks-open"

# ---- macOS ----------------------------------------------------------------
if [ "$os" = "Darwin" ]; then
  cfg_dir="$HOME/Library/Application Support/quarks"
  agent_dir="$HOME/Library/LaunchAgents"
  plist="$agent_dir/com.maxbasque.quarks.plist"

  echo "==> installing launchd agent"
  mkdir -p "$agent_dir"
  sed -e "s|__BIN__|$bin_dir/quarks|" -e "s|__HOME__|$HOME|g" \
    "$repo/packaging/com.maxbasque.quarks.plist" > "$plist"
  launchctl unload "$plist" 2>/dev/null || true
  launchctl load -w "$plist"

  if [ ! -f "$cfg_dir/config.yaml" ]; then
    echo "==> seeding $cfg_dir/config.yaml"
    mkdir -p "$cfg_dir"
    install -m 644 "$repo/config.example.yaml" "$cfg_dir/config.yaml"
  fi

  cat <<EOF

Installed.
  server  : launchctl list | grep quarks        (http://localhost:7373)
            logs: ~/Library/Logs/quarks.log
  window  : quarks-open   (needs Google Chrome / Chromium / Brave / Edge)
  config  : $cfg_dir/config.yaml   (hot-reloaded on save)
  secrets : $cfg_dir/secrets.yaml  (chmod 600; see secrets.example.yaml)

If ~/.local/bin isn't on your PATH, add it to your shell profile.
EOF
  exit 0
fi

# ---- Linux --------------------------------------------------------------
app_dir="$HOME/.local/share/applications"
icon_dir="$HOME/.local/share/icons/hicolor"
unit_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
cfg_dir="${XDG_CONFIG_HOME:-$HOME/.config}/quarks"

echo "==> installing launcher + icons"
mkdir -p "$app_dir" "$icon_dir/scalable/apps" "$icon_dir/192x192/apps" "$icon_dir/512x512/apps"
install -m 644 "$repo/internal/web/static/favicon.svg"   "$icon_dir/scalable/apps/quarks.svg"
install -m 644 "$repo/internal/web/static/icon-192.png"  "$icon_dir/192x192/apps/quarks.png"
install -m 644 "$repo/internal/web/static/icon-512.png"  "$icon_dir/512x512/apps/quarks.png"

# a user-local icon theme dir needs its own index.theme or some loaders skip it
[ -f "$icon_dir/index.theme" ] || cp -f /usr/share/icons/hicolor/index.theme "$icon_dir/index.theme" 2>/dev/null || \
  printf '[Icon Theme]\nName=Hicolor\nDirectories=scalable/apps,192x192/apps,512x512/apps\n\n[scalable/apps]\nSize=48\nType=Scalable\nMinSize=8\nMaxSize=512\nContext=Applications\n\n[192x192/apps]\nSize=192\nContext=Applications\n\n[512x512/apps]\nSize=512\nContext=Applications\n' > "$icon_dir/index.theme"

# pin Icon= to an absolute path so it resolves even before icon caches refresh
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
