// A thin, dependency-free client for the pieces of the MusicBrainz web service
// this app needs: mapping Spotify artist IDs to MusicBrainz artist IDs, and
// finding release groups with a future first release date for many artists at
// once. Unlike Spotify's catalog API, MusicBrainz lists announced-but-
// unreleased records, and it needs no auth.
//
// MusicBrainz allows one request per second per client and requires a
// descriptive User-Agent; Client enforces both itself so callers can't burst.

import { timeLeft } from "./ctx.ts";
import { sleep } from "./core/scheduler.ts";
import { readLimited, trim } from "./httpx.ts";

// maxURLLookup is how many resources one artistsBySpotify request can carry.
// Verified live (2026-09-26): at 101 the service silently returns no matches
// at all rather than an error.
export const maxURLLookup = 100;

// maxArtistsPerQuery bounds how many artist IDs upcomingReleaseGroups ORs
// into one search query, keeping the request URL a comfortable size. 60 was
// verified live; 50 leaves headroom.
export const maxArtistsPerQuery = 50;

const userAgent = "quarks/0.1 ( https://github.com/maxbasque/quarks )";

// ReleaseGroup is one upcoming record. firstReleaseDate's precision varies:
// "2026-10-13", "2026-10" or just "2026".
export interface ReleaseGroup {
  id: string;
  title: string;
  primaryType: string; // "Album" | "EP" | "Single" | "Broadcast" | "Other" | ""
  secondaryTypes: string[]; // "Live", "Compilation", "Remix", "Soundtrack", …
  firstReleaseDate: string;
  artistCredit: string; // display credit, e.g. "Artist A & Artist B"
  artistIds: string[]; // MusicBrainz IDs of every credited artist
}

// releaseGroupURL is the release group's MusicBrainz page.
export function releaseGroupURL(rg: ReleaseGroup): string {
  return "https://musicbrainz.org/release-group/" + rg.id;
}

// RateLimitedError is a 503 from MusicBrainz that persisted through get's
// retries — its signal for "slow down".
export class RateLimitedError extends Error {
  constructor() {
    super("musicbrainz: rate limited");
  }
}

class NotFoundError extends Error {
  constructor() {
    super("musicbrainz: not found");
  }
}

// Client talks to the MusicBrainz web service. base is overridable so tests
// can point it at a local fixture server; spacing is the minimum gap between
// two requests (the service's documented limit is 1/s; a little slower draws
// noticeably fewer 503s in practice). retryWaits are the pauses before
// retrying a 503: MusicBrainz 503s aren't only about this client's pace — its
// anonymous pool is shared and it sheds load under pressure (seen live
// 2026-09-26 at a steady 1 req/1.1s), so a short back-off usually gets through.
export class Client {
  base = "https://musicbrainz.org/ws/2";
  spacing = 1500;
  retryWaits = [2000, 5000];
  timeout = 15_000;

  #next = 0; // earliest time the next request may start

