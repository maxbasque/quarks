import { assert, assertEquals, assertStringIncludes } from "@std/assert";
import { join } from "@std/path";
import { listSecretKeys, readSecretKeys, setSecrets } from "../config/secrets.ts";
import { Store } from "../core/store.ts";
import { item } from "../core/types.ts";
import { Client as SpotifyClient } from "../spotifyapi.ts";
import { json, serve, type TestServer } from "../testutil.ts";
import { type Meta, Server } from "./server.ts";

function testServer(spotifyFixture?: TestServer): Server {
  const dir = Deno.makeTempDirSync();
  const sp = new SpotifyClient();
  if (spotifyFixture) {
    sp.authBase = spotifyFixture.url;
    sp.apiBase = spotifyFixture.url;
  }
  return new Server(
    new Store(join(dir, "cache")),
    () => Promise.resolve(true),
    join(dir, "secrets.yaml"),
    join(dir, "layout.yaml"),
    () => Promise.resolve(),
    sp,
  );
}

function get(s: Server, path: string, headers: Record<string, string> = {}): Promise<Response> {
  return Promise.resolve(s.routes()(new Request("http://127.0.0.1:7373" + path, { headers })));
}

function postForm(s: Server, path: string, form: [string, string][], headers: Record<string, string> = {}) {
  return Promise.resolve(
    s.routes()(
      new Request("http://127.0.0.1:7373" + path, {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded", ...headers },
        body: new URLSearchParams(form).toString(),
      }),
    ),
  );
}

const loc = (r: Response) => r.headers.get("location");

Deno.test("settings page, not configured", async () => {
  const w = await get(testServer(), "/settings");
  assertEquals(w.status, 200);
  assertStringIncludes(await w.text(), "Non configuré");
});

Deno.test("spotify authorize requires credentials", async () => {
  const w = await get(testServer(), "/settings/spotify/authorize");
  assertEquals([w.status, loc(w)], [302, "/settings?err=no_credentials"]);
});

Deno.test("spotify authorize redirects with a state", async () => {
  const s = testServer();
  setSecrets(s.secretsPath, { spotify_client_id: "cid", spotify_client_secret: "csecret" });
  const w = await get(s, "/settings/spotify/authorize");
  assertEquals(w.status, 302);
  const u = new URL(loc(w)!);
  const state = u.searchParams.get("state");
  assert(state, "no state in authorize redirect");
  assertEquals(s.settings.pending?.state, state);
  assertEquals(u.searchParams.get("redirect_uri"), "http://127.0.0.1:7373/settings/spotify/callback");
});

Deno.test("spotify callback: state mismatch", async () => {
  const s = testServer();
  s.settings.pending = { state: "expected", expires: Date.now() + 60_000 };
  const w = await get(s, "/settings/spotify/callback?code=abc&state=wrong");
  assertEquals([w.status, loc(w)], [302, "/settings?err=state_mismatch"]);
});

Deno.test("spotify callback: expired state", async () => {
  const s = testServer();
  s.settings.pending = { state: "s1", expires: Date.now() - 60_000 };
  assertEquals(loc(await get(s, "/settings/spotify/callback?code=abc&state=s1")), "/settings?err=state_mismatch");
});

Deno.test("spotify callback: denied", async () => {
  assertEquals(loc(await get(testServer(), "/settings/spotify/callback?error=access_denied")), "/settings?err=denied");
});

function fakeSpotify(): TestServer {
  return serve((req) => {
    const path = new URL(req.url).pathname;
    if (path === "/api/token") return json({ access_token: "at-1", refresh_token: "rt-1", expires_in: 3600 });
    if (path === "/me") return json({ display_name: "Max" });
    return new Response("404 page not found", { status: 404 });
  });
}

