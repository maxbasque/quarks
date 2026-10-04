// Tests for the smaller providers, one section each.

import { assert, assertEquals, assertRejects, assertStringIncludes, assertThrows } from "@std/assert";
import { bg, serve, widgetConfig } from "../testutil.ts";
import { F1, newF1 } from "./f1.ts";
import { newHackerNews } from "./hackernews.ts";
import { newNHL, NHL } from "./nhl.ts";
import { newOnThisDay, OnThisDay } from "./onthisday.ts";
import { newPOTD, POTD } from "./potd.ts";
import { newReddit, Reddit } from "./reddit.ts";
import { newStandings, StandingsProvider } from "./standings.ts";
import { newWeather, WeatherProvider } from "./weather.ts";
import { newYouTube, youtube } from "./youtube.ts";

const text = (body: string) => () => new Response(body);

// ---- hackernews ----------------------------------------------------------------

Deno.test("hackernews: fetch", async () => {
  let query = new URLSearchParams();
  await using srv = serve((req) => {
    query = new URL(req.url).searchParams;
    return Response.json({
      hits: [
        {
          objectID: "1",
          title: "Show HN",
          url: "https://x",
          author: "a",
          points: 10,
          num_comments: 2,
          created_at_i: 1788800000,
        },
        {
          objectID: "2",
          title: "Ask HN: why?",
          url: "",
          author: "b",
          points: 3,
          num_comments: 1,
          created_at_i: 1788790000,
        },
        { objectID: "3", title: "" },
      ],
    });
  });
  const p = newHackerNews(widgetConfig("type: hackernews\ntitle: HN\nlimit: 5\n")) as any;
  p.endpoint = srv.url;
  const items = (await p.fetch(bg())).items;
  assertEquals(query.get("tags"), "front_page");
  assertEquals(query.get("hitsPerPage"), "5");
  assertEquals(items.length, 2, "untitled hits are dropped");
  assertEquals([items[0].score, items[0].comments], [10, 2]);
  assertEquals(items[0].commentsUrl, "https://news.ycombinator.com/item?id=1");
  assertEquals(items[1].url, items[1].commentsUrl, "a text post links to its discussion");
  assertEquals(items[0].publishedAt?.getTime(), 1788800000_000);
});

// ---- reddit --------------------------------------------------------------------

const redditSample = `{
  "kind": "Listing",
  "data": {"children": [
    {"kind": "t3", "data": {
      "id": "aaa", "title": "A link post", "url": "https://example.com/article",
      "permalink": "/r/selfhosted/comments/aaa/a_link_post/", "score": 128,
      "num_comments": 44, "created_utc": 1788800000, "author": "alice",
      "subreddit_name_prefixed": "r/selfhosted", "thumbnail": "https://b.thumbs.redditmedia.com/x.jpg",
      "is_self": false, "stickied": false
    }},
    {"kind": "t3", "data": {
      "id": "bbb", "title": "A self post", "url": "https://www.reddit.com/r/selfhosted/comments/bbb/",
      "permalink": "/r/selfhosted/comments/bbb/a_self_post/", "score": 12, "num_comments": 3,
      "created_utc": 1788790000, "author": "bob", "subreddit_name_prefixed": "r/selfhosted",
      "thumbnail": "self", "is_self": true, "stickied": false
    }},
    {"kind": "t3", "data": {"id": "ccc", "title": "Pinned", "stickied": true, "permalink": "/x"}}
  ]}
}`;