  // artistsBySpotify maps Spotify artist IDs to MusicBrainz artist IDs via the
  // Spotify links MusicBrainz stores on artist pages. IDs with no link are
  // simply absent from the result. At most maxURLLookup IDs per call.
  async artistsBySpotify(spotifyIds: string[], signal: AbortSignal): Promise<Map<string, string>> {
    if (spotifyIds.length === 0) return new Map();
    if (spotifyIds.length > maxURLLookup) {
      throw new Error(
        `musicbrainz: ${spotifyIds.length} ids exceeds the ${maxURLLookup}-resource lookup limit`,
      );
    }
    const q = new URLSearchParams({ fmt: "json", inc: "artist-rels" });
    const byURL = new Map<string, string>();
    for (const id of spotifyIds) {
      const u = "https://open.spotify.com/artist/" + id;
      q.append("resource", u);
      byURL.set(u, id);
    }
    q.sort();

    let entities: any[];
    if (spotifyIds.length === 1) {
      // A single resource comes back as the bare entity, not a list — and as
      // a 404 when MusicBrainz has no such link.
      try {
        entities = [await this.#get("/url?" + q, signal)];
      } catch (err) {
        if (err instanceof NotFoundError) return new Map();
        throw err;
      }
    } else {
      try {
        entities = (await this.#get("/url?" + q, signal))?.urls ?? [];
      } catch (err) {
        if (!(err instanceof NotFoundError)) throw err;
        entities = [];
      }
    }

    const out = new Map<string, string>();
    for (const e of entities) {
      const sid = byURL.get(e?.resource);
      if (!sid) continue;
      const rel = (e.relations ?? []).find((r: any) => r["target-type"] === "artist" && r.artist?.id);
      if (rel) out.set(sid, rel.artist.id);
    }
    return out;
  }

  // artistByName looks an artist up by exact name, for followed artists whose
  // MusicBrainz page carries no Spotify link. It only returns a match when
  // it's unambiguous — exactly one artist whose name equals name
  // (case-insensitively) — because a wrong guess would put a stranger's
  // releases on the dashboard.
  async artistByName(name: string, signal: AbortSignal): Promise<string | null> {
    const q = new URLSearchParams({ fmt: "json", limit: "10", query: `artist:"${escapeQuery(name)}"` });
    const res = await this.#get("/artist?" + q, signal);
    let match: string | null = null;
    for (const a of res?.artists ?? []) {
      if (String(a.name ?? "").trim().toLowerCase() !== name.trim().toLowerCase()) continue;
      if (match !== null) return null; // two artists share the name: don't guess
      match = a.id;
    }
    return match;
  }

  // upcomingReleaseGroups returns release groups credited to any of artistIds
  // whose first release date is on or after from. At most maxArtistsPerQuery
  // artists per call; pages through results itself.
  //
  // Year- or month-only dates match the range generously (a "2026" record
  // matches any 2026 from), so callers should re-check dates themselves.
  async upcomingReleaseGroups(artistIds: string[], from: Date, signal: AbortSignal): Promise<ReleaseGroup[]> {
    if (artistIds.length === 0) return [];
    if (artistIds.length > maxArtistsPerQuery) {
      throw new Error(
        `musicbrainz: ${artistIds.length} artists exceeds the ${maxArtistsPerQuery}-per-query limit`,
      );
    }
    const terms = artistIds.map((id) => "arid:" + id);
    const query = `(${terms.join(" OR ")}) AND firstreleasedate:[${ymd(from)} TO *]`;

    const pageSize = 100;
    const maxPages = 5; // a few hundred upcoming records for 50 artists is already implausible
    const out: ReleaseGroup[] = [];
    for (let page = 0; page < maxPages; page++) {
      const q = new URLSearchParams({
        fmt: "json",
        limit: String(pageSize),
        offset: String(page * pageSize),
        query,
      });
      const res = await this.#get("/release-group?" + q, signal);
      const rgs: any[] = res?.["release-groups"] ?? [];
      for (const r of rgs) {
        const credits: any[] = r["artist-credit"] ?? [];
        out.push({
          id: r.id ?? "",
          title: r.title ?? "",
          primaryType: r["primary-type"] ?? "",
          secondaryTypes: r["secondary-types"] ?? [],
          firstReleaseDate: r["first-release-date"] ?? "",
          artistCredit: credits.map((ac) => (ac.name ?? "") + (ac.joinphrase ?? "")).join(""),
          artistIds: credits.map((ac) => ac.artist?.id ?? ""),
        });
      }
      if (rgs.length < pageSize || (page + 1) * pageSize >= (res?.count ?? 0)) break;
    }
    return out;
  }

  // get does one GET against base+path, retrying 503s after retryWaits while
  // the signal's deadline allows.
  async #get(path: string, signal: AbortSignal): Promise<any> {
    let lastErr: unknown;
    for (let attempt = 0; attempt <= this.retryWaits.length; attempt++) {
      if (attempt > 0) {
        const d = this.retryWaits[attempt - 1];
        if (timeLeft(signal) < d + this.spacing) throw lastErr;
        if (!(await sleep(d, signal))) throw lastErr;
      }
      try {
        return await this.#getOnce(path, signal);
      } catch (err) {
        if (!(err instanceof RateLimitedError)) throw err;
        lastErr = err;
      }
    }
    throw lastErr;
  }

  // getOnce waits for its rate-limit slot, then does one GET.
  async #getOnce(path: string, signal: AbortSignal): Promise<any> {
    await this.#wait(signal);
    const resp = await fetch(this.base + path, {
      headers: { "User-Agent": userAgent, Accept: "application/json" },
      signal: AbortSignal.any([signal, AbortSignal.timeout(this.timeout)]),
    });
    const body = new TextDecoder().decode(await readLimited(resp, 4 << 20));

    switch (resp.status) {
      case 200:
        break;
      case 404:
        throw new NotFoundError();
      case 503:
      case 429:
        throw new RateLimitedError();
      default:
        throw new Error(`musicbrainz: http ${resp.status} on ${path}: ${trim(body)}`);
    }
    try {
      return JSON.parse(body);
    } catch (err) {
      throw new Error(`musicbrainz: parse response from ${path}: ${(err as Error).message}`);
    }
  }

  // wait resolves when this client may send its next request, reserving the
  // slot after it so concurrent callers queue up spacing apart.
  async #wait(signal: AbortSignal) {
    const now = Date.now();
    const start = Math.max(this.#next, now);
    this.#next = start + this.spacing;
    const d = start - now;
    if (d > 0 && !(await sleep(d, signal))) throw signal.reason ?? new Error("aborted");
  }
}

function ymd(d: Date): string {
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

// escapeQuery backslash-escapes Lucene's special characters so an artist name
// like "AC/DC" or "Sunn O)))" is searched literally.
export function escapeQuery(s: string): string {
  let out = "";
  for (const ch of s) {
    if (`+-&|!(){}[]^"~*?:\\/`.includes(ch)) out += "\\";
    out += ch;
  }
  return out;
}