Deno.test("spotify callback success writes the secret and reloads", async () => {
  await using fixture = fakeSpotify();
  const s = testServer(fixture);
  setSecrets(s.secretsPath, { spotify_client_id: "cid", spotify_client_secret: "csecret" });
  s.settings.pending = { state: "s1", expires: Date.now() + 60_000 };
  let reloaded = false;
  s.reloadNow = () => {
    reloaded = true;
    return Promise.resolve();
  };

  const w = await get(s, "/settings/spotify/callback?code=abc&state=s1");
  assertEquals([w.status, loc(w)], [302, "/settings"]);
  assert(reloaded, "reloadNow was not called");
  assertEquals(s.settings.pending, null, "pending state should be cleared after use (one-shot)");
  assertEquals(readSecretKeys(s.secretsPath, "spotify_refresh_token").spotify_refresh_token, "rt-1");
  assertStringIncludes(await (await get(s, "/settings")).text(), "Connecté : Max");
});

Deno.test("spotify disconnect clears the token", async () => {
  const s = testServer();
  setSecrets(s.secretsPath, {
    spotify_client_id: "cid",
    spotify_client_secret: "csecret",
    spotify_refresh_token: "rt-1",
  });
  s.settings.status = { displayName: "Max", err: "" };
  const w = await postForm(s, "/settings/spotify/disconnect", []);
  assertEquals(w.status, 303);
  const secrets = readSecretKeys(s.secretsPath, "spotify_refresh_token", "spotify_client_id");
  assertEquals(secrets.spotify_refresh_token, undefined, "refresh token not cleared");
  assert(secrets.spotify_client_id, "client_id should survive a disconnect");
  assertEquals(s.settings.status, null, "cached status should be cleared on disconnect");
});

Deno.test("spotify credentials form only overwrites provided fields", async () => {
  const s = testServer();
  setSecrets(s.secretsPath, { spotify_client_id: "existing-id" });
  const w = await postForm(s, "/settings/spotify/credentials", [["client_secret", "new-secret"]]);
  assertEquals(w.status, 303);
  assertEquals(readSecretKeys(s.secretsPath, "spotify_client_id", "spotify_client_secret"), {
    spotify_client_id: "existing-id",
    spotify_client_secret: "new-secret",
  });
});

Deno.test("secrets: set, list and delete", async () => {
  const s = testServer();
  let w = await postForm(s, "/settings/secrets/set", [["key", "reddit_home"], [
    "value",
    "https://reddit.example/.rss",
  ]]);
  assertEquals([w.status, loc(w)], [303, "/settings#secrets"]);
  const page = await (await get(s, "/settings")).text();
  assertStringIncludes(page, "reddit_home");
  assert(!page.includes("reddit.example"), "secret value should never be echoed back into the page");

  w = await postForm(s, "/settings/secrets/delete", [["key", "reddit_home"]]);
  assertEquals(w.status, 303);
  assertEquals(readSecretKeys(s.secretsPath, "reddit_home"), {});
});

Deno.test("secrets: set rejects a bad key", async () => {
  const w = await postForm(testServer(), "/settings/secrets/set", [["key", "Not A Valid Key!"], ["value", "x"]]);
  assertEquals(loc(w), "/settings?serr=bad_key#secrets");
});

Deno.test("secrets: set rejects a reserved key", async () => {
  const s = testServer();
  const w = await postForm(s, "/settings/secrets/set", [["key", "spotify_client_id"], ["value", "sneaky"]]);
  assertEquals(loc(w), "/settings?serr=reserved_key#secrets");
  assertEquals(readSecretKeys(s.secretsPath, "spotify_client_id"), {});
});

Deno.test("secrets: set rejects an empty value", async () => {
  const w = await postForm(testServer(), "/settings/secrets/set", [["key", "reddit_home"], ["value", ""]]);
  assertEquals(loc(w), "/settings?serr=empty_value#secrets");
});

Deno.test("secrets: delete of a reserved key is a no-op", async () => {
  const s = testServer();
  setSecrets(s.secretsPath, { spotify_client_id: "keep-me" });
  await postForm(s, "/settings/secrets/delete", [["key", "spotify_client_id"]]);
  assertEquals(readSecretKeys(s.secretsPath, "spotify_client_id").spotify_client_id, "keep-me");
});

