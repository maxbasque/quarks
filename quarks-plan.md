# Quark's — a modular personal feed dashboard

*Named for Quark's bar on Deep Space Nine — where you go to find out what's actually going
on. Written `Quark's` in prose; the binary, config paths and unit files use `quarks`.*

A self-hosted, modular dashboard pulling your important feeds into one screen:
Radio-Canada, Reddit, YouTube, Hacker News, weather, calendar. Glance-inspired, built from
scratch. Primary target Bazzite (Fedora Atomic); macOS later.

---

**Status:** design complete, no code written. Last updated 2026-09-08.

**How to use this document.** It is the single source of truth for the project and is written
to be read cold — by future-you on a different machine, or by an AI assistant with no memory
of the conversation that produced it. Sections 1–11 are the design and the reasoning behind
it; §12 is the build order; §14 lists what still needs deciding or verifying. Start at §12,
and read backwards into whichever section a milestone references.

**No code has been written and nothing here has been run on real hardware.** Third-party APIs
drift, so it matters which claims were actually checked:

*Verified against primary sources on 2026-09-08:* Glance's stack, config format, absence of a
database and secrets handling (§2); Reddit's private RSS feeds still being issued (confirmed on
a live account); Google's 7-day refresh-token expiry in "Testing" publishing status (§9);
Bazzite's recommended software-installation methods (§4); Deno and Bun standalone-binary
compilation and cross-compilation (§3).

*Not verified — from general knowledge, spot-check before relying on them:* the Radio-Canada
feed URLs (already flagged in §14), YouTube's per-channel RSS URL format, the Hacker News
Algolia endpoint, Open-Meteo's endpoint and key-free access, YouTube Data API quota costs, and
the specific library names in §11.

---

## 1. Locked decisions

| Decision | Choice |
|---|---|
| Surface | Desktop window — via a browser app-window in v1 (§5). TUI possible later. |
| Interaction | Click → opens in browser. Text-heavy items expand inline in a reader view. |
| Platforms | Linux first (immutable-distro friendly), macOS later, **no mobile** |
| Storage | **No database.** Config file + secrets file + cache directory. (§8) |
| Personalization | Reddit via private RSS **now**; YouTube via imported list in v1, OAuth at M6. (§9) |
| Scope philosophy | Read-only. Never posts, votes, or writes to any service. |

---

## 2. Reference: how Glance actually works

Worth writing down, since it's the thing we're modelling on. Verified against the repo and
its configuration docs:

- **Backend:** Go
- **Frontend:** vanilla JS and CSS. No framework, no build step. Its contributing guide
  explicitly states *"No `package.json`"*
- **Distribution:** a single binary under 20MB, or a Docker image
- **Config:** YAML (`glance.yml`), structured as pages → columns → widgets
- **Storage:** **stateless, config-file only.** No SQLite, no Postgres, nothing
- **Secrets:** `${ENV_VAR}` substitution anywhere in the config, plus `${secret:filename}`
  and `${readFileFromEnv:VAR}`
- **Cache:** in-memory, cleared whenever the config reloads
- **Auth:** it has its own optional username/password login, with brute-force protection
- **Access:** a web server on port 8080 that you open **in a browser**. There is no mention
  anywhere of a desktop application, native window, or Electron.

**The key realization: Glance is not a desktop app.** It's a local web server that people set
as their browser homepage. Everything below follows from taking its architecture and adding
the window.

### What we copy, and what we change

| | Glance | Quark's |
|---|---|---|
| Backend, config format, widget model | Go, YAML, columns of widgets | same |
| Frontend | vanilla JS/CSS, no build step | same |
| Storage | no database | same |
| Cache | in-memory, lost on reload | **on disk** — instant cold start, kinder to APIs while developing |
| Dashboard login | optional user/password | **none needed** — binds to `127.0.0.1` |
| Surface | browser tab | **app-window + systemd user service** |

Mirroring Glance's shape has a side benefit worth stating: **its source becomes our reference
implementation.** Stuck on widget structure or an RSS edge case? Go read how they solved it.
For a first project in an unfamiliar language that's worth a great deal.

---

## 3. Stack: Go (recommended, not yet locked)

