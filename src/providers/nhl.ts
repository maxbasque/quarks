// The public, keyless api-web.nhle.com. Two modes:
//
//	mode: schedule (default) — one team's upcoming games, soonest first
//	mode: scores             — recent final scores from around the league

import { type Provider, Settings, type WidgetConfig } from "../core/provider.ts";
import { feed, type Item, item, type Payload } from "../core/types.ts";
import * as fr from "../fr.ts";
import { browserUserAgent, getJSON } from "../httpx.ts";

const gameCenterURL = "https://www.nhl.com/gamecenter/";

// newNHL is the core.Factory for "nhl".
export function newNHL(cfg: WidgetConfig): Provider {
  const s = Settings.of(cfg);
  const mode = s.str("mode").trim().toLowerCase() || "schedule";
  const title = JSON.stringify(cfg.title);
  if (mode !== "schedule" && mode !== "scores") {
    throw new Error(`nhl widget ${title}: mode must be schedule or scores`);
  }
  const team = s.str("team").trim().toUpperCase(); // 3-letter abbrev, e.g. MTL
  if (mode === "schedule" && team.length !== 3) {
    throw new Error(`nhl widget ${title}: set team to a 3-letter code, e.g. MTL`);
  }
  return new NHL(mode, team, cfg.limit > 0 ? cfg.limit : 12);
}

interface Team {
  abbrev?: string;
  commonName?: { default?: string };
  name?: { default?: string };
  score?: number | null;
  logo?: string;
}

interface Game {
  id: number;
  startTimeUTC?: string;
  gameState?: string;
  gameType?: number;
  venue?: { default?: string };
  gameOutcome?: { lastPeriodType?: string };
  awayTeam: Team;
  homeTeam: Team;
}

function teamName(t: Team): string {
  for (const m of [t.commonName, t.name]) {
    if (typeof m?.default === "string" && m.default !== "") return m.default;
  }
  return t.abbrev ?? "";
}

const hasScore = (t: Team) => typeof t.score === "number";

export class NHL implements Provider {
  scheduleURL = "https://api-web.nhle.com/v1/club-schedule-season/%s/now";
  scoreURL = "https://api-web.nhle.com/v1/score/%s";

  constructor(private mode: string, private team: string, private limit: number) {}

  fetch(signal: AbortSignal): Promise<Payload> {
    return this.mode === "scores" ? this.#fetchScores(signal) : this.#fetchSchedule(signal);
  }

  #get(url: string, signal: AbortSignal) {
    return getJSON(url, signal, { ua: browserUserAgent, errPrefix: "nhl api" });
  }

  // ---- schedule --------------------------------------------------------

  async #fetchSchedule(signal: AbortSignal): Promise<Payload> {
    const data = await this.#get(this.scheduleURL.replace("%s", this.team), signal);
    const tz = fr.validTimeZone(data.clubTimezone) ? data.clubTimezone : "UTC";

    const cutoff = Date.now() - 30 * 3_600_000;
    const items: Item[] = [];
    for (const g of (data.games ?? []) as Game[]) {
      const start = parseRFC3339(g.startTimeUTC);
      if (!start || start.getTime() < cutoff) continue;
      items.push(this.#scheduleItem(g, start, tz));
      if (items.length >= this.limit) break;
    }
    return feed(items);
  }

  #scheduleItem(g: Game, start: Date, tz: string): Item {
    let us = g.homeTeam, them = g.awayTeam;
    let title = "c. " + teamName(them);
    if (g.awayTeam.abbrev === this.team) {
      us = g.awayTeam;
      them = g.homeTeam;
      title = "@ " + teamName(them);
    }

    let summary = fr.dateTime(start, tz);
    const extra = gameExtra(g, us, them);
    if (extra) summary += "  ·  " + extra;

    return item({
      id: String(g.id),
      title,
      url: gameCenterURL + g.id,
      source: gameTypeLabel(g.gameType ?? 0),
      publishedAt: start,
      thumbnail: them.logo ?? "",
      summary,
    });
  }

  // ---- scores (league-wide) ---------------------------------------------

  async #fetchScores(signal: AbortSignal): Promise<Payload> {
    let date = "now";
    const seen = new Set<number>();
    let items: Item[] = [];

    for (let hop = 0; hop < 5 && items.length < this.limit; hop++) {
      let d;
      try {
        d = await this.#get(this.scoreURL.replace("%s", date), signal);
      } catch (err) {
        if (hop === 0) throw err;
        break;
      }
      for (const g of (d.games ?? []) as Game[]) {
        if (seen.has(g.id) || !hasScore(g.awayTeam) || !hasScore(g.homeTeam)) continue;
        if (["OFF", "FINAL", "LIVE", "CRIT"].includes(g.gameState ?? "")) {
          seen.add(g.id);
          items.push(scoreItem(g));
        }
      }
      if (!d.prevDate || d.prevDate === date) break;
      date = d.prevDate;
    }

    const t = (it: Item) => it.publishedAt?.getTime() ?? -Infinity;
    items.sort((a, b) => t(b) - t(a));
    if (items.length > this.limit) items = items.slice(0, this.limit);
    return feed(items);
  }
}

// gameExtra is the score once a game is under way, otherwise the venue.
function gameExtra(g: Game, us: Team, them: Team): string {
  const live = g.gameState === "LIVE" || g.gameState === "CRIT";
  const done = g.gameState === "OFF" || g.gameState === "FINAL";
  if ((live || done) && hasScore(us) && hasScore(them)) {
    const a = us.score!, b = them.score!;
    const r = `${a}–${b}`;
    if (live) return "En cours " + r;
    if (a > b) return "Victoire " + r;
    if (a < b) return "Défaite " + r;
    return "Final " + r;
  }
  return g.venue?.default ?? "";
}

function gameTypeLabel(t: number): string {
  switch (t) {
    case 1:
      return "Préparatoire";
    case 3:
      return "Séries";
    default:
      return "";
  }
}

function scoreItem(g: Game): Item {
  const a = g.awayTeam, h = g.homeTeam;
  const as = a.score!, hs = h.score!;
  const winner = as > hs ? a : h;

  let state = "Final";
  if (g.gameState === "LIVE" || g.gameState === "CRIT") state = "En cours";
  else if (g.gameOutcome?.lastPeriodType === "OT") state = "Final (prol.)";
  else if (g.gameOutcome?.lastPeriodType === "SO") state = "Final (t.b.)";

  const start = parseRFC3339(g.startTimeUTC);
  let summary = state;
  if (start) summary += "  ·  " + fr.date(start);

  return item({
    id: String(g.id),
    title: `${a.abbrev ?? ""} ${as} – ${hs} ${h.abbrev ?? ""}`,
    url: gameCenterURL + g.id,
    publishedAt: start,
    thumbnail: winner.logo ?? "",
    summary,
  });
}

// parseRFC3339 reads a full timestamp with a zone ("2026-10-10T23:00:00Z").
export function parseRFC3339(s: string | undefined): Date | null {
  if (!s || !/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?(Z|[+-]\d\d:\d\d)$/.test(s)) return null;
  const d = new Date(s);
  return isNaN(d.getTime()) ? null : d;
}