function layoutServer(): Server {
  const s = testServer();
  s.publish({
    theme: "dark",
    ttls: new Map(),
    pages: [],
    layout: [
      {
        name: "Accueil",
        enabled: true,
        maxColumns: 2,
        columns: [
          { name: "Niches", enabled: false, missingSecrets: ["reddit_home"] },
          { name: "Nouvelles", enabled: true, missingSecrets: [] },
          { name: "Aujourd'hui", enabled: true, missingSecrets: [] },
        ],
      },
      {
        name: "Sports",
        enabled: false,
        maxColumns: 3,
        columns: [
          { name: "Canadiens", enabled: true, missingSecrets: [] },
          { name: "Classements", enabled: true, missingSecrets: [] },
        ],
      },
      { name: "Media", enabled: true, maxColumns: 0, columns: [] },
    ],
  });
  return s;
}

Deno.test("settings: layout section", async () => {
  const body = await (await get(layoutServer(), "/settings")).text();
  for (
    const want of [
      `id="layout"`,
      `name="page" value="Media"`,
      `name="cols.Accueil" value="Niches"`,
      `name="cols.Sports" value="Canadiens"`,
      `<code>reddit_home</code>`,
      `data-max="2"`,
      "2 pages sur 3 affichées",
    ]
  ) assertStringIncludes(body, want);
});

Deno.test("settings: layout set", async () => {
  const s = layoutServer();
  const w = await postForm(s, "/settings/layout", [
    ["page", "Sports"],
    ["page", "Accueil"],
    ["cols.Accueil", "Aujourd'hui"],
    ["cols.Accueil", "Niches"],
    ["cols.Sports", "Classements"],
    ["cols.Bogus", "x"],
  ]);
  assertEquals([w.status, loc(w)], [303, "/settings#layout"]);
  // byte-for-byte what the Go version wrote
  assertEquals(
    Deno.readTextFileSync(s.layoutPath),
    "pages:\n    Accueil: true\n    Media: false\n    Sports: true\ncolumns:\n    Accueil:\n        Aujourd'hui: true\n        Niches: true\n        Nouvelles: false\n    Sports:\n        Canadiens: false\n        Classements: true\n",
  );

  const ok: [string, string][] = [["cols.Accueil", "Nouvelles"], ["cols.Sports", "Canadiens"]];
  const cases: [[string, string][], string][] = [
    [[...ok, ["page", "Gone"]], "no_pages"],
    [[["cols.Accueil", "Nouvelles"], ["page", "Media"]], "no_columns"],
    [[["cols.Sports", "Canadiens"], ["page", "Media"], ["cols.Accueil", "Niches"], ["cols.Accueil", "Nouvelles"], [
      "cols.Accueil",
      "Aujourd'hui",
    ]], "too_many"],
  ];
  for (const [form, code] of cases) {
    assertEquals(loc(await postForm(s, "/settings/layout", form)), `/settings?lerr=${code}#layout`);
  }
});

Deno.test("rejects a cross-site POST", async () => {
  const s = testServer();
  const w = await postForm(
    s,
    "/settings/secrets/set",
    [["key", "reddit_home"], ["value", "https://evil.example/rss"]],
    {
      "Sec-Fetch-Site": "cross-site",
    },
  );
  assertEquals(w.status, 403);
  assertEquals(listSecretKeys(s.secretsPath), []);
});

Deno.test("rejects a POST whose Origin doesn't match", async () => {
  const s = testServer();
  const w = await postForm(s, "/refresh", [], { Origin: "http://evil.example" });
  assertEquals(w.status, 403);
});

Deno.test("rejects an unknown Host", async () => {
  const s = testServer();
  const cases: [string, number][] = [
    ["evil.example:7373", 403], // DNS rebinding
    ["localhost:7373", 200],
    ["127.0.0.1:7373", 200],
    ["[::1]:7373", 200],
    ["192.168.1.20:7373", 200], // --addr on the LAN, reached by IP
  ];
  for (const [host, want] of cases) {
    const w = await Promise.resolve(s.routes()(new Request(`http://${host}/settings`, { headers: { Host: host } })));
    assertEquals(w.status, want, host);
    await w.body?.cancel();
  }
});

