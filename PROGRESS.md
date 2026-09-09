# Quark's — progress log

Companion to `quarks-plan.md` (the design, the source of truth). This file tracks
*what has actually been built and run*. Update it at the end of each work session.

**Language:** Go 1.27 (locked 2026-09-08, resolves plan §14 Q1).
**Box:** Bazzite Kinoite, **KDE** (resolves plan §14 Q6). Google Chrome flatpak
(`com.google.Chrome`) already installed — satisfies the Chromium-family requirement
for the app-window; launchers must call `flatpak run com.google.Chrome --app=…`.

---

## Milestones

| # | Milestone | State |
|---|---|---|
| M0 | Skeleton: config → registry → scheduler → store → web, one hardcoded feed | **✅ done (2026-09-08)** — HN frontpage rendering in browser |
| M1 | It looks good: multi-column, cards, thumbnails, dark theme, auto-refresh, stale badges, YAML hot-reload | **✅ done (2026-09-08)** |
| M2 | The feeds: YouTube (imported list), Hacker News (Algolia), Reddit (private home feed + secrets file), weather | **✅ done (2026-09-08)** |
| M3 | Reader view: inline article extraction, keyboard nav (j/k, Enter, o) | **✅ done (2026-09-08)** |
| M4 | Calendar: ICS subscription, agenda widget | not started |
| M5 | Packaging: systemd --user unit, .desktop + StartupWMClass, Makefile, README | **✅ done (2026-09-08)** — installed + verified on the Bazzite box |
| M6 | OAuth subsystem: YouTube subscription sync, token storage/refresh, `quarks auth youtube` | not started |
| M7 | Optional shells: Wails native window or Bubble Tea TUI | not started |

---

## M0 — done

What works, verified on the Bazzite box 2026-09-08:

- `quarks` binary builds (`go build -o quarks ./cmd/quarks`), no CGo, stdlib + 2 deps
  (`mmcdole/gofeed`, `gopkg.in/yaml.v3`).
- Loads `~/.config/quarks/config.yaml` (`--config` to override).
- Registry resolves `type: rss` → generic gofeed provider (RSS/Atom/JSON Feed).
- Scheduler: one goroutine per widget, immediate fetch then on TTL, 30s fetch timeout,
  per-widget failure isolation.
- Store: in-memory + atomic JSON snapshot per widget in `~/.cache/quarks/`. Cold start
  reads the snapshot, so the UI is never blank.
- Web UI on `127.0.0.1:7373` (`--addr` to override): 3-column dark layout, cards, item
  links open in a new tab, freshness badge (`stale · Nm` / `offline`), meta-refresh 60s.
- Confirmed: real Hacker News front-page headlines render; snapshot file written;
  `stale`/`offline` badge logic exercised via the fetch-cancelled path.

---

## Restyle: soft dark + monospace (2026-09-09)

Dark-only now — light palette and the theme toggle are gone (`window.theme` still
parses, just unused). One soft, slightly warm dark palette (Tokyo-Night-ish);
IBM Plex Sans bundled (`static/fonts/*.woff2`, SIL OFL, embedded via
`//go:embed static`) and used throughout. Thinner scrollbars, quieter borders,
softer accent/link/warn/danger tokens, item row hover.

---

## Top-level pages + NHL provider (2026-09-09)