**Go for the core, HTML/CSS/vanilla JS for the UI, no framework, no build step.**

Why Go here:

- **Single static binary, zero runtime deps.** Decisive on Bazzite: an rpm-ostree distro
  punishes anything needing system packages. A Go binary drops into `~/.local/bin` and runs.
- `GOOS=darwin go build` gets macOS later. Core and UI unchanged.
- Mature libraries for every piece (§11), plus Bubble Tea if the TUI ever happens.
- Glance parity → its source is readable as a reference (§2).
- Good first non-JS project: mostly standard library, no generics puzzles, no framework
  conventions, no borrow checker.

### The honest counter-argument: Bun or Deno

This deserves recording, because it undercuts part of the case above.

The Go argument leaned hard on *"single binary, zero deps."* But `bun build --compile` and
`deno compile` both produce standalone executables with the runtime bundled, and both
cross-compile to macOS via `--target`. **That argument no longer uniquely favours Go.** You'd
get identical deployment, in TypeScript, in a language already known.

And this app isn't a domain where Go's strengths surface. Fetching twenty feeds concurrently
is `Promise.all` — arguably *simpler* in JS than goroutines. No CPU-bound work, no threading
requirement.

| | Go | Bun / Deno |
|---|---|---|
| Binary size | ~20MB | ~50–90MB (embeds a JS engine) — irrelevant for a desktop app |
| Feed libraries | `gofeed`, very battle-tested | `rss-parser` etc., fine |
| Long-running daemon track record | excellent | younger |
| Reference implementation | Glance is Go | — |
| Time-to-first-feature | new language | **immediate** |
| One language across front and back | no | **yes**, shared types for `Item` |

**The real question is what you want out of this.** Learning Go → take Go, and Glance becomes
your tutorial. Finishing fast → take Deno or Bun, since the failure mode for hobby projects is
abandonment, not bad architecture.

Leaning Go. Not clear-cut. **Everything else in this document is language-agnostic** —
switching to TypeScript is a find-and-replace here, not a redesign.

Ruled out: **Rust** (hardest language, and this app needs nothing it offers), **Python**
(worst packaging story on an immutable distro, no single binary), **Node** (distribution is a
folder plus `node_modules`; Bun/Deno do the binary better), **C#/.NET** (genuinely good,
single-file AOT — but a bigger conceptual jump than Go for no extra payoff).

---

## 4. Development environment on Bazzite

Bazzite is an immutable Fedora Atomic image, so the usual `dnf install` reflex is wrong here.
Its own documentation ranks the options:

| Method | Use for | Notes |
|---|---|---|
| **Homebrew** | CLI/TUI tools — compilers, linters, git tooling | Preinstalled. The recommended path for anything you want on the host CLI. |
| **Flatpak** | GUI apps | Flathub configured out of the box. |
| **Distrobox** | anything needing a real distro package manager (`dnf`, `apt`, …) | `DistroShelf` is preinstalled for managing containers graphically; editors can attach to a running container. |
| **rpm-ostree layering** | last resort only | Bazzite's docs explicitly say **not recommended**. Needs a reboot. Anything requiring root privileges ends up here or in a rootful Distrobox container. |

**This project is designed to never need the bottom row.** That constraint drove the stack
choice in §3 — the entire toolchain lives in `$HOME`, and the finished artifact is a single
binary with no system dependencies.

### Setup

```bash
# Toolchain (pick one, matching §3)
brew install go
# …or, if you take the TypeScript route instead:
#   brew install deno
#   brew install oven-sh/bun/bun

# Chromium — needed for the app-window in §5.
# Firefox will not work: it removed site-specific-browser support.
flatpak install flathub org.chromium.Chromium
```

Verify with `go version` and a test launch:

```bash
flatpak run org.chromium.Chromium --app=https://example.com
```

If that opens a window with no tabs and no URL bar, the §5 approach works on your machine.

**Note the Flatpak invocation.** Because Chromium is a Flatpak rather than a system package,
the launcher and the `.desktop` file in §10 must call `flatpak run org.chromium.Chromium
--app=…`, not a bare `chromium`. Easy to get wrong and confusing to debug.

### macOS, if it happens later

