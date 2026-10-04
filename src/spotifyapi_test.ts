import { assert, assertEquals, assertRejects, assertStringIncludes } from "@std/assert";
import { Client, RateLimitedError, UnauthorizedError } from "./spotifyapi.ts";
import { bg, json, serve, type TestServer } from "./testutil.ts";

function testClient(auth?: TestServer, api?: TestServer): Client {
  const c = new Client();
  if (auth) c.authBase = auth.url;
  if (api) c.apiBase = api.url;
  return c;
}

const noCreds = { clientId: "", clientSecret: "", refreshToken: "" };

Deno.test("authorizeURL", () => {
  const got = new Client().authorizeURL(
    "cid",
    "http://127.0.0.1:7373/settings/spotify/callback",
    "xyz",
    "user-follow-read",
  );
  assert(got.startsWith("https://accounts.spotify.com/authorize?"), got);
  for (const want of ["client_id=cid", "response_type=code", "state=xyz", "scope=user-follow-read"]) {
    assertStringIncludes(got, want);
  }
});

Deno.test("exchangeCode", async () => {
  let form = "", authHeader = "";
  await using srv = serve(async (req) => {
    form = await req.text();
    authHeader = req.headers.get("authorization") ?? "";
    return json({ access_token: "at-1", refresh_token: "rt-1", expires_in: 3600 });
  });
  const tr = await testClient(srv).exchangeCode(
    { ...noCreds, clientId: "cid", clientSecret: "csecret" },
    "authcode",
    "http://cb",
    bg(),
  );
  assertEquals(tr, { refreshToken: "rt-1", accessToken: "at-1", expiresIn: 3600 });
  assertEquals(authHeader, "Basic " + btoa("cid:csecret"));
  assertStringIncludes(form, "grant_type=authorization_code");
  assertStringIncludes(form, "code=authcode");
});

Deno.test("exchangeCode error", async () => {
  await using srv = serve(() => new Response(`{"error":"invalid_grant"}`, { status: 400 }));
  await assertRejects(() => testClient(srv).exchangeCode(noCreds, "bad", "http://cb", bg()));
});

Deno.test("refreshAccessToken doesn't need a new refresh token", async () => {
  await using srv = serve(() => json({ access_token: "at-2", expires_in: 3600 }));
  const got = await testClient(srv).refreshAccessToken({ ...noCreds, refreshToken: "rt-1" }, bg());
  assertEquals(got, { accessToken: "at-2", expiresIn: 3600 });
});

Deno.test("whoAmI", async () => {
  await using srv = serve((req) => {
    assertEquals(new URL(req.url).pathname, "/me");
    assertEquals(req.headers.get("authorization"), "Bearer at-1");
    return json({ display_name: "Max" });
  });
  assertEquals(await testClient(undefined, srv).whoAmI("at-1", bg()), "Max");
});

Deno.test("followedArtists paginates", async () => {
  let calls = 0;
  await using srv = serve((req) => {
    calls++;
    const after = new URL(req.url).searchParams.get("after") ?? "";
    if (after === "") return json({ artists: { items: [{ id: "a1", name: "Artist One" }], cursors: { after: "a1" } } });
    if (after === "a1") return json({ artists: { items: [{ id: "a2", name: "Artist Two" }], cursors: { after: "" } } });
    throw new Error(`unexpected after=${after}`);
  });
  const c = testClient(undefined, srv);
  const p1 = await c.followedArtists("at-1", "", bg());
  assertEquals([p1.artists.map((a) => a.name), p1.next], [["Artist One"], "a1"]);
  const p2 = await c.followedArtists("at-1", p1.next, bg());
  assertEquals([p2.artists.map((a) => a.name), p2.next], [["Artist Two"], ""]);
  assertEquals(calls, 2);
});

Deno.test("a long 429 comes back as a RateLimitedError", async () => {
  await using srv = serve(() => new Response(null, { status: 429, headers: { "Retry-After": "74442" } }));
  const err = await assertRejects(() => testClient(undefined, srv).followedArtists("at-1", "", bg()), RateLimitedError);
  assertEquals(err.retryAfter, 74442_000);
});

Deno.test("a 401 is an UnauthorizedError", async () => {
  await using srv = serve(() => new Response(null, { status: 401 }));
  await assertRejects(() => testClient(undefined, srv).whoAmI("expired", bg()), UnauthorizedError);
});