- **Pages**: `config.pages: [{name, columns, column_weights, widgets}]` gives the app
  title-bar tabs, each its own column layout (Glance's pages concept). Legacy flat
  `widgets:` still works (becomes one unnamed page). All widgets across all pages
  fetch on schedule; the client shows one page at a time (localStorage `quarks-page`).
  `config.Config.Pages`, `web.Meta.Pages`; the template wraps each page's grid in
  `#pages` and in-place refresh swaps that whole container.
- **`internal/providers/nhl`**: `type: nhl`, `team: MTL` — a team's season schedule
  from `api-web.nhle.com` (keyless), one item per game, soonest first, old games
  dropped after ~30h. Title "vs Senators" / "@ Maple Leafs" from the team's side,
  opponent logo as thumbnail, venue (or score, once games are played) as summary,
  gamecenter URL.
- **Future-aware relative time**: `ago()` / JS `rel()` render "in 3h" / "in 10d" for
  upcoming items (NHL games), with a <1h future window treated as "just now" to
  absorb feed/clock skew.

---

## Item summaries (2026-09-08)

`rss` items now carry a `Summary` — the feed's `<description>`/`content` stripped
to plain text, whitespace-collapsed, capped ~260 chars, shown 2-line-clamped under
the title. `<img>` in the description is harvested as a thumbnail when the item
has none (helps VGC and other WordPress feeds). Reddit link-post boilerplate
("submitted by /u/… to r/… [link] [comments]") is filtered out. Per-widget
`summary: false` opt-out; `youtube` disables it (descriptions are link walls).
`core.Item.Summary` is separate from `Body` (the lazy reader-view field).

---

## Tabbed-card header two rows (2026-09-08)

A tab group's header now stacks: label + status on top, the tab strip on its own
row below — so 5+ tabs fit instead of being clipped by the status area.
Single-widget cards keep the one-line title + status header. Templates gained a
shared `boxstatus` define.

---

## Column weights (2026-09-08)

`window.column_weights: [1, 1.6, 1]` sizes columns relative to each other (via a
`--grid-cols` custom property; media-query collapse still wins at narrow widths).
Ignored unless the list length matches `columns`.

Note: YouTube's RSS endpoint is persistently IP-blocking the target network (every
channel 404s). Swapped the box's video widget for Radio-Canada's `rad/reportages`
feed. Real RC section feeds: `ici.radio-canada.ca/info/rss/info/{a-la-une,en-continu,
en-bref,rad/reportages}` and `…/<section>/en-continu` (politique, international,
economie, sante, grandmontreal, …).

---

## Manual refresh + cross-platform open (2026-09-08)

- **Refresh buttons**: a ↻ in each card header (refetches the active tab's widget)
  and one in the top bar (refetches everything). `POST /refresh` (optional `key`)
  → `Scheduler.Refresh` fetches now, bypassing the TTL, blocking until done; the
  frontend then does an in-place reload. Per-widget mutex so a manual refresh and
  a scheduled tick can't run the same fetch concurrently.
- **`openInBrowser`** (`internal/web/open.go`): `/open` now switches on GOOS —
  `open` (macOS), `xdg-open` (Linux), `rundll32` (Windows). The binary builds for
  `GOOS=darwin` clean; the packaging scripts are still Linux-only.

---

## YouTube playlists (2026-09-08)

`type: youtube` now takes `playlists: [PL…]` alongside `channels: [UC…]` — both
become `youtube.com/feeds/videos.xml` URLs (`?playlist_id=` / `?channel_id=`),
keyless. Rejects `WL`/`LL` with a message pointing at the unlisted-playlist
workaround. There is still no subscriptions feed without OAuth (M6) — the list
comes from a Google Takeout export.

---

## Layout v2 — viewport fit + tab groups (2026-09-08)

Both from user feedback after M5.

- **Viewport-fit layout**: the page no longer scrolls. `body` is a flex column at
  `100dvh`; the grid fills the rest; each column can scroll; each card is
  `flex: 1 1 0` and scrolls *inside itself*. Weather cards (and empty/offline
  feeds) are content-sized instead (`:has()` selectors). Falls back to normal
  document flow below 720px.
- **Tab groups**: `type: group` (or `tabs`) in the config bundles several widgets
  into one card with a tab bar. Each tab is still a normal widget instance (own
  provider, schedule, cache, key) — the group is purely a layout grouping.
  `config.Config` now exposes `[]Box` (was flat `[]WidgetConfig`); `web.Meta`
  carries `[]Box{Column, Order, Title, Members}`. Active tab persists per card in
  `localStorage`; the freshness/badge line follows the active tab.
- **In-place refresh** now preserves each panel's scroll position (keyed by widget
  key) and re-applies the active tab.
- **Feed interleaving**: the `rss` provider gained `interleave: true` — with
  several feeds it round-robins (newest from each, then next from each) instead of
  merging into one date-sorted list, so a busy feed can't crowd the others out.
  `youtube` sets it by default. Tested.