`brew install go` works identically. The app-window needs any Chromium-family browser, and
autostart uses a `launchd` plist instead of a systemd user unit. Nothing else changes.

## 5. The window: what a "chromeless app window" is

Every browser window has *chrome* — URL bar, tabs, back button, bookmarks. Chromium can open
a window with all of it stripped off: just the page, in a plain window with a title bar.

```bash
chromium --app=http://localhost:7373
```

That's the entire trick. Own taskbar entry, own Alt-Tab slot, no visible browser.

**The useful framing: this is Electron without the bundling.** Electron ships a private copy
of Chromium inside your app — hence 150MB downloads. The app-window approach renders your
page in the Chromium already installed on the machine. Same engine, same DevTools, no shipped
copy.

Nicer variant: add a **web app manifest** (small JSON — name, icons, `"display": "standalone"`)
and Chromium offers an **Install** button that creates the desktop entry for you.

Three pieces make it feel native rather than like a browser trick:

1. A **`.desktop` file** — real icon in the KDE/GNOME app launcher
2. **`StartupWMClass`** in that file — so the taskbar shows *your* icon, not a generic
   Chromium one. This is the detail people skip, and skipping it is what makes it look fake.
3. A **systemd user service** — starts the Go server in the background at login

Caveat: Firefox removed its equivalent, so this needs a Chromium-family browser. On Bazzite,
`flatpak install org.chromium.Chromium` — a Flatpak, so the base image is untouched.

### The ladder of "native", and why the rungs are cheap to move between

| | What | Cost |
|---|---|---|
| 1 | Browser tab | what Glance does |
| 2 | Chromium `--app=` window / installed PWA | **v1 choice** |
| 3 | Wails (Go) / Tauri (Rust) — webview embedded in the binary | needs system WebKitGTK — the Bazzite friction |
| 4 | Electron — ship your own Chromium | ~150MB, heavy toolchain |
| 5 | GTK4 / Qt — no web tech | full UI rewrite |

**Rungs 2, 3 and 4 all render the same HTML, CSS and JavaScript.** The only difference is who
supplies the browser engine. That's why starting at rung 2 isn't a one-way door — moving to
Wails later swaps the shell, not the app. Rung 5 is the only genuine rewrite, which is exactly
why it isn't proposed.

Rung 3 is deliberately deferred: it needs `libwebkit2gtk` present on the host, which on Bazzite
likely means layering a package on day zero — friction before a single feed works.

---

## 6. Architecture

The single most important structural decision: **the core knows nothing about the frontend.**

```
  config (YAML) ─────► registry: name → provider factory
                              │   "rss" "reddit" "youtube"
                              │   "hackernews" "weather" "calendar"
                              ▼
                       scheduler — one goroutine per widget
                       instance, each on its own TTL
                              │
                              ▼
                       store — in-memory + on-disk snapshot
                       (~/.cache/quarks/) — always readable
                              │
                  ┌───────────┴───────────┐
                  ▼                       ▼
             web UI (v1)             TUI (optional, later)
```

Consequences worth stating explicitly:

- **The UI never waits on the network.** It renders whatever is in the store, instantly, even
  on a cold start (disk snapshot) or with the network down.
- **Failures are per-widget.** A dead Reddit shows last-good items with a subtle `stale · 14m`
  badge. There is no state where the dashboard is blank because one API had a bad day.
- **Adding a feed type = one file** plus one registry line. That's "modular" made concrete.

### Provider interface

```go
// One widget instance's data source. That's the whole contract.
type Provider interface {
    Fetch(ctx context.Context) ([]Item, error)
}

type Factory func(cfg WidgetConfig) (Provider, error)
```

Credentials never appear in this interface. A secret URL and an OAuth token are both just
*"something this provider was configured with"* — which is why adding OAuth later (§9) doesn't
disturb the design.

### The normalized item

Everything on screen is one of these. Getting it right early keeps the UI simple.

```go
type Item struct {
    ID          string     // stable, for dedupe + "seen" tracking
    Title       string
    URL         string     // where the click goes
    Source      string     // "r/selfhosted", "Radio-Canada", …
    Author      string
    PublishedAt time.Time
    Thumbnail   string
    Score       int        // upvotes / HN points; 0 if N/A
    Comments    int
    CommentsURL string     // discussion link, distinct from URL
    Body        string     // populated lazily by the reader view
}
```

