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
| M5 | Packaging: systemd --user unit, .desktop + StartupWMClass, Makefile, README | **partial** — files written + `make install`; app-window WM_CLASS not yet verified on a live window |
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
  youtube/     channels → per-channel Atom, wraps rss
internal/reader/reader.go       article extraction (go-readability + bluemonday)
internal/web/
  web.go                        handlers (/, /reader), view models, Meta
  templates/{index,reader}.html html/template
  static/{style.css,app.js,favicon.svg}
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

**M4 — calendar.** Subscribe to ICS URLs (secret URL from the secrets file), parse
with an ICS library (`arran4/golang-ical` or similar), render an agenda widget
(next N events, grouped by day). New `core` payload field like Weather, or a
generic events type. Needs §14 Q5 (which calendar) for a real ICS export URL, but
can be built and tested against a fixture .ics first.

Then **M5 — packaging polish** (mostly done; verify WM_CLASS) and **M6 — OAuth**.

Quick wins with real value:
- Point a real config at the Bazzite box; check `type: reddit` works there (403s
  from the dev environment).
- Verify Radio-Canada feed URLs (§14 Q2) — still placeholders.

Still needed from the user for a personalized dashboard (not blockers): real
subreddit list, YouTube channel IDs, RC sections, the Reddit home-feed secret URL,
which calendar.
