# Quark's

A self-hosted, modular personal feed dashboard — Radio-Canada, Reddit, YouTube,
Hacker News and weather on one screen. Glance-inspired, built from scratch in Go.
Read-only, no database, binds to `127.0.0.1`. Primary target Bazzite (Fedora
Atomic); runs on macOS too.

*Named for Quark's bar on Deep Space Nine — where you go to find out what's actually
going on. `Quark's` in prose; the binary and config paths use `quarks`.*

See **[`quarks-plan.md`](quarks-plan.md)** for the design and **[`PROGRESS.md`](PROGRESS.md)**
for build status.

## Status

Working: config-driven widgets on a schedule, on-disk cache (instant cold start),
per-widget failure isolation, YAML hot-reload, a viewport-filling dark/light layout
with tabbed cards, an inline reader view, and a user-scoped installer for Linux and
macOS. Not built yet: calendar, YouTube-subscription OAuth.

## Quick start

```bash
go build -o quarks ./cmd/quarks
mkdir -p ~/.config/quarks
cp config.example.yaml ~/.config/quarks/config.yaml   # then edit it
./quarks
# open http://127.0.0.1:7373
```

Flags: `--config <path>` (default `~/.config/quarks/config.yaml`, or
`~/Library/Application Support/quarks/config.yaml` on macOS), `--addr <host:port>`
(default `127.0.0.1:7373`). The config file is hot-reloaded on save.

**Widget types:** `rss` (also covers YouTube channel feeds and the Reddit home
feed via a secret URL), `hackernews`, `youtube` (channels + playlists), `reddit`,
`weather`, `nhl` (a team's schedule). `type: group` bundles several widgets into
one card with tabs. Secrets live in `secrets.yaml` next to the config
(`chmod 600`), referenced as `${secret:key}` — see `secrets.example.yaml` and
`config.example.yaml`.

**Keyboard:** `j` / `k` move between items, `Enter` opens the article inline
(extracted reader view), `o` opens the original, `Esc` closes reader panels.
`↻` buttons in each card and the top bar force a refresh.

**Layout:** the dashboard fills the window — the page doesn't scroll, each card
scrolls inside itself. `pages:` in the config gives title-bar tabs, each with its
own column layout.

## Install (user-scoped, no root)

```bash
make install     # build + install + start the background service
make uninstall   # undo  (./packaging/uninstall.sh --purge also drops config/cache)
```

`make install` detects the OS:

**Linux** — binary + `quarks-open` to `~/.local/bin`, a **systemd `--user`**
service (starts at login; `loginctl enable-linger $USER` to keep it running while
logged out), and a `.desktop` launcher with icons. Launch the window from your app
menu ("Quark's") or `quarks-open`.

**macOS** — binary + `quarks-open` to `~/.local/bin`, and a **launchd**
LaunchAgent (`~/Library/LaunchAgents/com.maxbasque.quarks.plist`, logs to
`~/Library/Logs/quarks.log`). Run `quarks-open` for the window. Needs Homebrew Go
to build (`brew install go`) and a Chromium-family browser (Chrome / Chromium /
Brave / Edge) for the app-window — Firefox dropped app-window support.

The window is a chromeless Chromium `--app=` window. Running standalone, a link
click opens in your **OS default browser** (`xdg-open` / `open`), not a second
Chromium window. For the tidiest taskbar/Dock integration, open the dashboard and
use Chrome's "Install Quark's…".

## Development

```bash
make test          # offline — providers run against recorded fixtures
make fakefeed      # terminal 1: fake YouTube / news / Reddit feeds on :7400
make dev           # terminal 2: quarks against config.fake.yaml (server only)
make open          # terminal 3: open the dashboard in an app-window
```

`make dev` starts only the server — nothing appears until you open
`http://localhost:7373`.

## Layout

```
cmd/quarks/          entrypoint, flags, signal handling
cmd/fakefeed/        dev-only fake feed server
internal/app/        wiring, provider registry, config hot-reload
internal/config/     YAML load + validate + ${secret:} / ${env} expansion
internal/core/       Item, Weather, Provider, registry, scheduler, store
internal/providers/  rss, hackernews, weather, reddit, youtube
internal/reader/     article extraction (go-readability + bluemonday)
internal/web/        handlers, templates, embedded static assets
packaging/           install.sh / uninstall.sh, service unit, launchd plist, .desktop
```