Weather and calendar don't fit this shape and shouldn't be forced into it — they get their own
small types and renderers. Two special cases is fine; contorting the model is not.

---

## 7. Config

One YAML file, hot-reloaded on save. Fast iteration matters more than anything else early on.

```yaml
window:
  columns: 3
  theme: dark

widgets:
  - type: rss
    column: 1
    title: Radio-Canada
    ttl: 15m
    feeds: [ https://ici.radio-canada.ca/rss/XXXX ]   # verify IDs — §11
    limit: 12

  - type: hackernews
    column: 1
    ttl: 20m
    limit: 15

  - type: reddit
    column: 2
    ttl: 20m
    subreddits: [selfhosted, bazzite, montreal]
    sort: hot

  - type: youtube
    column: 2
    ttl: 30m
    channels: [ UCxxxxxxxxxxxxxxxxxxxxxx ]   # imported list — §9

  - type: weather
    column: 3
    ttl: 30m
    latitude: 45.50
    longitude: -73.57

  - type: calendar
    column: 3
    ttl: 1h
    ics_urls: [ "${QUARKS_CAL_PERSONAL}" ]     # secret — never inline
```

---

## 8. Credentials and state — why there's no database

Two different things get called "login", and separating them collapses most of the complexity.

**1. Logging into the dashboard itself.** Glance supports this because people expose their
instance to the internet. We bind to `127.0.0.1` — only this machine can reach it. Nothing to
protect, nothing to store. Skipped entirely.

**2. Credentials for the feeds.** You never log into Reddit or YouTube the way a browser does.
There's no username, no password, no session cookie. What you have is a **static string**
generated once on their website and pasted into a file. Static strings need a text file, not a
database.

### What each feed actually requires

| Feed | Credential |
|---|---|
| Radio-Canada | none |
| YouTube (public per-channel RSS) | none |
| Hacker News | none |
| Weather (Open-Meteo) | none |
| Reddit (public subreddits) | none |
| Reddit (your home feed) | a secret RSS URL — §9, confirmed working |
| Calendar | a secret ICS URL |
| YouTube (your subscriptions) | OAuth token — §9, deferred |

Four of the six base feeds need nothing at all, and none of the rest is a password.

### Where things live

| What | Where |
|---|---|
| Config | `~/.config/quarks/config.yaml` — no secrets, safe to paste or commit |
| Secrets | `~/.config/quarks/secrets.yaml`, mode `0600` — or environment variables |
| Cache | `~/.cache/quarks/*.json` — last good response per widget |
| OAuth tokens (later) | one more `0600` file. Still not a database. |

Writing the cache to disk is a deliberate improvement over Glance: restart and the dashboard
is populated instantly instead of blank, and you aren't hammering Reddit on every restart
while developing.

**Security note:** treat the ICS URL and the Reddit feed URL as real credentials. Anyone
holding either can read your calendar or your home feed without logging into anything. Secrets
file only, never in the main config, never in a repo.

### When a database would actually earn its place

Not in v1. The honest triggers: read/unread state across thousands of items, search over feed
history, or "show me everything I missed since Tuesday." Until one of those is wanted, a
database is a moving part that buys nothing.

---

## 9. Personalization strategy

**The goal:** feeds curated where you already curate them, not hand-maintained in YAML. Keeping
a list of 40 YouTube channels in a config file is exactly the chore that makes a dashboard rot
— you subscribe to something new, never update the config, and six months on you're reading a
stale slice of your own interests.

**The decision: do it, but not first.** Imported lists in v1; OAuth as a dedicated later
milestone (M6).

### Reddit — free. Confirmed working.

Reddit still offers **private RSS feeds**: at `reddit.com/prefs/feeds/` you get a secret URL
of the form `reddit.com/.rss?feed=<token>&user=<you>` returning *your personalized home feed*.
Same pattern as the calendar ICS — a secret URL, not an OAuth flow.