---

## M5 — packaging (2026-09-08) — installed & verified on the Bazzite box

- **`packaging/install.sh`** (`make install`), fully user-scoped, no root:
  binary → `~/.local/bin/quarks`, `quarks-open` helper alongside, systemd `--user`
  service enabled + (re)started, `.desktop` + hicolor icons (svg/192/512), seeds
  `~/.config/quarks/config.yaml` if absent. **`uninstall.sh`** (`make uninstall`,
  `--purge` also drops config/cache).
- **`quarks-open`** — opens the dashboard in a chromeless app-window, trying
  Flatpak Chrome/Chromium/Brave/Edge then native binaries. Firefox unsupported
  (no SSB mode). `make open` uses it.
- **`StartupWMClass=chrome-localhost__-Default`** — verified via a KWin script
  against the live window (KDE Wayland, Chrome flatpak). The port is *not* in the
  id.
- **Web app manifest** (`/manifest.webmanifest` + icons + `theme-color`) so
  Chrome's "Install Quark's…" is available as the zero-config PWA path.
- **`POST /open`** → `xdg-open`: `app.js` routes external link clicks through it
  **only when running standalone** (`display-mode: standalone`), so links open in
  the OS default browser (Firefox) instead of a second Chromium window. Normal
  browser tabs keep native behaviour. http/https only.
- systemd unit: `Restart=on-failure`, `NoNewPrivileges`, `PrivateTmp`,
  `WantedBy=default.target` (starts at login; `loginctl enable-linger` for
  logged-out).

Verified: `systemctl --user` service running and serving :7373; app-window opens
chromeless with the right taskbar icon. Not tested: an actual reboot.

---

## M3 — reader view (2026-09-08)

- **`internal/reader`**: `Reader.Get(ctx, url)` fetches a page, runs
  `go-shiori/go-readability`, sanitizes the extracted HTML with `bluemonday`
  (UGCPolicy + nofollow), and caches the result in memory (1h TTL, 64 entries,
  crude eviction). 8 MiB body cap; http/https only; rejects non-HTML responses.
- **`GET /reader?url=…`**: renders one article via `reader.html`. Works as a plain
  standalone page (no JS — the "read here" links just navigate) *and* as a fragment
  that `app.js` lifts the `<article>` out of and expands inline under the item.
- **Keyboard nav** (`app.js`): `j`/`k` move focus through items (outline), `Enter`
  toggles the inline reader on the focused item, `o` opens the original, `Esc`
  closes all reader panels. Ignored while typing in a field.
- In-place refresh is **paused while any reader panel is open**, so the page never
  gets yanked out from under you mid-read.
- Tests: extraction + script/handler stripping + caching + scheme rejection.
- New deps: `go-shiori/go-readability`, `microcosm-cc/bluemonday`.

---

## M2 (part 2) — secrets, Reddit, YouTube (2026-09-08)

- **Secrets + substitution** (`internal/config/secrets.go`): `${VAR}` (environment)
  and `${secret:key}` (from `secrets.yaml` beside the config) are expanded in the raw
  config bytes before YAML parsing, so a token works anywhere. `secrets.yaml` must be
  mode 0600 or Load refuses it. A missing secrets file is fine; unresolved tokens
  become empty and are logged. The config watcher now also watches `secrets.yaml`.
  `.gitignore` blocks `secrets.yaml` and `config.yaml`; `secrets.example.yaml` added.
- **`internal/providers/reddit`** — public subreddit listings via `r/<subs>/<sort>.json`
  (scores + comments). Detects an HTML/blocked response and returns a clear error
  pointing at the private-RSS path. **The `.json` endpoint 403s outside a residential
  IP** — confirmed from this dev environment; expected to work on the Bazzite box.
- **Reddit home feed** needs *no new code* — it's a secret RSS URL through the generic
  `rss` provider: `feeds: [ "${secret:reddit_home}" ]`.
- **`internal/providers/youtube`** — thin wrapper over `rss`: `channels: [UC…]` →
  per-channel Atom feed URLs. `rss.NewWithFeeds` added for this reuse. Empty source →
  items get the channel name. Verified live (Fireship, Veritasium) with thumbnails.
