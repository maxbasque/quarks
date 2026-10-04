import { assert, assertEquals, assertNotEquals, assertRejects } from "@std/assert";
import * as fr from "../../fr.ts";
import * as musicbrainz from "../../musicbrainz.ts";
import * as spotifyapi from "../../spotifyapi.ts";
import { bg, json, serve, type TestServer, widgetConfig } from "../../testutil.ts";
import {
  artistListTTL,
  cacheKey,
  classify,
  parseReleaseDate,
  pollInterval,
  type ReleaseEntry,
  Shared,
  upcoming,
} from "./shared.ts";
import { newSpotify, parseSettings, SpotifyProvider, summarize } from "./spotify.ts";
import { Logger } from "../../log.ts";

const quiet = new Logger(() => {});

const ymd = (d: Date) =>
  `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
const daysFromNow = (n: number) => {
  const d = new Date();
  d.setDate(d.getDate() + n);
  return d;
};

// ---- settings and fetch ------------------------------------------------------------

Deno.test("parseSettings tolerates missing credentials", () => {
  // Missing credentials is a transient "not connected yet" state, not a config
  // mistake: pasting the Media snippet before finishing Connect must not fail
  // the entire config load, just this tab.
  const p = parseSettings(widgetConfig("type: spotify\ntitle: Albums\ninclude: album\n"));
  assertEquals(p.creds, { clientId: "", clientSecret: "", refreshToken: "" });
});

Deno.test("without credentials it builds, but fetch errors", async () => {
  const prov = newSpotify(widgetConfig("type: spotify\ntitle: Albums\ninclude: album\n"));
  await assertRejects(() => prov.fetch(bg()), Error, "not connected");
});

Deno.test("parseSettings rejects a bad include", () => {
  let threw = false;
  try {
    parseSettings(
      widgetConfig("type: spotify\ntitle: X\ninclude: singles\nclient_id: cid\nclient_secret: cs\nrefresh_token: rt\n"),
    );
  } catch {
    threw = true;
  }
  assert(threw);
});

Deno.test("parseSettings defaults", () => {
  const p = parseSettings(
    widgetConfig("type: spotify\ntitle: X\nclient_id: cid\nclient_secret: cs\nrefresh_token: rt\n"),
  );
  assertEquals(p.include, "all");
});

Deno.test("parseSettings ignores the retired track thresholds", () => {
  parseSettings(widgetConfig("type: spotify\ntitle: X\ninclude: eps\nep_min_tracks: 8\nep_max_tracks: 3\n"));
});

// newTestShared never touches the network: lastPollAt is "just now", so
// maybePoll is a no-op for pollInterval.
function newTestShared(): Shared {
  const sh = new Shared(new spotifyapi.Client(), new musicbrainz.Client(), quiet);
  sh.lastPollAt = Date.now();
  return sh;
}

function entry(id: string, cls: "album" | "eps", date: string): ReleaseEntry {
  const p = parseReleaseDate(date)!;
  return {
    rg: {
      id,
      title: id,
      artistCredit: "Artist " + id,
      primaryType: "",
      secondaryTypes: [],
      firstReleaseDate: date,
      artistIds: [],
    },
    date: p.date,
    precision: p.precision,
    class: cls,
  };
}

Deno.test("fetch maps the snapshot to items", async () => {
  const sh = newTestShared();
  const day = daysFromNow(3);
  const e = entry("rg1", "album", ymd(day));
  Object.assign(e.rg, { title: "New Album", artistCredit: "Some Artist", secondaryTypes: ["Live"] });
  sh.releases.set("rg1", e);
  const items = (await new SpotifyProvider(sh, "album").fetch(bg())).items;
  assertEquals(items.length, 1);
  const it = items[0];
  assertEquals([it.id, it.title, it.source, it.url], [
    "rg1",
    "New Album",
    "Some Artist",
    "https://musicbrainz.org/release-group/rg1",
  ]);
  assertEquals(it.summary, "Album · Live · " + fr.dayMonthYear(e.date));
  assertEquals(it.publishedAt?.getTime(), e.date.getTime());
});

Deno.test("an imprecise date has no countdown", async () => {
  const sh = newTestShared();
  const month = new Date();
  month.setMonth(month.getMonth() + 1, 1);
  const date = ymd(month).slice(0, 7);
  sh.releases.set("rg1", entry("rg1", "eps", date));
  const items = (await new SpotifyProvider(sh, "eps").fetch(bg())).items;
  assertEquals(items.length, 1);
  assertEquals(items[0].publishedAt, null, "a month-only date must not render as a precise countdown");
  assertEquals(items[0].summary, "EP · " + fr.monthYear(month));
});

Deno.test("fetch only returns the matching include", async () => {
  const sh = newTestShared();
  sh.releases.set("ep1", entry("ep1", "eps", ymd(daysFromNow(1))));
  assertEquals((await new SpotifyProvider(sh, "album").fetch(bg())).items.length, 0);
});

Deno.test("include all mixes albums and EPs", async () => {
  const sh = newTestShared();
  sh.releases.set("ep1", entry("ep1", "eps", ymd(daysFromNow(1))));
  sh.releases.set("al1", entry("al1", "album", ymd(daysFromNow(2))));
  const items = (await new SpotifyProvider(sh, "all").fetch(bg())).items;
  assertEquals(items.map((i) => i.id), ["ep1", "al1"]);
  assert(items[0].summary.startsWith("EP · ") && items[1].summary.startsWith("Album · "));
});

Deno.test("an empty cache is not an error", async () => {
  assertEquals((await new SpotifyProvider(newTestShared(), "album").fetch(bg())).items, []);
});

Deno.test("summarize a future year", () => {
  assertEquals(summarize(entry("x", "album", "2031")), "Album · 2031, date à venir");
});

// ---- classification and dates ------------------------------------------------------

Deno.test("classify", () => {
  const cases: [string, string[], string][] = [
    ["Album", [], "album"],
    ["Album", ["Live"], "album"],
    ["Album", ["Soundtrack"], "album"],
    ["EP", [], "eps"],
    ["Single", [], ""],
    ["Broadcast", [], ""],
    ["", [], ""],
    ["Album", ["Compilation"], ""],
    ["EP", ["Remix"], ""],
    ["Album", ["DJ-mix"], ""],
  ];
  for (const [primaryType, secondaryTypes, want] of cases) {
    assertEquals(classify({ primaryType, secondaryTypes }), want, `${primaryType} ${secondaryTypes}`);
  }
});

Deno.test("parseReleaseDate", () => {
  const cases: [string, string, string][] = [
    ["2026-11-13", "2026-11-13", "day"],
    ["2026-11", "2026-11-01", "month"],
    ["2026", "2026-01-01", "year"],
  ];
  for (const [input, want, precision] of cases) {
    const got = parseReleaseDate(input)!;
    assertEquals([ymd(got.date), got.precision], [want, precision]);
  }
  assertEquals(parseReleaseDate(""), null);
  assertEquals(parseReleaseDate("garbage"), null);
});

Deno.test("upcoming", () => {
  const now = new Date(2026, 8, 26, 15);
  const cases: [string, boolean][] = [
    ["2026-09-27", true],
    ["2026-09-26", true], // out today: still shown until the day is over
    ["2026-09-25", false],
    ["2026-09", true], // "sometime this month"
    ["2026-10", true],
    ["2026-08", false],
    ["2026", false], // a bare current year is usually an undated catalog entry
    ["2027", true],
  ];
  for (const [date, want] of cases) {
    const p = parseReleaseDate(date)!;
    assertEquals(upcoming(p.date, p.precision, now), want, date);
  }
});

Deno.test("cacheKey dedupes by refresh token", async () => {
  assertEquals(await cacheKey("same-token"), await cacheKey("same-token"));
  assertNotEquals(await cacheKey("token-a"), await cacheKey("token-b"));
});

Deno.test("snapshot filters by class and sorts soonest first", () => {
  const sh = newTestShared();
  sh.releases.set("later", entry("later", "album", ymd(daysFromNow(20))));
  sh.releases.set("sooner", entry("sooner", "album", ymd(daysFromNow(5))));
  sh.releases.set("ep", entry("ep", "eps", ymd(daysFromNow(5))));
  assertEquals(sh.snapshot("album").map((r) => r.rg.id), ["sooner", "later"]);
  assertEquals(sh.snapshot("eps").map((r) => r.rg.id), ["ep"]);
});

Deno.test("prune drops releases that have passed", () => {
  const sh = newTestShared();
  sh.releases.set("past", entry("past", "album", ymd(daysFromNow(-2))));
  sh.releases.set("future", entry("future", "album", ymd(daysFromNow(2))));
  sh.prune();
  assert(!sh.releases.has("past"));
  assert(sh.releases.has("future"));
});

// ---- full polls against fake Spotify and MusicBrainz ---------------------------------

// Fixture fakes both Spotify's Web API and MusicBrainz for a full poll.
class Fixture {
  spotifyHits = 0;
  spotifyStatus = 0; // non-zero: every Spotify request fails with it
  retryAfter = "";
  mbStatus = 0; // non-zero: every release-group query fails with it
  queries: string[] = [];
  nameSearches: string[] = [];
  servers: TestServer[] = [];

  shared(): Shared {
    const sp = serve(() => {
      this.spotifyHits++;
      if (this.spotifyStatus) {
        return new Response(null, {
          status: this.spotifyStatus,
          headers: this.retryAfter ? { "Retry-After": this.retryAfter } : {},
        });
      }
      return json({
        artists: {
          items: [{ id: "sp1", name: "One" }, { id: "sp2", name: "Two" }, { id: "sp3", name: "Three" }],
          cursors: { after: "" },
        },
      });
    });

    const future = ymd(daysFromNow(10));
    const past = ymd(daysFromNow(-10));
    const nm = new Date();
    nm.setMonth(nm.getMonth() + 1, 1);
    const nextMonth = ymd(nm).slice(0, 7);
    const mb = serve((req) => {
      const u = new URL(req.url);
      switch (u.pathname) {
        case "/url":
          return json({
            urls: [{
              resource: "https://open.spotify.com/artist/sp1",
              relations: [{ "target-type": "artist", artist: { id: "mb1" } }],
            }],
          });
        case "/artist": {
          const q = u.searchParams.get("query") ?? "";
          this.nameSearches.push(q);
          return json({ artists: q.includes("Two") ? [{ id: "mb2", name: "Two" }] : [] });
        }
        case "/release-group": {
          this.queries.push(u.searchParams.get("query") ?? "");
          if (this.mbStatus) return new Response(null, { status: this.mbStatus });
          const rg = (id: string, type: string, date: string) => ({
            id,
            title: id,
            "primary-type": type,
            "first-release-date": date,
            "artist-credit": [{ name: "One", artist: { id: "mb1" } }],
          });
          return json({
            count: 4,
            "release-groups": [
              rg("album", "Album", future),
              rg("ep", "EP", nextMonth),
              rg("single", "Single", future),
              rg("old", "Album", past),
            ],
          });
        }
      }
      throw new Error(`unexpected MusicBrainz path ${u.pathname}`);
    });
    this.servers.push(sp, mb);

    const client = new spotifyapi.Client();
    client.apiBase = sp.url;
    const mbc = new musicbrainz.Client();
    mbc.base = mb.url;
    mbc.spacing = 0;
    mbc.retryWaits = [];
    const sh = new Shared(client, mbc, quiet);
    sh.tok = "at-1"; // skip the token refresh
    sh.tokExp = Date.now() + 3_600_000;
    return sh;
  }

  async [Symbol.asyncDispose]() {
    await Promise.all(this.servers.map((s) => s.close()));
  }
}

const due = (sh: Shared) => (sh.lastPollAt = Date.now() - pollInterval - 1000);

Deno.test("poll end to end", async () => {
  await using f = new Fixture();
  const sh = f.shared();

  assertEquals(await sh.maybePoll(bg()), null);
  assertEquals(sh.snapshot("album").map((r) => r.rg.id), ["album"], "singles and past records must be dropped");
  const eps = sh.snapshot("eps");
  assertEquals([eps.length, eps[0]?.rg.id, eps[0]?.precision], [1, "ep", "month"]);
  assertEquals([sh.mbids.get("sp1"), sh.mbids.get("sp2"), sh.mbids.get("sp3")], ["mb1", "mb2", undefined]);
  assertEquals(f.queries.length, 1);
  assert(f.queries[0].includes("arid:mb1"));

  // Next poll: the name-matched artist makes a sweep due right away, and the
  // unmatched one isn't searched again so soon.
  due(sh);
  assertEquals(await sh.maybePoll(bg()), null);
  assertEquals(f.queries.length, 2);
  assert(f.queries[1].includes("arid:mb2"), "second sweep should include the newly mapped artist");
  assertEquals(f.nameSearches.length, 2, "Two and Three once each");
  assertEquals(f.spotifyHits, 1, "followed artists should be fetched once a day");

  // Third poll: nothing due, so MusicBrainz isn't touched at all.
  due(sh);
  await sh.maybePoll(bg());
  assertEquals(f.queries.length, 2);
});

Deno.test("maybePoll skips within the interval", async () => {
  await using f = new Fixture();
  const sh = f.shared();
  await sh.maybePoll(bg());
  await sh.maybePoll(bg()); // the other tab, or a double refresh click
  assertEquals([f.spotifyHits, f.queries.length], [1, 1]);
});

Deno.test("a failed sweep keeps the last good releases", async () => {
  await using f = new Fixture();
  const sh = f.shared();
  assertEquals(await sh.maybePoll(bg()), null);
  f.mbStatus = 500;
  sh.sweepDue = true;
  due(sh);
  assertEquals(await sh.maybePoll(bg()), null, "the last sweep's data is still shown");
  assertEquals(sh.snapshot("album").length, 1);
});

Deno.test("a first sweep failure is reported", async () => {
  await using f = new Fixture();
  f.mbStatus = 503;
  const sh = f.shared();
  assert(await sh.maybePoll(bg()), "with nothing ever fetched, a MusicBrainz failure must surface");
});

Deno.test("a Spotify 429 blocks Spotify until Retry-After", async () => {
  await using f = new Fixture();
  f.spotifyStatus = 429;
  f.retryAfter = "74442";
  const sh = f.shared();
  assert(await sh.maybePoll(bg()), "no artist list at all should be an error");
  assert(sh.blockedUntil - Date.now() >= 20 * 3_600_000, "blockedUntil should honor Retry-After");
  due(sh);
  await sh.maybePoll(bg());
  assertEquals(f.spotifyHits, 1, "no Spotify request may be made while blocked");
});

Deno.test("a Spotify 401 drops the cached token", async () => {
  await using f = new Fixture();
  f.spotifyStatus = 401;
  const sh = f.shared();
  await sh.maybePoll(bg());
  assertEquals(sh.tok, "");
});

Deno.test("a stale artist list is still used when Spotify fails", async () => {
  await using f = new Fixture();
  const sh = f.shared();
  assertEquals(await sh.maybePoll(bg()), null);
  f.spotifyStatus = 500;
  sh.artistsCheckedAt = Date.now() - artistListTTL - 3_600_000;
  due(sh);
  assertEquals(await sh.maybePoll(bg()), null, "yesterday's artist list is good enough");
  assertEquals(sh.artists.length, 3);
});