**Verified live (Sept 2026).** So personalized Reddit costs **zero new architecture**: the URL
goes in the secrets file and the generic RSS provider handles it. No OAuth, no client ID, no
token refresh, no rate-limit anxiety — and it sidesteps the fragility of the public
`.json` endpoint entirely for your main feed.

That page exposes several feeds (front page, saved, inbox). The one to grab is the **front
page / home feed**. Worth copying the saved-posts URL too — a "read later" widget is nearly
free once the RSS provider exists.

One trade-off to expect: RSS gives titles, links, authors and timestamps, but **not scores or
comment counts**. If those matter on the home feed, the public `.json` endpoint stays
available as a supplement for specific subreddits.

### YouTube — genuinely needs OAuth

No secret-URL equivalent exists. Two paths:

**A. Import once, RSS forever (v1).** Get the subscription list one time — Google Takeout
exports it as CSV, or a single OAuth call — store the channel IDs in config, then poll each
channel's free public RSS from then on. No quota, no tokens, no refresh logic, no expiry. Cost:
re-import manually every few months.

**B. Full OAuth (M6).** `subscriptions.list` with `mine=true` on a schedule. Two gotchas worth
knowing *before* starting rather than discovering at 1am:

1. **The 7-day trap.** Google expires refresh tokens after exactly 7 days while the OAuth
   consent screen sits in **"Testing"** status — you'd re-authenticate weekly and assume your
   code was broken. Fix: set publishing status to **"In production"**. You get a one-time
   "unverified app" warning screen, clickable through for personal use, after which refresh
   tokens last indefinitely.
2. **Quota discipline.** 10,000 units/day. `subscriptions.list` costs 1 unit per 50
   subscriptions. `search.list` costs **100 units per call** and will torch the budget.

**The design that falls out: OAuth for the list, RSS for the content.** Use OAuth once a day
purely to refresh the channel-ID list (a handful of quota units), then keep fetching videos via
free per-channel RSS. Auto-updating subscriptions at near-zero quota.

### Why OAuth is deferred

It is the single largest complexity jump in the project: token storage with refresh handling, a
local callback listener, a `quarks auth youtube` command to drive the consent flow, and an auth
concept threaded through configuration. Not enormous — but the difference between one weekend
and three, and entirely front-loaded *before* you know whether you'll open this thing every
morning.

Sequencing it after packaging means the dashboard has to earn it. And by then YouTube is its
only consumer, which keeps the subsystem small and focused.

---

## 10. Repo layout

```
quarks/
  cmd/quarks/main.go          # flags, wiring, start server
  internal/
    config/                  # parse + validate + watch YAML, secrets loading
    core/                    # Item, Provider, registry, scheduler, store
    providers/
      rss/  reddit/  youtube/  hackernews/  weather/  calendar/
    reader/                  # article extraction for inline reading
    web/                     # handlers, html/template, embedded static assets
    auth/                    # M6 only — OAuth flow + token persistence
  testdata/                  # recorded HTTP fixtures per provider
  packaging/
    quarks.service            # systemd --user unit
    quarks.desktop            # app launcher + icon + StartupWMClass
  Makefile
  README.md
```

**Testing approach:** providers are tested against *recorded HTTP fixtures*, never live
network. Feed formats change without warning, and a test suite needing the internet is a test
suite you stop running. `go test ./...` must pass on a plane.

---

## 11. Per-feed notes

| Feed | Approach | Auth | Notes |
|---|---|---|---|
| **Radio-Canada** | RSS | none | RC publishes RSS per section. **Confirm the current feed URLs/IDs** before M1 — the one in §7 is a placeholder. |
| **YouTube** | RSS per channel: `youtube.com/feeds/videos.xml?channel_id=UC…` | none | No key, no quota. Gives title, link, published, author, `media:thumbnail`. Channel list imported (§9). |
| **Hacker News** | Algolia: `hn.algolia.com/api/v1/search?tags=front_page` | none | One request returns points, comment counts and URLs. Preferred over the official Firebase API, which needs N+1 requests. |
| **Reddit** | **Private home-feed RSS (§9)** as the primary path; `reddit.com/r/<sub>/hot.json` for specific subreddits | secret URL / none | The private feed is confirmed working and avoids all rate-limit risk. Public JSON works from a home IP **with a real custom User-Agent** (Reddit blocks absent/default UAs) but is the fragile path — use it only where scores and comment counts are wanted. |
| **Weather** | Open-Meteo `api.open-meteo.com/v1/forecast` | **none** | Free, no signup, no key, generous limits. Best in class here. |
| **Calendar** | Subscribe to **ICS URLs** | secret URL | Deliberately not CalDAV in v1 — a large protocol for a read-only agenda. Google/Proton/Nextcloud all expose a private ICS link. |
| **Reader view** | `go-shiori/go-readability` | — | Fetch article HTML on demand, extract main text, render inline. Lazily **on click**, never during background polling. |