- Tests for reddit, youtube, and config secret/env substitution + the 0600 check.
- Web: suppress an item's Author when it equals its Source (YouTube sets both).

---

## M2 (part 1) — Hacker News + weather (2026-09-08)

- **Provider contract generalized:** `Provider.Fetch` now returns `core.Payload`
  (`{ Items []Item; Weather *Weather }`) instead of `[]Item`. Feed providers use
  `core.Feed(items)`; the widgets that don't fit the Item shape (weather now,
  calendar later) fill their own field. Store/scheduler/web updated; snapshot JSON
  now carries `weather`.
- **`internal/providers/hackernews`** — Algolia search API
  (`hn.algolia.com/api/v1/search`). One request → points + comment counts +
  discussion URL, which hnrss doesn't give. Config: `tags` (default `front_page`),
  `query`. Ask/Show HN text posts link to the HN item.
- **`internal/providers/weather`** — Open-Meteo (`api.open-meteo.com/v1/forecast`),
  keyless. Config: `latitude`, `longitude`, `location`, `forecast_days` (default 4).
  Current temp + feels-like + WMO condition + today's high/low + N-day forecast.
  Rendered as its own card (emoji icons, forecast strip), not a feed list.
- Tests for both providers against canned responses via `httptest`.
- `config.fake.yaml` now includes both (they hit real keyless APIs); example config
  updated.

Known rough edge: widget keys strip non-ASCII (`Montréal` → `montr-al`). Internal
only (cache filename), never shown. Fix when it matters.

---

## Test harness — done (2026-09-08)

- **`cmd/fakefeed`** — dev-only server (`make fakefeed`, `127.0.0.1:7400`) serving
  `/youtube.xml` (Atom + `media:group/media:thumbnail`, like a real YouTube channel
  feed), `/news.xml` (RSS 2.0 + `dc:creator`), `/reddit.xml` (Atom), plus `/img/<seed>`
  SVG placeholders so thumbnails render fully offline. Timestamps are regenerated on
  every request (newest ~4m old, spreading ~40m/item), so relative-time rendering
  gets exercised. `config.fake.yaml` + `make dev` wire quarks to it.
- **`internal/providers/rss/rss_test.go`** — first real tests, against `testdata/*.xml`
  fixtures served via `httptest` (no live network — `go test ./...` passes offline).
  Covers: YouTube nested-`media:group` thumbnail extraction, bare `media:thumbnail`,
  `dc:creator` → Author, missing pubDate → zero time, entity decoding, newest-first
  ordering, all-feeds-fail error, no-feeds config error.
- Fixed `rss` provider thumbnail extraction to handle the `media:group` nesting
  (YouTube) as well as a bare `media:thumbnail`.

---

## M1 — done (2026-09-08)

- **Hot-reload:** wiring moved to `internal/app`. A 2s mtime poll on the config file
  triggers a reload — rebuild providers, swap in a fresh scheduler, cancel + drain the
  old one, republish window metadata. A config that fails to parse is logged and the
  previous generation keeps serving. Verified: adding/removing widgets live.
- **Stable widget keys:** title-derived (not index-prefixed), so reordering widgets in
  the config keeps their disk cache. `WidgetState.Order` drives UI ordering.
- **Design pass:** sticky top bar with wordmark + theme toggle (localStorage, overrides
  config `theme:`), light + dark palettes, cards with thumbnails, per-item source /
  author / relative-time / score / comments, per-widget "updated Nm ago" + stale badge.
- **Client JS (progressive enhancement):** relative timestamps tick every 30s;
  in-place refresh every 60s (fetch `/`, swap `.grid`, no scroll jump) + on tab focus;
  works with JS disabled, just static.
- **Packaging (M5 partial):** `packaging/quarks.service` (systemd --user),
  `packaging/quarks.desktop` (+ StartupWMClass guess), `packaging/install.sh`,
  `make install`. Screenshots: `~/Downloads/quarks-dash.png`, `quarks-light.png`.

