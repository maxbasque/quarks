// Spotify supplies only the followed-artist list (a handful of paginated calls
// a day). Release data comes from MusicBrainz, which — unlike Spotify's
// catalog — lists records that are announced but not out yet, and can answer
// "what's upcoming for these 50 artists" in one query. The previous design
// polled Spotify's per-artist albums endpoint and earned a ~20h rate-limit
// penalty (2026-09-24) while still rarely finding anything before release day.

import { encodeHex } from "@std/encoding/hex";
import { deadlineOf, withDeadline } from "../../ctx.ts";
import * as fr from "../../fr.ts";
import { log as defaultLog, type Logger } from "../../log.ts";
import * as musicbrainz from "../../musicbrainz.ts";
import { Mutex } from "../../mutex.ts";
import * as spotifyapi from "../../spotifyapi.ts";

const minute = 60_000;
const hour = 60 * minute;

// pollInterval is the minimum gap between two actual polls (see maybePoll) —
// not a ticker period. There is no background loop; a poll only happens inside
// fetch, triggered by the widget's own TTL or a manual refresh click. This
// floor is what stops two widgets sharing one Shared (say, an albums-only and
// an EPs-only widget), or repeated refresh clicks, from polling back-to-back.
export const pollInterval = 10 * minute;

// artistListTTL is how long the followed-artist list is trusted before a
// refetch from Spotify.
export const artistListTTL = 24 * hour;

// sweepInterval is how often the upcoming releases of every mapped artist are
// re-queried from MusicBrainz. A full sweep is one request per 50 artists.
export const sweepInterval = 6 * hour;

// nameRetryAfter is how long an artist that couldn't be matched by name stays
// skipped before it's searched again — someone may have added it to
// MusicBrainz, or its Spotify link, in the meantime.
const nameRetryAfter = 7 * 24 * hour;

// pollBudget caps one poll's wall time when the caller's signal has no
// deadline; with one, the poll stops pollMargin short of it. MusicBrainz
// allows one request a second, so this bounds a poll to a couple dozen.
const pollBudget = 25_000;
const pollMargin = 2_000;

// excludedSecondary are MusicBrainz secondary types that aren't new material
// from the artist, so they're never shown even when their primary type is
// Album or EP.
const excludedSecondary = new Set(["Compilation", "Remix", "DJ-mix"]);

export type Precision = "day" | "month" | "year";

// ReleaseEntry is one cached upcoming release.
export interface ReleaseEntry {
  rg: musicbrainz.ReleaseGroup;
  date: Date; // start of the announced period, for sorting
  precision: Precision;
  class: "album" | "eps";
}

// Shared is one Spotify account's release cache — one per distinct refresh
// token, not one per widget instance. Two widgets (say, albums-only and
// EPs-only widgets) reading the same account converge on the same Shared via
// acquire, so whichever one's fetch runs first for a given poll window does the
// work and pollMu/lastPollAt make the other a no-op. The cache survives config
// reloads as long as the refresh token doesn't change.
export class Shared {
  creds: spotifyapi.Credentials = { clientId: "", clientSecret: "", refreshToken: "" };

  tok = "";
  tokExp = 0;
  #tokMu = new Mutex();

  #pollMu = new Mutex(); // serializes maybePoll across concurrent fetch calls
  lastPollAt = 0;
  // blockedUntil is when Spotify's last 429 Retry-After expires. Until then
  // no Spotify request is made.
  blockedUntil = 0;
  // pollErr is the last poll's failure (null if it got useful work done),
  // reported by every widget's fetch until the next poll.
  pollErr: Error | null = null;

  artists: spotifyapi.Artist[] = [];
  artistsCheckedAt = 0;
  mbids = new Map<string, string>(); // spotify artist ID -> MusicBrainz artist ID
  urlTried = new Set<string>(); // looked up by Spotify link since the last artist refresh
  nameTriedAt = new Map<string, number>(); // last failed name search
  releases = new Map<string, ReleaseEntry>();
  lastSweepAt = 0;
  sweepDue = false; // new artists mapped since the last sweep