Deno.test("secrets: set rejects a YAML breakout", async () => {
  const s = testServer();
  const w = await postForm(s, "/settings/secrets/set", [["key", "reddit_home"], ["value", 'x"]\n  - type: rss']]);
  assertEquals(loc(w), "/settings?serr=bad_value#secrets");
  assertEquals(listSecretKeys(s.secretsPath), []);
});

// ---- the dashboard -------------------------------------------------------------

function dashboard(): Server {
  const s = testServer();
  s.store.register("news", "News", 0, 1, "rss");
  s.store.register("hn", "HN", 1, 2, "hackernews");
  s.store.setPayload("news", {
    items: [
      item({
        id: "1",
        title: "Tom & Jerry <b>",
        url: "https://news.example/a b",
        publishedAt: new Date(Date.now() - 3 * 3_600_000),
        score: 5,
        comments: 2,
      }),
      item({ id: "2", title: "Evil", url: "javascript:alert(1)" }),
    ],
  });
  s.store.setError("hn", new Error("boom"));
  const meta: Meta = {
    theme: "dark",
    ttls: new Map([["news", 60_000], ["hn", 60_000]]),
    pages: [{
      name: "Accueil",
      columns: 2,
      columnWeights: [2, 1],
      boxes: [
        { column: 1, order: 0, title: "", members: ["news"] },
        { column: 2, order: 1, title: "", members: ["hn"] },
      ],
    }],
    layout: [],
  };
  s.publish(meta);
  return s;
}

Deno.test("dashboard renders, escapes, and filters unsafe URLs", async () => {
  const w = await get(dashboard(), "/");
  assertEquals(w.status, 200);
  const body = await w.text();
  assertStringIncludes(body, "Tom &amp; Jerry &lt;b&gt;");
  assertStringIncludes(body, 'href="https://news.example/a%20b"');
  assertStringIncludes(body, 'href="#ZgotmplZ"', "javascript: URLs must be neutralized");
  assertStringIncludes(body, "/reader?url=https%3A%2F%2Fnews.example%2Fa%20b");
  assertStringIncludes(body, "il y a 3 h");
  assertStringIncludes(body, "--grid-cols: 2fr 1fr");
  assertStringIncludes(body, "hors ligne", "a widget with only an error shows offline");
  assertStringIncludes(body, "box--danger");
});

Deno.test("dashboard: an unchanged render answers 304", async () => {
  const s = dashboard();
  const first = await get(s, "/");
  const etag = first.headers.get("etag")!;
  await first.body?.cancel();
  assert(etag);
  assertEquals((await get(s, "/", { "If-None-Match": etag })).status, 304);
  s.store.setError("news", new Error("x"));
  const after = await get(s, "/", { "If-None-Match": etag });
  assertEquals(after.status, 200, "a store change invalidates the ETag");
  await after.body?.cancel();
});

Deno.test("static files carry an ETag and revalidate", async () => {
  const s = testServer();
  const w = await get(s, "/static/style.css");
  assertEquals(w.status, 200);
  assertEquals(w.headers.get("content-type"), "text/css; charset=utf-8");
  assertEquals(w.headers.get("cache-control"), "no-cache");
  await w.body?.cancel();
  assertEquals((await get(s, "/static/style.css", { "If-None-Match": w.headers.get("etag")! })).status, 304);
  assertEquals((await get(s, "/static/nope.css")).status, 404);
});

Deno.test("refresh and open require POST; unknown paths 404", async () => {
  const s = testServer();
  assertEquals((await get(s, "/refresh")).status, 405);
  assertEquals((await get(s, "/open")).status, 405);
  assertEquals((await get(s, "/nope")).status, 404);
  assertEquals((await postForm(s, "/refresh", [["key", "x"]])).status, 204);
  assertEquals((await postForm(s, "/open", [["url", "file:///etc/passwd"]])).status, 400);
});
