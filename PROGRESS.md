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
| M2 | The feeds: YouTube (imported list), Hacker News (Algolia), Reddit (private home feed + secrets file), weather | not started |
| M3 | Reader view: inline article extraction, keyboard nav (j/k, Enter, o) | not started |
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
internal/app/app.go             wiring + config hot-reload (generations)
internal/config/config.go       YAML load + validate + defaults
internal/core/
  item.go                       normalized Item
  provider.go                   Provider interface, WidgetConfig, ParseWidget
  registry.go                   type name → Factory
  scheduler.go                  per-widget fetch loops, WaitGroup drain
  store.go                      in-memory + on-disk snapshot, reload-safe
internal/providers/rss/rss.go   generic feed provider (gofeed)
internal/web/
  web.go                        handlers, view models, Meta (published on reload)
  templates/index.html          html/template dashboard
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

**M2 — the feeds.** In rough order:
1. **Hacker News via Algolia** (`hn.algolia.com/api/v1/search?tags=front_page`) — a
   dedicated provider so items carry Score + Comments + CommentsURL. Keep the hnrss
   feed working via the generic rss provider as the fallback.
2. **Open-Meteo weather** — its own small type + renderer (not an Item), config
   `latitude`/`longitude`.
3. **Secrets file** — `~/.config/quarks/secrets.yaml` (0600) + `${VAR}` / `${secret:...}`
   substitution in config values. Needed before Reddit/calendar.
4. **Reddit** — private home-feed RSS through the generic rss provider (secret URL from
   the secrets file); public `r/<sub>.json` provider for specific subreddits with a real
   User-Agent.
5. **YouTube** — per-channel RSS (`youtube.com/feeds/videos.xml?channel_id=…`), channel
   IDs from config. (Needs answers to §14 Q3/Q4.)

Also outstanding: verify Radio-Canada feed URLs (§14 Q2); confirm the Chrome `--app=`
window WM_CLASS on the Bazzite box.