  constructor(
    readonly client: spotifyapi.Client,
    readonly mb: musicbrainz.Client,
    readonly log: Logger = defaultLog,
  ) {}

  // maybePoll runs one poll if at least pollInterval has passed since the last
  // one, otherwise it's a no-op. Called from fetch, so it shares fetch's
  // deadline. Returns the error fetch should report, which persists across
  // skipped calls so both widgets show the same state.
  maybePoll(signal: AbortSignal): Promise<Error | null> {
    return this.#pollMu.run(async () => {
      const now = Date.now();
      if (this.lastPollAt !== 0 && now - this.lastPollAt < pollInterval) return this.pollErr;
      this.lastPollAt = now;

      let deadline = now + pollBudget;
      const dl = deadlineOf(signal);
      if (dl !== undefined && dl - pollMargin < deadline) deadline = dl - pollMargin;

      try {
        await this.poll(withDeadline(signal, deadline));
        this.pollErr = null;
      } catch (err) {
        this.pollErr = err as Error;
      }
      return this.pollErr;
    });
  }

  // poll does whatever's due, cheapest and most useful first:
  //
  //  1. refresh the followed-artist list from Spotify (daily);
  //  2. map new artists to MusicBrainz by their Spotify link (100 per request);
  //  3. sweep upcoming releases for every mapped artist (every sweepInterval,
  //     or sooner once new artists are mapped);
  //  4. spend what's left of the budget matching unlinked artists by name, one
  //     request each, so a big first-time backlog trickles in over polls.
  //
  // Throws only when the poll leaves the widget with nothing trustworthy to
  // show. Caller holds pollMu.
  async poll(signal: AbortSignal) {
    try {
      await this.ensureArtists(signal);
    } catch (err) {
      this.log.warn("spotify: refresh followed artists failed", { err });
      if (this.artists.length === 0) {
        throw new Error(`spotify: refresh followed artists: ${(err as Error).message}`);
      }
      // Carry on with yesterday's list; releases come from MusicBrainz anyway.
    }

    try {
      await this.resolveByURL(signal);
    } catch (err) {
      this.log.warn("musicbrainz: artist lookup failed", { err });
      if (err instanceof musicbrainz.RateLimitedError) throw err;
    }

    if (this.sweepDue || Date.now() - this.lastSweepAt >= sweepInterval) {
      try {
        await this.sweep(signal);
      } catch (err) {
        this.log.warn("musicbrainz: release sweep failed", { err });
        if (this.lastSweepAt === 0 || err instanceof musicbrainz.RateLimitedError) {
          throw new Error(`musicbrainz: upcoming releases: ${(err as Error).message}`);
        }
        return; // keep showing the last complete sweep
      }
    }

    try {
      await this.resolveByName(signal);
    } catch (err) {
      if (!signal.aborted) this.log.warn("musicbrainz: artist name search failed", { err });
    }
    this.prune();
  }

  // ensureArtists refetches the full followed-artist list if it hasn't been
  // checked in artistListTTL (or ever). Honors Spotify's Retry-After.
  async ensureArtists(signal: AbortSignal) {
    if (Date.now() - this.artistsCheckedAt <= artistListTTL) return;
    if (Date.now() < this.blockedUntil) {
      throw new Error(`Spotify limite les requêtes jusqu'à ${fr.dateTime(new Date(this.blockedUntil))}`);
    }

    const tok = await this.accessToken(signal);
    const all: spotifyapi.Artist[] = [];
    let after = "";
    for (;;) {
      let page;
      try {
        page = await this.client.followedArtists(tok, after, signal);
      } catch (err) {
        this.noteErr(err);
        throw err;
      }
      all.push(...page.artists);
      if (!page.next) break;
      after = page.next;
    }

    this.artists = all;
    this.artistsCheckedAt = Date.now();
    this.urlTried = new Set(); // links get added over time: retry the unmapped
  }

  // noteErr reacts to a Spotify error that affects every later request: a 429
  // blocks Spotify calls for its Retry-After, a 401 drops the cached access
  // token so the next attempt mints a fresh one.
  noteErr(err: unknown) {
    if (err instanceof spotifyapi.RateLimitedError) {
      this.blockedUntil = Date.now() + err.retryAfter;
      this.log.warn("spotify: rate limited, pausing requests", {
        retry_after: spotifyapi.formatDuration(err.retryAfter),
        until: new Date(this.blockedUntil),
      });
    } else if (err instanceof spotifyapi.UnauthorizedError) {
      this.tok = "";
    }
  }

  // accessToken returns a cached access token, refreshing it a minute before
  // it actually expires.
  accessToken(signal: AbortSignal): Promise<string> {
    return this.#tokMu.run(async () => {
      if (this.tok && Date.now() < this.tokExp) return this.tok;
      const { accessToken, expiresIn } = await this.client.refreshAccessToken(this.creds, signal);
      this.tok = accessToken;
      this.tokExp = Date.now() + expiresIn * 1000 - minute;
      return this.tok;
    });
  }

  // resolveByURL maps every not-yet-mapped artist whose Spotify link hasn't
  // been looked up since the last artist refresh.
  async resolveByURL(signal: AbortSignal) {
    let todo = this.artists.filter((a) => !this.mbids.get(a.id) && !this.urlTried.has(a.id)).map((a) => a.id);
    while (todo.length > 0) {
      const batch = todo.slice(0, musicbrainz.maxURLLookup);
      todo = todo.slice(batch.length);
      const found = await this.mb.artistsBySpotify(batch, signal);
      for (const id of batch) {
        this.urlTried.add(id);
        const mbid = found.get(id);
        if (mbid) {
          this.mbids.set(id, mbid);
          this.sweepDue = true;
        }
      }
    }
  }

  // resolveByName tries an exact-name search for artists the link lookup
  // couldn't map, one request each, until the poll's deadline is near.
  async resolveByName(signal: AbortSignal) {
    const now = Date.now();
    const todo = this.artists.filter((a) =>
      !this.mbids.get(a.id) && this.urlTried.has(a.id) && now - (this.nameTriedAt.get(a.id) ?? 0) >= nameRetryAfter
    );
    for (const a of todo) {
      const dl = deadlineOf(signal);
      if (dl !== undefined && dl - Date.now() < 2 * this.mb.spacing) return; // out of budget; the rest wait
      const mbid = await this.mb.artistByName(a.name, signal);
      if (mbid) {
        this.mbids.set(a.id, mbid);
        this.sweepDue = true;
      } else {
        this.nameTriedAt.set(a.id, Date.now());
      }
    }
  }

  // sweep re-queries upcoming releases for every mapped artist. A complete
  // sweep replaces the cache (so cancelled or re-dated records disappear); a
  // partial one only adds to it.
  async sweep(signal: AbortSignal) {
    let ids = [...new Set(this.artists.map((a) => this.mbids.get(a.id)).filter((m): m is string => !!m))];

    const now = new Date();
    // Start the range a month back: a month-precision record due this month
    // is still upcoming, and upcoming() does the exact filtering.
    const from = new Date(now);
    from.setMonth(from.getMonth() - 1);
    const fresh = new Map<string, ReleaseEntry>();
    try {
      while (ids.length > 0) {
        const batch = ids.slice(0, musicbrainz.maxArtistsPerQuery);
        const rgs = await this.mb.upcomingReleaseGroups(batch, from, signal);
        ids = ids.slice(batch.length);
        for (const rg of rgs) {
          const e = toEntry(rg, now);
          if (e) fresh.set(rg.id, e);
        }
      }
    } catch (err) {
      for (const [id, e] of fresh) this.releases.set(id, e);
      throw err;
    }
    this.releases = fresh;
    this.lastSweepAt = Date.now();
    this.sweepDue = false;
  }

  // prune drops cached releases whose date has passed since the last sweep.
  prune() {
    const now = new Date();
    for (const [id, r] of this.releases) {
      if (!upcoming(r.date, r.precision, now)) this.releases.delete(id);
    }
  }

  // snapshot returns every cached upcoming release of class include ("all"
  // for every class), sorted soonest-first. No network call itself.
  snapshot(include: string): ReleaseEntry[] {
    return [...this.releases.values()]
      .filter((r) => include === "all" || r.class === include)
      .sort((a, b) => {
        if (a.date.getTime() !== b.date.getTime()) return a.date.getTime() - b.date.getTime();
        const x = a.rg.artistCredit.toLowerCase(), y = b.rg.artistCredit.toLowerCase();
        return x < y ? -1 : x > y ? 1 : 0;
      });
  }
}

