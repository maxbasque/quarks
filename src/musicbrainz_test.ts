import { assert, assertEquals, assertRejects, assertStringIncludes } from "@std/assert";
import { withTimeout } from "./ctx.ts";
import { Client, escapeQuery, maxURLLookup, RateLimitedError, releaseGroupURL } from "./musicbrainz.ts";
import { bg, json, serve, type TestServer } from "./testutil.ts";

function testClient(h: (req: Request) => Response | Promise<Response>): { c: Client; srv: TestServer } {
  const srv = serve(h);
  const c = new Client();
  c.base = srv.url;
  c.spacing = 0;
  c.retryWaits = [0, 0];
  return { c, srv };
}

Deno.test("artistsBySpotify: batch", async () => {
  const { c, srv } = testClient((req) => {
    assertStringIncludes(req.headers.get("user-agent") ?? "", "quarks", "MusicBrainz requires a descriptive UA");
    assertEquals(new URL(req.url).searchParams.getAll("resource").length, 2);
    return json({
      urls: [{
        resource: "https://open.spotify.com/artist/sp1",
        relations: [{ "target-type": "artist", artist: { id: "mb1" } }],
      }],
    });
  });
  await using _ = srv;
  assertEquals(await c.artistsBySpotify(["sp1", "sp2"], bg()), new Map([["sp1", "mb1"]]));
});

Deno.test("artistsBySpotify: a single missing link is not an error", async () => {
  const { c, srv } = testClient(() => json({ error: "Not Found" }, { status: 404 }));
  await using _ = srv;
  assertEquals((await c.artistsBySpotify(["nope"], bg())).size, 0);
});

Deno.test("artistsBySpotify: a single resource is a bare entity", async () => {
  const { c, srv } = testClient(() =>
    json({
      resource: "https://open.spotify.com/artist/sp1",
      relations: [{ "target-type": "artist", artist: { id: "mb1" } }],
    })
  );
  await using _ = srv;
  assertEquals((await c.artistsBySpotify(["sp1"], bg())).get("sp1"), "mb1");
});

Deno.test("artistsBySpotify rejects an oversized batch", async () => {
  const { c, srv } = testClient(() => {
    throw new Error("must not send");
  });
  await using _ = srv;
  await assertRejects(() => c.artistsBySpotify(new Array(maxURLLookup + 1).fill("x"), bg()));
});

Deno.test("artistByName requires an unambiguous exact match", async () => {
  const resp: Record<string, unknown[]> = {
    Radiohead: [{ id: "rh", name: "Radiohead" }, { id: "x", name: "DJ Radiohead" }],
    Ghost: [{ id: "g1", name: "Ghost" }, { id: "g2", name: "ghost" }],
    Nobody: [{ id: "n", name: "Nobody Special" }],
  };
  const { c, srv } = testClient((req) => {
    const q = new URL(req.url).searchParams.get("query") ?? "";
    return json({ artists: resp[q.replace(/^artist:"/, "").replace(/"$/, "")] });
  });
  await using _ = srv;
  assertEquals(await c.artistByName("Radiohead", bg()), "rh");
  assertEquals(await c.artistByName("Ghost", bg()), null, "two exact matches must not be guessed between");
  assertEquals(await c.artistByName("Nobody", bg()), null, "a partial match must not count");
});

Deno.test("escapeQuery", () => {
  assertEquals(escapeQuery(`AC/DC "live"`), `AC\\/DC \\"live\\"`);
});

Deno.test("upcomingReleaseGroups: query and parse", async () => {
  const { c, srv } = testClient((req) => {
    assertEquals(
      new URL(req.url).searchParams.get("query"),
      "(arid:a1 OR arid:a2) AND firstreleasedate:[2026-09-01 TO *]",
    );
    return json({
      count: 1,
      "release-groups": [{
        id: "rg1",
        title: "New One",
        "primary-type": "Album",
        "secondary-types": ["Live"],
        "first-release-date": "2026-10-13",
        "artist-credit": [
          { name: "A", joinphrase: " & ", artist: { id: "a1" } },
          { name: "B", joinphrase: "", artist: { id: "b" } },
        ],
      }],
    });
  });
  await using _ = srv;
  const got = await c.upcomingReleaseGroups(["a1", "a2"], new Date(2026, 8, 1), bg());
  assertEquals(got.length, 1);
  const rg = got[0];
  assertEquals([rg.title, rg.primaryType, rg.firstReleaseDate, rg.artistCredit], [
    "New One",
    "Album",
    "2026-10-13",
    "A & B",
  ]);
  assertEquals([rg.secondaryTypes.length, rg.artistIds.length], [1, 2]);
  assertEquals(releaseGroupURL(rg), "https://musicbrainz.org/release-group/rg1");
});

Deno.test("upcomingReleaseGroups paginates", async () => {
  let calls = 0;
  const { c, srv } = testClient((req) => {
    calls++;
    const n = new URL(req.url).searchParams.get("offset") === "100" ? 30 : 100;
    return json({ count: 130, "release-groups": Array.from({ length: n }, () => ({ id: "x" })) });
  });
  await using _ = srv;
  const got = await c.upcomingReleaseGroups(["a1"], new Date(), bg());
  assertEquals([got.length, calls], [130, 2]);
});

Deno.test("a 503 is retried, then reported as rate limiting", async () => {
  let calls = 0;
  const { c, srv } = testClient(() => {
    calls++;
    return new Response(null, { status: 503 });
  });
  await using _ = srv;
  await assertRejects(() => c.upcomingReleaseGroups(["a1"], new Date(), bg()), RateLimitedError);
  assertEquals(calls, 3, "1 + 2 retries");
});

Deno.test("a transient 503 recovers", async () => {
  let calls = 0;
  const { c, srv } = testClient(() => {
    if (++calls === 1) return new Response(null, { status: 503 });
    return json({ artists: [{ id: "a", name: "A" }] });
  });
  await using _ = srv;
  assertEquals(await c.artistByName("A", bg()), "a");
});

Deno.test("no retry past the deadline", async () => {
  let calls = 0;
  const { c, srv } = testClient(() => {
    calls++;
    return new Response(null, { status: 503 });
  });
  await using _ = srv;
  c.retryWaits = [2000];
  // shorter than the first real retry wait
  await assertRejects(() => c.artistByName("A", withTimeout(bg(), 1000)), RateLimitedError);
  assertEquals(calls, 1);
});

Deno.test("requests are spaced", async () => {
  let last = 0, minGap = Infinity;
  const { c, srv } = testClient(() => {
    const now = performance.now();
    if (last !== 0) minGap = Math.min(minGap, now - last);
    last = now;
    return json({ artists: [] });
  });
  await using _ = srv;
  c.spacing = 50;
  for (let i = 0; i < 3; i++) await c.artistByName("x", bg());
  assert(minGap >= 45, `requests only ${minGap}ms apart, want >= spacing`);
});