Deno.test("reddit: fetch", async () => {
  let path = "";
  await using srv = serve((req) => {
    path = new URL(req.url).pathname;
    return new Response(redditSample, { headers: { "Content-Type": "application/json" } });
  });
  const p = newReddit(widgetConfig("type: reddit\ntitle: Reddit\nsubreddits: [selfhosted, bazzite]\n")) as Reddit;
  p.base = srv.url;
  const items = (await p.fetch(bg())).items;
  assertEquals(path, "/r/selfhosted+bazzite/hot.json");
  assertEquals(items.length, 2, "stickied dropped");
  const [link, self] = items;
  assertEquals(link.url, "https://example.com/article");
  assertEquals(link.commentsUrl, "https://www.reddit.com/r/selfhosted/comments/aaa/a_link_post/");
  assertEquals([link.score, link.comments], [128, 44]);
  assertEquals([link.author, link.source], ["u/alice", "r/selfhosted"]);
  assert(link.thumbnail, "link post should keep its http thumbnail");
  assertEquals(self.url, self.commentsUrl, "self post URL should be the permalink");
  assertEquals(self.thumbnail, "");
});

Deno.test("reddit: a non-JSON response is a clear error", async () => {
  await using srv = serve(() => new Response("<html>blocked</html>", { headers: { "Content-Type": "text/html" } }));
  const p = newReddit(widgetConfig("type: reddit\ntitle: R\nsubreddits: [selfhosted]\n")) as Reddit;
  p.base = srv.url;
  await assertRejects(() => p.fetch(bg()), Error, "residential IP");
});

Deno.test("reddit: requires subreddits", () => {
  assertThrows(() => newReddit(widgetConfig("type: reddit\ntitle: R\n")));
});

// ---- weather -------------------------------------------------------------------

const weatherSample = `{
  "current": {"temperature_2m": 15.3, "apparent_temperature": 14.3, "weather_code": 3},
  "daily": {
    "time": ["2026-09-08","2026-09-09","2026-09-10","2026-09-11","2026-09-12"],
    "weather_code": [55, 65, 3, 3, 0],
    "temperature_2m_max": [22.3, 23.5, 20.9, 19.4, 23.0],
    "temperature_2m_min": [14.5, 13.2, 13.2, 9.4, 12.1]
  }
}`;

Deno.test("weather: fetch", async () => {
  let days = "";
  await using srv = serve((req) => {
    days = new URL(req.url).searchParams.get("forecast_days") ?? "";
    return new Response(weatherSample);
  });
  const p = newWeather(
    widgetConfig("type: weather\ntitle: Test City\nlatitude: 45.5\nlongitude: -73.57\nforecast_days: 3\n"),
  ) as WeatherProvider;
  p.endpoint = srv.url;
  const w = (await p.fetch(bg())).weather!;
  assertEquals(days, "4", "days + today");
  assertEquals(w.location, "Test City");
  assertEquals([w.current, w.feelsLike], [15.3, 14.3]);
  assertEquals(w.condition, "Couvert");
  assertEquals([w.today.high, w.today.low], [22.3, 14.5]);
  assertEquals(w.forecast.length, 4, "today excluded");
  assertEquals(w.forecast[0].condition, "Pluie");
  assertEquals(w.forecast[0].date?.toISOString(), "2026-09-09T00:00:00.000Z");
});

Deno.test("weather: requires coordinates", () => {
  assertThrows(() => newWeather(widgetConfig("type: weather\ntitle: Nowhere\n")));
});

// ---- youtube -------------------------------------------------------------------

const channelFeed = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns:yt="http://www.youtube.com/xml/schemas/2015"
      xmlns:media="http://search.yahoo.com/mrss/"
      xmlns="http://www.w3.org/2005/Atom">
  <title>Some Channel</title>
  <entry>
    <id>yt:video:vvvvvvvvvvv</id>
    <yt:videoId>vvvvvvvvvv</yt:videoId>
    <title>A video</title>
    <link rel="alternate" href="https://www.youtube.com/watch?v=vvvvvvvvvv"/>
    <published>2026-09-08T10:00:00+00:00</published>
    <media:group>
      <media:thumbnail url="https://i.ytimg.com/vi/vvvvvvvvvv/hqdefault.jpg" width="480" height="360"/>
    </media:group>
  </entry>