Not yet verified: the Chrome `--app=` window's actual WM_CLASS (needs a live window —
run `xprop WM_CLASS` on it and fix `quarks.desktop` if the taskbar icon is generic).

---

## Repo layout (built so far)

```
cmd/quarks/main.go              flags, signal handling → app.Run
cmd/fakefeed/main.go            dev-only fake feed server
internal/app/app.go             wiring, provider registry, config hot-reload
internal/config/
  config.go                     YAML load + validate + defaults
  secrets.go                    ${VAR} / ${secret:key} expansion, 0600 check
internal/core/
  item.go                       normalized Item
  weather.go                    Weather / WeatherDay + WMO code labels
  provider.go                   Provider iface, Payload{Items,Weather}, WidgetConfig
  registry.go                   type name → Factory
  scheduler.go                  per-widget fetch loops, WaitGroup drain
  store.go                      in-memory + on-disk snapshot, reload-safe
internal/providers/             (each: <name>.go + <name>_test.go)
  rss/       generic feed provider (gofeed) + testdata/*.xml
  hackernews/  Algolia search API
  weather/     Open-Meteo
  reddit/      r/<subs>.json  (fragile — residential IP)
  youtube/     channels/playlists → Atom, wraps rss
  nhl/         a team's season schedule (api-web.nhle.com)
internal/reader/reader.go       article extraction (go-readability + bluemonday)
internal/web/
  web.go                        handlers (/, /reader, /open, /manifest), Meta
  templates/{index,reader}.html html/template
  static/{style.css,app.js,favicon.svg,icon-*.png,manifest.webmanifest}
packaging/
  install.sh, uninstall.sh      user-scoped install (make install / uninstall)
  quarks.service                systemd --user unit
  quarks.desktop                app launcher (+ StartupWMClass)
  quarks-open                   chromeless app-window launcher
config.fake.yaml                UI-dev config pointing at fakefeed
config.example.yaml, secrets.example.yaml
  static/{style.css,app.js,favicon.svg}
packaging/{quarks.service,quarks.desktop,install.sh}
config.example.yaml
```

---

## Open questions (from plan §14) — current status

1. ~~Language~~ → **Go**, locked.
2. **Radio-Canada RSS URLs** — still placeholders. Blocks adding the RC widget. *TODO before M1/M2.*
3. **Concrete feed list** — which subreddits, which YouTube channels, which RC sections. *Needed for M2.*
4. **YouTube subscription import method** — Takeout CSV vs one-off OAuth call. *Needed for M2.*
5. **Which calendar** (Google/Proton/Nextcloud) for the ICS export path. *Needed for M4.*
6. ~~KDE or GNOME~~ → **KDE**, confirmed.

---

## Next session — start here

M0–M3 + M5 are done. The dashboard is installed and running on the Bazzite box.
**M4 (calendar) is skipped for now** by request; **M6 (OAuth for YouTube subs) is
still deferred** per the plan.

On the box, `~/.config/quarks/config.yaml` now has 5 real widgets (HN, Radio-Canada,
Reddit, YouTube×2, weather) — this file is local, not in the repo.

- **Radio-Canada** (§14 Q2, partly resolved): `https://ici.radio-canada.ca/info/rss/info/a-la-une`
  works. Pattern is `…/info/rss/info/<section>` but only `a-la-une` responded of the
  sections tried — the section list still needs digging.
- **Reddit `type: reddit` 403s from this box too** — not just the dev env. So the
  private-RSS home-feed path (a `${secret:reddit_home}` URL through `type: rss`) is
  the one to use here. Needs the user's secret URL from reddit.com/prefs/feeds/.
- YouTube: with two channels of different posting frequency, the busier one
  dominates the (date-sorted) list — possible future tweak (round-robin / per-channel cap).
- Whatever annoys you in daily use — that's the M1–M5 review the plan's M7 calls
  for (Wails native shell / Bubble Tea TUI are both optional).

If M4 comes back: ICS subscribe (secret URL), parse with `arran4/golang-ical`,
agenda widget as its own payload type like Weather. Buildable against a fixture
.ics before a real calendar URL exists (§14 Q5).
