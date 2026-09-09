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
| M1 | It looks good: multi-column, cards, thumbnails, dark theme, auto-refresh, stale badges, YAML hot-reload | not started |
| M2 | The feeds: YouTube (imported list), Hacker News (Algolia), Reddit (private home feed + secrets file), weather | not started |
| M3 | Reader view: inline article extraction, keyboard nav (j/k, Enter, o) | not started |
| M4 | Calendar: ICS subscription, agenda widget | not started |
| M5 | Packaging: systemd --user unit, .desktop + StartupWMClass, Makefile, README | not started |
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

Not yet done in M0 (deferred to M1): the actual Chrome `--app=` window + `.desktop`
file, YAML hot-reload, thumbnails, keyboard nav, real styling polish.

---

## Repo layout (built so far)

```
cmd/quarks/main.go            flags, wiring, HTTP + scheduler lifecycle
internal/config/config.go     YAML load + validate + defaults
internal/core/
  item.go                     normalized Item
  provider.go                 Provider interface, WidgetConfig, ParseWidget
  registry.go                 type name → Factory
  scheduler.go                per-widget fetch loops
  store.go                    in-memory + on-disk snapshot
internal/providers/rss/rss.go generic feed provider (gofeed)
internal/web/
  web.go                      handlers, view models, stale-badge logic
  templates/index.html        html/template dashboard
  static/style.css            dark theme
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

M1. In rough order:
1. Chrome `--app=` window: confirm `flatpak run com.google.Chrome --app=http://localhost:7373`
   opens chromeless. Add a `quarks-open` helper script.
2. YAML hot-reload (fsnotify or a mtime poll) — rebuild providers on config save.
3. Layout/design pass on the dashboard — thumbnails in the Item, card polish, per-widget
   "updated Nm ago", relative-time that refreshes client-side.
4. Then M2: add the Algolia HN provider (scores + comments), Open-Meteo weather.
