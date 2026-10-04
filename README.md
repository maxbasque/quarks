# Quark's

A self-hosted, modular personal feed dashboard — Radio-Canada, Reddit, YouTube,
Hacker News and weather on one screen. Glance-inspired, built from scratch in TypeScript on Deno.
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
make build                     # deno compile → a standalone ./quarks binary
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
`weather`, `nhl` (a team's schedule), `standings`, `f1` (race calendar, or
driver / constructor standings, via the keyless Jolpica API), `onthisday`, `potd`,
`spotify` (upcoming releases from your followed artists — see Settings below).
`type: group` bundles several widgets into one card with tabs. Secrets live in
`secrets.yaml` next to the config (`chmod 600`), referenced as `${secret:key}`
— see `secrets.example.yaml` and `config.example.yaml`.

**Settings:** the gear icon in the header opens `/settings`, an in-app page for
things that shouldn't need hand-editing YAML: switching pages and their named
columns on and off (saved to `layout.yaml` beside the config — `config.yaml` is never
rewritten), adding secrets such as `reddit_home`, and connecting a Spotify
account (OAuth happens in-window; it writes `secrets.yaml` for you and shows
you the `config.yaml` block to paste in for a Media tab).

**Columns:** a page's `columns:` is either a count (widgets place themselves
with `column: N`) or a list of named columns, each with its own `widgets:`,
an optional `weight` and `enabled: false` to start hidden; `max_columns`
(default 3) caps how many show at once. Pages take `enabled: false` too, with
no limit on how many are shown; a hidden page isn't fetched. The shipped `config.example.yaml`
needs no secrets: Accueil starts with Nouvelles + Aujourd'hui, and Niches
(which includes your Reddit front page) is off until you add `reddit_home`.

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

**Linux** — binary + `quarks-window` + `quarks-open` to `~/.local/bin`, a
**systemd `--user`** service (starts at login; `loginctl enable-linger $USER` to
keep it running while logged out), and a `.desktop` launcher with icons. Launch
the window from your app menu ("Quark's") or `quarks-open`. The window is
`quarks-window` (`cmd/quarks_window.ts`, run as `quarks window`), a native
WebKitGTK viewer on the running service — no browser needed; closing it leaves
the service running. Nothing to compile: it loads the webview library through
FFI, and `make install` downloads that library (pinned by SHA-256) to
`~/.local/lib/quarks`. It's built for GTK 4 + WebKitGTK 6.0, which Fedora /
Bazzite ship. `make window` reinstalls just the window.

**macOS, for everyone else** — download `Quarks-<version>.dmg` from the
GitHub releases page and drag Quark's onto Applications (step-by-step French
guide: [`packaging/macos/INSTALLER.md`](packaging/macos/INSTALLER.md)). It's a
native app (`cmd/quarks_mac.ts`): the server and a WebKit window in one process,
so opening it starts everything and quitting or closing the window stops
everything — no browser needed, nothing starts at login. It isn't signed with
an Apple developer account, so macOS asks for *Open Anyway* once. Built on
GitHub's macOS machines by `.github/workflows/macos-app.yml`: a `v*` tag
publishes a release; a push to a `mac/...` branch builds a test `.dmg`.

**macOS, from source** — binary + `quarks-open` to `~/.local/bin`, and a **launchd**
LaunchAgent (`~/Library/LaunchAgents/com.maxbasque.quarks.plist`, logs to
`~/Library/Logs/quarks.log`). Run `quarks-open` for the window. Needs Deno to
build (`brew install deno`) and a Chromium-family browser (Chrome / Chromium /
Brave / Edge) for the app-window — Firefox dropped app-window support.

In every window — Linux, the macOS app, or the macOS from-source Chrome app
window — clicking an article opens it in your **OS default browser**
(`xdg-open` / `open`); "lire ici" / Enter reads it inline.

## Development

Needs [Deno](https://deno.com) 2 (`curl -fsSL https://deno.land/install.sh | sh`).

```bash
make test          # offline — providers run against recorded fixtures
make check         # type-check + lint
make fakefeed      # terminal 1: fake YouTube / news / Reddit feeds on :7400
make dev           # terminal 2: quarks against config.fake.yaml (server only)
make open          # terminal 3: open the dashboard in an app-window
```

`make dev` starts only the server — nothing appears until you open
`http://localhost:7373`.

## Layout

```
cmd/quarks.ts          server entrypoint (flags, signals) + `quarks window`
cmd/quarks_window.ts   Linux native window (webview over FFI)
cmd/quarks_mac.ts      macOS app: window on the main thread, server in a worker
cmd/fakefeed.ts        dev-only fake feed server
src/app.ts             wiring, provider registry, config hot-reload
src/config/            YAML load + validate + ${secret:} / ${env} expansion
src/core/              Item, Weather, Provider, registry, scheduler, store
src/providers/         rss (+ feed parser), hackernews, weather, reddit, youtube, …
src/reader.ts          article extraction (Mozilla Readability + sanitize-html)
src/web/               handlers, templates, static assets
src/window/            libwebview FFI binding, GTK and Cocoa helpers
packaging/             install.sh / uninstall.sh, service unit, launchd plist, .desktop
```