</feed>`;

Deno.test("youtube: channels and playlists become feeds", async () => {
  let channel = "", playlist = "";
  await using srv = serve((req) => {
    const q = new URL(req.url).searchParams;
    channel ||= q.get("channel_id") ?? "";
    playlist ||= q.get("playlist_id") ?? "";
    return new Response(channelFeed, { headers: { "Content-Type": "application/xml" } });
  });
  const old = youtube.feedBase;
  youtube.feedBase = srv.url + "/feeds/videos.xml?";
  try {
    const p = newYouTube(
      widgetConfig("type: youtube\ntitle: YT\nchannels: [UCtestchannelid000000]\nplaylists: [PLtestplaylist00000]\n"),
    );
    const items = (await p.fetch(bg())).items;
    // both feeds serve the same video; it's listed once
    assertEquals(items.length, 1);
    assertEquals([channel, playlist], ["UCtestchannelid000000", "PLtestplaylist00000"]);
    assert(items[0].thumbnail, "thumbnail not extracted from media:group");
    assertEquals(items[0].source, "Some Channel", "source falls back to the feed title");
  } finally {
    youtube.feedBase = old;
  }
});

Deno.test("youtube: rejects bad IDs", () => {
  assertThrows(() => newYouTube(widgetConfig("type: youtube\ntitle: YT\nchannels: [not-a-channel]\n")));
  assertThrows(() => newYouTube(widgetConfig("type: youtube\ntitle: YT\nplaylists: [WL]\n")));
});

Deno.test("youtube: requires something", () => {
  assertThrows(() => newYouTube(widgetConfig("type: youtube\ntitle: YT\n")));
});

// ---- nhl -----------------------------------------------------------------------

Deno.test("nhl: schedule", async () => {
  const soon = new Date(Date.now() + 48 * 3_600_000).toISOString().replace(/\.\d+Z$/, "Z");
  const past = new Date(Date.now() - 10 * 86_400_000).toISOString().replace(/\.\d+Z$/, "Z");
  const body = `{"games":[
    {"id":1,"startTimeUTC":"${past}","gameState":"OFF","gameType":2,
     "awayTeam":{"abbrev":"MTL","commonName":{"default":"Canadiens"},"score":3,"logo":"mtl.svg"},
     "homeTeam":{"abbrev":"TOR","commonName":{"default":"Maple Leafs"},"score":2,"logo":"tor.svg"}},
    {"id":2,"startTimeUTC":"${soon}","gameState":"FUT","gameType":1,
     "venue":{"default":"Bell Centre"},
     "awayTeam":{"abbrev":"OTT","commonName":{"default":"Senators"},"logo":"ott.svg"},
     "homeTeam":{"abbrev":"MTL","commonName":{"default":"Canadiens"},"logo":"mtl.svg"}}
  ]}`;
  let path = "";
  await using srv = serve((req) => {
    path = new URL(req.url).pathname;
    return new Response(body);
  });
  const p = newNHL(widgetConfig("type: nhl\ntitle: Habs\nteam: mtl\n")) as NHL;
  p.scheduleURL = srv.url + "/v1/club-schedule-season/%s/now";
  const items = (await p.fetch(bg())).items;
  assertStringIncludes(path, "/MTL/");
  // the 10-day-old game is dropped; the upcoming one remains
  assertEquals(items.length, 1);
  const it = items[0];
  assertEquals(it.title, "c. Senators");
  assertStringIncludes(it.summary, "Bell Centre");
  assertStringIncludes(it.summary, " h ");
  assertEquals(it.source, "Préparatoire");
  assertEquals(it.thumbnail, "ott.svg", "the opponent's logo");
  assertEquals(it.url, "https://www.nhl.com/gamecenter/2");
});

Deno.test("nhl: requires a team, except for scores", () => {
  assertThrows(() => newNHL(widgetConfig("type: nhl\ntitle: X\n")));
  newNHL(widgetConfig("type: nhl\ntitle: X\nmode: scores\n"));
});

Deno.test("nhl: scores", async () => {
  await using srv = serve(text(`{"prevDate":"","games":[
    {"id":10,"startTimeUTC":"2026-10-08T23:00:00Z","gameState":"OFF",
     "gameOutcome":{"lastPeriodType":"OT"},
     "awayTeam":{"abbrev":"MTL","name":{"default":"Canadiens"},"score":4,"logo":"mtl.svg"},
     "homeTeam":{"abbrev":"TOR","name":{"default":"Maple Leafs"},"score":3,"logo":"tor.svg"}},
    {"id":11,"startTimeUTC":"2026-10-08T22:00:00Z","gameState":"FUT",
     "awayTeam":{"abbrev":"BOS"},"homeTeam":{"abbrev":"NYR"}}
  ]}`));
  const p = newNHL(widgetConfig("type: nhl\ntitle: League\nmode: scores\n")) as NHL;
  p.scoreURL = srv.url + "/v1/score/%s";
  const items = (await p.fetch(bg())).items;
  assertEquals(items.length, 1, "the FUT game is skipped");
  assertEquals(items[0].title, "MTL 4 – 3 TOR");
  assert(items[0].summary.startsWith("Final (prol.)"), items[0].summary);
  assertEquals(items[0].thumbnail, "mtl.svg", "the winner's logo");
});

// ---- standings -----------------------------------------------------------------

Deno.test("standings: nhl", async () => {
  await using srv = serve(text(`{"standings":[
    {"teamAbbrev":{"default":"TOR"},"teamCommonName":{"default":"Maple Leafs"},"teamLogo":"tor.svg",
     "divisionName":"Atlantic","gamesPlayed":82,"wins":50,"losses":24,"otLosses":8,"points":108,"divisionSequence":2,"streakCode":"W","streakCount":3},
    {"teamAbbrev":{"default":"MTL"},"teamCommonName":{"default":"Canadiens"},"teamLogo":"mtl.svg",
     "divisionName":"Atlantic","gamesPlayed":82,"wins":52,"losses":22,"otLosses":8,"points":112,"divisionSequence":1,"streakCode":"L","streakCount":1},
    {"teamAbbrev":{"default":"NYR"},"teamCommonName":{"default":"Rangers"},"teamLogo":"nyr.svg",
     "divisionName":"Metropolitan","gamesPlayed":82,"wins":45,"losses":30,"otLosses":7,"points":97,"divisionSequence":1}
  ]}`));
  const p = newStandings(widgetConfig("type: standings\ntitle: NHL\nleague: nhl\nteam: mtl\n")) as StandingsProvider;
  p.nhlURL = srv.url;
  const s = (await p.fetch(bg())).standings!;
  assertEquals(s.groups.length, 2);
  const atl = s.groups[0];
  assertEquals([atl.name, atl.rows.length], ["Atlantique", 2]);
  // sorted by divisionSequence: MTL (1) then TOR (2)
  assertEquals([atl.rows[0].abbrev, atl.rows[0].rank, atl.rows[0].highlight], ["MTL", 1, true]);
  assertEquals([atl.rows[0].values[1], atl.rows[0].values[2]], ["52-22-8", "112"]);
  assertEquals(atl.rows[1].values[3], "W3");
});

Deno.test("standings: mlb", async () => {
  await using srv = serve(text(`{"records":[
    {"division":{"id":201},"teamRecords":[
      {"team":{"id":147,"name":"Yankees"},"wins":95,"losses":67,"winningPercentage":".586","gamesBack":"-","divisionRank":"1","streak":{"streakCode":"W2"}},
      {"team":{"id":111,"name":"Red Sox"},"wins":89,"losses":73,"winningPercentage":".549","gamesBack":"6.0","divisionRank":"2","streak":{"streakCode":"L1"}}
    ]}
  ]}`));
  const p = newStandings(
    widgetConfig("type: standings\ntitle: MLB\nleague: mlb\nteam: Yankees\n"),
  ) as StandingsProvider;
  p.mlbURL = srv.url;
  const g = (await p.fetch(bg())).standings!.groups[0];
  assertEquals(g.name, "AL Est");
  assertEquals([g.rows[0].team, g.rows[0].highlight], ["Yankees", true]);
  assertEquals(g.rows[0].values.slice(0, 3), ["95-67", ".586", "—"]);
  assertEquals(g.rows[1].values[2], "6.0");
});

Deno.test("standings: bad league", () => {
  assertThrows(() => newStandings(widgetConfig("type: standings\ntitle: X\nleague: nfl\n")));
});

// ---- f1 ------------------------------------------------------------------------

const scheduleJSON = `{"MRData":{"RaceTable":{"Races":[
 {"round":"17","raceName":"Azerbaijan Grand Prix","url":"https://w/aze","date":"2026-09-20","time":"11:00:00Z",
  "Circuit":{"circuitName":"Baku","Location":{"locality":"Baku","country":"Azerbaijan"}}},
 {"round":"18","raceName":"Singapore Grand Prix","url":"https://w/sgp","date":"2026-10-04","time":"11:00:00Z",
  "Circuit":{"circuitName":"Marina Bay","Location":{"locality":"Marina Bay","country":"Singapore"}}},
 {"round":"19","raceName":"United States Grand Prix","url":"https://w/usa","date":"2026-10-18","time":"19:00:00Z",
  "Circuit":{"circuitName":"COTA","Location":{"locality":"Austin","country":"USA"}},
  "Qualifying":{"date":"2026-10-17","time":"21:00:00Z"},"Sprint":{"date":"2026-10-17","time":"17:00:00Z"}},
 {"round":"20","raceName":"Mexico City Grand Prix","url":"https://w/mex","date":"2026-10-25","time":"20:00:00Z",
  "Circuit":{"circuitName":"Hermanos Rodríguez","Location":{"locality":"Mexico City","country":"Mexico"}}}
]}}}`;

const resultsJSON = `{"MRData":{"RaceTable":{"Races":[
 {"round":"17","Results":[{"Driver":{"givenName":"George","familyName":"Russell"},"Constructor":{"name":"Mercedes"}}]}
]}}}`;

function f1Fixture(mode: string, routes: Record<string, string>) {
  const srv = serve((req) => {
    const body = routes[new URL(req.url).pathname];
    return body === undefined ? new Response("404 page not found", { status: 404 }) : new Response(body);
  });
  const p = newF1(widgetConfig(`type: f1\ntitle: F1\nmode: ${mode}\nhighlight: str\n`)) as F1;
  p.base = srv.url + "/f1/current";
  p.now = () => new Date(Date.UTC(2026, 9, 4, 12));
  return { p, srv };
}

Deno.test("f1: schedule", async () => {
  const { p, srv } = f1Fixture("schedule", {
    "/f1/current.json": scheduleJSON,
    "/f1/current/results/1.json": resultsJSON,
  });
  await using _ = srv;
  const items = (await p.fetch(bg())).items;
  // Singapore started an hour ago, so it's still "upcoming"; then the rest of
  // the season soonest first; then past races, most recent first
  assertEquals(items.map((i) => i.source), ["Manche 18", "Manche 19", "Manche 20", "Manche 17"]);
  const usa = items[1].summary;
  // sessions in running order: the sprint (17:00Z) comes before qualifying (21:00Z)
  const sp = usa.indexOf("Sprint "), q = usa.indexOf("Qualifs "), r = usa.indexOf("Course ");
  assert(usa.startsWith("Austin, USA · ") && sp >= 0 && sp < q && q < r, usa);
  assert(items[3].summary.startsWith("🏁 George Russell (Mercedes)"), items[3].summary);
});

Deno.test("f1: schedule without results", async () => {
  const { p, srv } = f1Fixture("schedule", { "/f1/current.json": scheduleJSON });
  await using _ = srv;
  const items = (await p.fetch(bg())).items;
  assertEquals(items.length, 4);
  assert(!items[3].summary.includes("🏁"));
});

Deno.test("f1: driver standings", async () => {
  const { p, srv } = f1Fixture("drivers", {
    "/f1/current/driverStandings.json":
      `{"MRData":{"StandingsTable":{"StandingsLists":[{"round":"16","DriverStandings":[
     {"position":"1","points":"302","wins":"8","Driver":{"code":"ANT","givenName":"Andrea Kimi","familyName":"Antonelli"},"Constructors":[{"name":"Mercedes"}]},
     {"position":"2","points":"280","wins":"5","Driver":{"code":"STR","givenName":"Lance","familyName":"Stroll"},"Constructors":[{"name":"Williams"},{"name":"Aston Martin"}]}
    ]}]}}}`,
  });
  await using _ = srv;
  const g = (await p.fetch(bg())).standings!.groups[0];
  assertEquals([g.name, g.rows.length], ["Après la manche 16", 2]);
  const r = g.rows[1];
  assertEquals([r.rank, r.team, r.values[0], r.values[1], r.highlight], [2, "Stroll", "Aston Martin", "280", true]);
  assert(!g.rows[0].highlight);
});

Deno.test("f1: constructor standings", async () => {
  const { p, srv } = f1Fixture("constructors", {
    "/f1/current/constructorStandings.json":
      `{"MRData":{"StandingsTable":{"StandingsLists":[{"round":"16","ConstructorStandings":[
     {"position":"1","points":"538","wins":"11","Constructor":{"name":"Mercedes"}}
    ]}]}}}`,
  });
  await using _ = srv;
  const r = (await p.fetch(bg())).standings!.groups[0].rows[0];
  assertEquals([r.team, r.values[0], r.values[1]], ["Mercedes", "538", "11"]);
});

Deno.test("f1: bad mode", () => {
  assertThrows(() => newF1(widgetConfig("type: f1\ntitle: F1\nmode: qualifying\n")));
});

// ---- onthisday -----------------------------------------------------------------

Deno.test("onthisday: fetch", async () => {
  let path = "";
  await using srv = serve((req) => {
    path = new URL(req.url).pathname;
    return new Response(`{"selected":[
      {"text":"Premier événement","year":1901,"pages":[
        {"title":"Truc","extract":"Une <b>explication</b>.","content_urls":{"desktop":{"page":"https://fr.wikipedia.org/wiki/Truc"}},"thumbnail":{"source":"https://img/truc.jpg"}}]},
      {"text":"Événement récent","year":1999,"pages":[]},
      {"text":"Sans année","year":0,"pages":[]}
    ]}`);
  });
  const p = newOnThisDay(widgetConfig("type: onthisday\ntitle: Éphéméride\n")) as OnThisDay;
  p.base = srv.url + "/%s";
  p.now = () => new Date(2026, 2, 7, 12);
  const items = (await p.fetch(bg())).items;
  assertEquals(path, "/fr/selected/03/07");
  assertEquals(items.length, 3);
  // most recent year first
  assertEquals(items[0].title, "1999 — Événement récent");
  assertEquals(items[2].title, "Sans année");
  assertEquals([items[1].url, items[1].thumbnail], ["https://fr.wikipedia.org/wiki/Truc", "https://img/truc.jpg"]);
  assertEquals(items[1].summary, "Une explication.");
  assertEquals(items[0].publishedAt, null);
  assertEquals(items[0].score, 0);
});

Deno.test("onthisday: mode validation", () => {
  newOnThisDay(widgetConfig("type: onthisday\ntitle: x\n"));
  newOnThisDay(widgetConfig("type: onthisday\ntitle: x\nmode: births\n"));
  assertThrows(() => newOnThisDay(widgetConfig("type: onthisday\ntitle: x\nmode: bogus\n")), Error, "mode must be");
});

Deno.test("onthisday: mode in path", async () => {
  let path = "";
  await using srv = serve((req) => {
    path = new URL(req.url).pathname;
    return new Response(`{"births":[{"text":"X","year":1950,"pages":[]}]}`);
  });
  const p = newOnThisDay(widgetConfig("type: onthisday\ntitle: x\nmode: births\nlang: en\n")) as OnThisDay;
  p.base = srv.url + "/%s";
  p.now = () => new Date(2026, 11, 1);
  const items = (await p.fetch(bg())).items;
  assertEquals(path, "/en/births/12/01");
  assertEquals(items.map((i) => i.title), ["1950 — X"]);
});

// ---- potd ----------------------------------------------------------------------

const potdBody = `{"image":{
  "title":"File:Grand Canyon vue.jpg",
  "file_page":"https://commons.wikimedia.org/wiki/File:Grand_Canyon_vue.jpg",
  "thumbnail":{"source":"https://upload.wikimedia.org/wikipedia/commons/thumb/a/ab/Grand_Canyon_vue.jpg/640px-Grand_Canyon_vue.jpg"},
  "artist":{"text":"<a href=\\"x\\">Jane Doe</a>"},
  "license":{"type":"CC BY-SA 4.0"},
  "description":{"text":"A <b>wide</b> view of the canyon.","lang":"en"},
  "structured":{"captions":{"en":"Wide view of the canyon","fr":"Vue large du canyon"}}
}}`;

function potd(srv: { url: string }, cfg: string): POTD {
  const p = newPOTD(widgetConfig(cfg)) as POTD;
  p.base = srv.url + "/%s";
  p.now = () => new Date(2026, 1, 3, 9);
  return p;
}

Deno.test("potd: fetch", async () => {
  let path = "";
  await using srv = serve((req) => {
    path = new URL(req.url).pathname;
    return new Response(potdBody);
  });
  const items = (await potd(srv, "type: potd\ntitle: Photo du jour\nlang: fr\n").fetch(bg())).items;
  assertEquals(path, "/fr/2026/02/03");
  assertEquals(items.length, 1);
  const it = items[0];
  assert(it.hero);
  assertEquals(it.title, "Vue large du canyon");
  assertEquals(
    it.thumbnail,
    "https://upload.wikimedia.org/wikipedia/commons/thumb/a/ab/Grand_Canyon_vue.jpg/1280px-Grand_Canyon_vue.jpg",
  );
  assertEquals(it.url, "https://commons.wikimedia.org/wiki/File:Grand_Canyon_vue.jpg");
  assertEquals(it.author, "Jane Doe · CC BY-SA 4.0");
  assertEquals(it.summary, "", "empty when the caption is the title");
  assertEquals(it.publishedAt, null);
});

Deno.test("potd: caption fallback", async () => {
  await using srv = serve(text(`{"image":{"title":"File:X.jpg","file_page":"p",
    "thumbnail":{"source":"https://u/300px-X.jpg"},
    "description":{"text":"desc","lang":"en"},
    "structured":{"captions":{"en":"english caption"}}}}`));
  const items = (await potd(srv, "type: potd\ntitle: x\nlang: fr\n").fetch(bg())).items;
  assertEquals(items[0].title, "english caption");
});

Deno.test("potd: yesterday fallback", async () => {
  const paths: string[] = [];
  await using srv = serve((req) => {
    paths.push(new URL(req.url).pathname);
    return paths.length === 1 ? new Response("", { status: 404 }) : new Response(potdBody);
  });
  const items = (await potd(srv, "type: potd\ntitle: x\n").fetch(bg())).items;
  assertEquals(paths, ["/fr/2026/02/03", "/fr/2026/02/02"]);
  assertEquals(items.length, 1);
});