const shareds = new Map<string, Shared>();

// acquire returns the shared cache for creds, creating it on first use. The
// map key is a hash of the refresh token, never the token itself.
export async function acquire(creds: spotifyapi.Credentials): Promise<Shared> {
  const key = await cacheKey(creds.refreshToken);
  let sh = shareds.get(key);
  if (!sh) {
    sh = new Shared(new spotifyapi.Client(), new musicbrainz.Client());
    sh.creds = creds;
    shareds.set(key, sh);
  }
  return sh;
}

export async function cacheKey(refreshToken: string): Promise<string> {
  const sum = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(refreshToken));
  return encodeHex(new Uint8Array(sum));
}

// toEntry classifies rg and returns its entry if it belongs on the dashboard:
// an Album or EP, not a compilation/remix, still upcoming as of now.
export function toEntry(rg: musicbrainz.ReleaseGroup, now: Date): ReleaseEntry | null {
  const cls = classify(rg);
  if (!cls) return null;
  const parsed = parseReleaseDate(rg.firstReleaseDate);
  if (!parsed || !upcoming(parsed.date, parsed.precision, now)) return null;
  return { rg, date: parsed.date, precision: parsed.precision, class: cls };
}

// classify buckets a release group as "album" or "eps" from MusicBrainz's own
// primary type, or "" for anything this widget doesn't show (singles,
// broadcasts, compilations, remix records).
export function classify(rg: Pick<musicbrainz.ReleaseGroup, "primaryType" | "secondaryTypes">): "album" | "eps" | "" {
  if ((rg.secondaryTypes ?? []).some((t) => excludedSecondary.has(t))) return "";
  if (rg.primaryType === "Album") return "album";
  if (rg.primaryType === "EP") return "eps";
  return "";
}

