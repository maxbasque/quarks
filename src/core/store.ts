import { join } from "@std/path";
import type { Item, Payload, Standings, Weather, WeatherDay } from "./types.ts";

// WidgetState is everything the UI needs to render one widget: the last-good
// items plus freshness metadata. The UI never blocks on the network — it renders
// whatever is here.
export interface WidgetState {
  key: string;
  title: string;
  order: number; // position in the config, for stable UI ordering
  column: number;
  type: string;
  items: Item[]; // feed widgets
  weather: Weather | null; // weather widgets
  standings: Standings | null; // standings widgets
  lastOk: Date | null; // null until a fetch succeeds
  lastErr: string; // last fetch error, "" if last fetch was ok
  lastTry: Date | null;
}

// isStale reports whether the newest good data is older than ttl (ms).
export function isStale(s: WidgetState, ttl: number): boolean {
  return s.lastOk !== null && Date.now() - s.lastOk.getTime() > ttl;
}

// Store holds widget state in memory and mirrors each widget to a JSON file in
// cacheDir so a cold start is populated instantly instead of blank.
export class Store {
  #cacheDir: string;
  #states = new Map<string, WidgetState>();
  #gen = 0; // bumped on every change, so the web layer can cache renders

  constructor(cacheDir: string) {
    Deno.mkdirSync(cacheDir, { recursive: true });
    this.#cacheDir = cacheDir;
  }

  // gen is a version number that changes whenever any widget state changes.
  get gen(): number {
    return this.#gen;
  }

  // fresh reports whether key has good data younger than ttl (ms).
  fresh(key: string, ttl: number): boolean {
    const st = this.#states.get(key);
    return st !== undefined && st.lastOk !== null && Date.now() - st.lastOk.getTime() < ttl;
  }

