# Quark's

A self-hosted, modular personal feed dashboard — Radio-Canada, Reddit, YouTube,
Hacker News, weather and calendar on one screen. Glance-inspired, built from scratch
in Go. Primary target: Bazzite (Fedora Atomic); macOS later. Read-only, no database,
binds to `127.0.0.1`.

*Named for Quark's bar on Deep Space Nine — where you go to find out what's actually
going on. `Quark's` in prose; the binary and config paths use `quarks`.*

See **[`quarks-plan.md`](quarks-plan.md)** for the full design and **[`PROGRESS.md`](PROGRESS.md)**
for build status.

## Status

**M0 done.** The skeleton runs: it loads a YAML config, schedules per-widget fetches,
caches results to disk, and serves a dark 3-column dashboard on `127.0.0.1:7373`.
Currently wired to one feed (Hacker News front page). See `PROGRESS.md`.

## Quick start

```bash
go build -o quarks ./cmd/quarks
mkdir -p ~/.config/quarks
cp config.example.yaml ~/.config/quarks/config.yaml
./quarks
# open http://127.0.0.1:7373
```

Flags: `--config <path>` (default `~/.config/quarks/config.yaml`), `--addr <host:port>`
(default `127.0.0.1:7373`). The config file is hot-reloaded on save.

**Keyboard:** `j` / `k` move between items, `Enter` opens the article inline
(extracted reader view), `o` opens the original, `Esc` closes reader panels.

**Widget types:** `rss` (also covers YouTube channel feeds and the Reddit home
feed via a secret URL), `hackernews`, `youtube`, `reddit`, `weather`. Secrets go
in `~/.config/quarks/secrets.yaml` (`chmod 600`) and are referenced as
`${secret:key}` — see `secrets.example.yaml`.

## Development

```bash
make test          # offline — providers run against recorded fixtures
make fakefeed      # terminal 1: fake YouTube / news / Reddit feeds on :7400
make dev           # terminal 2: quarks against config.fake.yaml (server only)
make open          # terminal 3: open the dashboard in a Chrome app-window
```

`make dev` starts only the server — nothing appears until you open
`http://localhost:7373` (via `make open`, or any browser tab).

## Install (Linux, user-scoped)

```bash
make install     # binary + quarks-open + systemd --user service + .desktop launcher
make uninstall   # undo (add --purge via ./packaging/uninstall.sh to drop config/cache)
```

No root, nothing layered onto the base image. The server runs as a systemd `--user`
service (starts at login; `loginctl enable-linger $USER` to keep it running logged
out). Launch the window from your app menu ("Quark's") or `quarks-open`.

The window is a chromeless Chromium/Chrome `--app=` window. When it runs standalone,
clicking a link opens it in your **OS default browser** (via `xdg-open`), not a
second Chromium window. For the cleanest taskbar integration, open the dashboard and
use Chrome's "Install Quark's…".

## Layout

```
cmd/quarks/          entrypoint, wiring
internal/config/     YAML load + validate
internal/core/       Item, Provider, registry, scheduler, store
internal/providers/  one package per feed type (rss so far)
internal/web/        handlers + templates + static assets (embedded)
```