// parseReleaseDate parses a MusicBrainz date, whose precision is implied by
// its length: "2026-10-13", "2026-10" or "2026". The date is local midnight.
export function parseReleaseDate(s: string): { date: Date; precision: Precision } | null {
  let m = /^(\d{4})-(\d\d)-(\d\d)$/.exec(s);
  if (m) return valid(new Date(+m[1], +m[2] - 1, +m[3]), +m[2], +m[3], "day");
  m = /^(\d{4})-(\d\d)$/.exec(s);
  if (m) return valid(new Date(+m[1], +m[2] - 1, 1), +m[2], 1, "month");
  m = /^(\d{4})$/.exec(s);
  if (m) return { date: new Date(+m[1], 0, 1), precision: "year" };
  return null;
}

function valid(d: Date, month: number, day: number, precision: Precision) {
  // reject roll-overs like 2026-02-31
  if (d.getMonth() + 1 !== month || d.getDate() !== day) return null;
  return { date: d, precision };
}

// upcoming reports whether a release dated (start, precision) is still ahead:
// a day or month counts until it's over. A bare year only counts if it's a
// future year — a current-year "2026" is far more often an undated catalog
// entry than an announcement.
export function upcoming(start: Date, precision: Precision, now: Date): boolean {
  switch (precision) {
    case "day": {
      const end = new Date(start);
      end.setDate(end.getDate() + 1);
      return end > now;
    }
    case "month": {
      const end = new Date(start);
      end.setMonth(end.getMonth() + 1);
      return end > now;
    }
    case "year":
      return start.getFullYear() > now.getFullYear();
  }
}