Core libraries: `mmcdole/gofeed` (RSS/Atom/JSON Feed), `go-shiori/go-readability`, an ICS
parser, `charmbracelet/bubbletea` only if the TUI happens. Everything else is stdlib.

**The simplification that falls out of this table:** most of these feeds are just RSS. A solid
generic RSS provider covers Radio-Canada *and* YouTube *and* your Reddit home feed *and* a
fallback for subreddits. Build that one really well first; the rest are increments.

---

## 12. Milestones

Each ends in something you can actually look at.

- **M0 — Skeleton.** Config loading, registry, scheduler, store, HTTP server, one hardcoded
  RSS widget. *Done when:* a `quarks` binary shows real Radio-Canada headlines in a window on
  the Bazzite box, and clicking one opens Firefox.
- **M1 — It looks good.** Multi-column layout, cards, thumbnails, dark theme, per-widget
  auto-refresh, stale badges, YAML hot-reload. *Done when:* you'd willingly make it your
  homepage.
- **M2 — The feeds.** YouTube (imported channel list), Hacker News, Reddit, weather.
  Reddit uses the private home feed (confirmed, §9), which also introduces the secrets file.
  *Done when:* the config in §7 works end to end.
- **M3 — Reader view.** Inline expansion with extracted article text. Keyboard nav
  (`j/k`, `Enter`, `o`).
- **M4 — Calendar.** ICS subscription, agenda widget.
- **M5 — Packaging.** systemd `--user` unit, `.desktop` launcher with `StartupWMClass`,
  Makefile, README. *Done when:* it survives a reboot and lives in your app launcher.
- **M6 — OAuth subsystem.** YouTube subscriptions sync automatically. Token storage, refresh,
  `quarks auth youtube`. Only build this once M0–M5 have proven the dashboard is part of your
  routine.
- **M7 — Optional shells.** A real native window (Wails, rung 3) or the Bubble Tea TUI, both
  against the same unchanged core. Decide here based on what actually annoyed you in M1–M5.

**M0–M2 is the real project.** M3 onward is gravy, and each is independently droppable.

---

## 13. Non-goals

Stated so they don't creep in:

- Mobile, responsive layout, touch
- Multi-user, accounts, hosting for anyone but you
- Writing to any service (posting, voting, commenting, RSVPing)
- In-app video playback
- Sync between machines
- Dashboard login — unnecessary when bound to localhost (§8)
- A plugin system with dynamic loading. "Modular" here means *a clean interface and one file
  per feed type*, recompiled. Real plugins are a lot of machinery for a solo app.

---

## 14. Open questions and action items

1. **Language.** Go recommended (§3), not locked. Bun/Deno is a legitimate alternative if
   shipping speed matters more than learning.
2. **Radio-Canada RSS URLs** — verify against the live site. *(Blocks M1.)*
3. **Concrete feed list** — which subreddits (beyond the home feed), which YouTube channels,
   which RC sections. *(Needed for M2.)*
4. **YouTube subscription import method** — Google Takeout CSV, or a one-off OAuth call?
   *(Needed for M2.)*
5. **Which calendar** you're on, so the ICS export path is known. *(Needed for M4.)*
6. **KDE or GNOME** on the Bazzite box — affects only `.desktop` and window-rule polish.
   *(Needed for M5.)*

**Resolved:**

- **Name** — Quark's. Binary and paths use `quarks`.
- **Reddit personalization** — private RSS feeds confirmed live (Sept 2026). Reddit never
  needs OAuth. See §9.