  // register seeds a widget's state. On first sight it loads any disk snapshot;
  // on a config reload it keeps the in-memory items and just refreshes the
  // identity fields, so a reload never blanks the dashboard.
  register(key: string, title: string, order: number, column: number, type: string) {
    this.#gen++;
    const existing = this.#states.get(key);
    if (existing) {
      Object.assign(existing, { title, order, column, type });
      return;
    }
    let st: WidgetState = {
      key,
      title,
      order,
      column,
      type,
      items: [],
      weather: null,
      standings: null,
      lastOk: null,
      lastErr: "",
      lastTry: null,
    };
    try {
      st = { ...decodeState(JSON.parse(Deno.readTextFileSync(this.#path(key)))), key, title, order, column, type };
    } catch {
      // no snapshot yet, or an unreadable one: start empty
    }
    this.#states.set(key, st);
  }

  // retain drops in-memory state (and the disk snapshot) for any widget whose
  // key is not in keep — i.e. widgets removed from the config on reload.
  retain(keep: Set<string>) {
    for (const k of [...this.#states.keys()]) {
      if (!keep.has(k)) {
        this.#states.delete(k);
        try {
          Deno.removeSync(this.#path(k));
        } catch { /* already gone */ }
        this.#gen++;
      }
    }
  }

  // setPayload records a successful fetch and writes the snapshot. It only
  // bumps the render version when the visible content actually changed, so an
  // unchanged (e.g. HTTP 304) refetch costs nothing downstream.
  setPayload(key: string, p: Payload) {
    const st = this.#states.get(key);
    if (!st) return;
    const changed = st.lastErr !== "" || !sameContent(st, p);
    const now = new Date();
    st.items = p.items ?? [];
    st.weather = p.weather ?? null;
    st.standings = p.standings ?? null;
    st.lastOk = now;
    st.lastTry = now;
    st.lastErr = "";
    if (changed) this.#gen++;
    this.#persist(st);
  }

  // setError records a failed fetch. Existing items are kept.
  setError(key: string, err: unknown) {
    const st = this.#states.get(key);
    if (!st) return;
    st.lastTry = new Date();
    st.lastErr = errorMessage(err);
    this.#gen++;
    this.#persist(st);
  }

  // snapshot returns a copy of all widget states.
  snapshot(): WidgetState[] {
    return [...this.#states.values()].map((s) => ({ ...s }));
  }

  #persist(st: WidgetState) {
    const path = this.#path(st.key);
    const tmp = path + ".tmp";
    try {
      Deno.writeTextFileSync(tmp, JSON.stringify(encodeState(st)));
      Deno.renameSync(tmp, path);
    } catch {
      // the cache is best-effort; the next fetch writes it again
    }
  }

  #path(key: string): string {
    return join(this.#cacheDir, key + ".json");
  }
}

function sameContent(st: WidgetState, p: Payload): boolean {
  if (p.weather || p.standings) return false; // small, changes most fetches — just re-render
  const items = p.items ?? [];
  if (st.weather || st.standings || st.items.length !== items.length) return false;
  return items.every((it, i) => st.items[i].id === it.id);
}

export function errorMessage(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}

// ---- disk format -----------------------------------------------------------
//
// The snapshot files keep the Go version's JSON shape (Go field names for
// items and standings, snake_case for the rest, "0001-01-01T00:00:00Z" for an
// unset time), so a cache written by either version warms the other.

const zeroTime = "0001-01-01T00:00:00Z";

function encTime(d: Date | null): string {
  return d ? d.toISOString() : zeroTime;
}

function decTime(v: unknown): Date | null {
  if (typeof v !== "string" || v.startsWith("0001-01-01")) return null;
  const d = new Date(v);
  return isNaN(d.getTime()) ? null : d;
}

function encodeState(s: WidgetState) {
  return {
    key: s.key,
    title: s.title,
    order: s.order,
    column: s.column,
    type: s.type,
    items: s.items.map((it) => ({
      ID: it.id,
      Title: it.title,
      URL: it.url,
      Source: it.source,
      Author: it.author,
      PublishedAt: encTime(it.publishedAt),
      Thumbnail: it.thumbnail,
      Score: it.score,
      Comments: it.comments,
      CommentsURL: it.commentsUrl,
      Summary: it.summary,
      Body: it.body,
      Hero: it.hero,
    })),
    weather: s.weather && {
      location: s.weather.location,
      current: s.weather.current,
      feels_like: s.weather.feelsLike,
      code: s.weather.code,
      condition: s.weather.condition,
      today: encDay(s.weather.today),
      forecast: s.weather.forecast.map(encDay),
      observed_at: encTime(s.weather.observedAt),
    },
    standings: s.standings && {
      Groups: s.standings.groups.map((g) => ({
        Name: g.name,
        Columns: g.columns,
        Rows: g.rows.map((r) => ({
          Rank: r.rank,
          Team: r.team,
          Abbrev: r.abbrev,
          Logo: r.logo,
          Values: r.values,
          Highlight: r.highlight,
        })),
      })),
    },
    last_ok: encTime(s.lastOk),
    last_err: s.lastErr,
    last_try: encTime(s.lastTry),
  };
}

function encDay(d: WeatherDay) {
  return { date: encTime(d.date), high: d.high, low: d.low, code: d.code, condition: d.condition };
}

// get reads a key case-insensitively, the way Go's encoding/json matches
// field names.
function get(o: any, name: string): any {
  if (o == null || typeof o !== "object") return undefined;
  if (name in o) return o[name];
  const lower = name.toLowerCase();
  for (const k of Object.keys(o)) if (k.toLowerCase() === lower) return o[k];
  return undefined;
}

const s = (v: unknown) => (typeof v === "string" ? v : "");
const n = (v: unknown) => (typeof v === "number" ? v : 0);
const arr = (v: unknown): any[] => (Array.isArray(v) ? v : []);

function decodeState(o: any): Omit<WidgetState, "key" | "title" | "order" | "column" | "type"> {
  const w = get(o, "weather");
  const st = get(o, "standings");
  return {
    items: arr(get(o, "items")).map((it) => ({
      id: s(get(it, "ID")),
      title: s(get(it, "Title")),
      url: s(get(it, "URL")),
      source: s(get(it, "Source")),
      author: s(get(it, "Author")),
      publishedAt: decTime(get(it, "PublishedAt")),
      thumbnail: s(get(it, "Thumbnail")),
      score: n(get(it, "Score")),
      comments: n(get(it, "Comments")),
      commentsUrl: s(get(it, "CommentsURL")),
      summary: s(get(it, "Summary")),
      body: s(get(it, "Body")),
      hero: get(it, "Hero") === true,
    })),
    weather: w
      ? {
        location: s(get(w, "location")),
        current: n(get(w, "current")),
        feelsLike: n(get(w, "feels_like")),
        code: n(get(w, "code")),
        condition: s(get(w, "condition")),
        today: decDay(get(w, "today")),
        forecast: arr(get(w, "forecast")).map(decDay),
        observedAt: decTime(get(w, "observed_at")) ?? new Date(0),
      }
      : null,
    standings: st
      ? {
        groups: arr(get(st, "Groups")).map((g) => ({
          name: s(get(g, "Name")),
          columns: arr(get(g, "Columns")).map(s),
          rows: arr(get(g, "Rows")).map((r) => ({
            rank: n(get(r, "Rank")),
            team: s(get(r, "Team")),
            abbrev: s(get(r, "Abbrev")),
            logo: s(get(r, "Logo")),
            values: arr(get(r, "Values")).map(s),
            highlight: get(r, "Highlight") === true,
          })),
        })),
      }
      : null,
    lastOk: decTime(get(o, "last_ok")),
    lastErr: s(get(o, "last_err")),
    lastTry: decTime(get(o, "last_try")),
  };
}

function decDay(d: any): WeatherDay {
  return {
    date: decTime(get(d, "date")),
    high: n(get(d, "high")),
    low: n(get(d, "low")),
    code: n(get(d, "code")),
    condition: s(get(d, "condition")),
  };
}
