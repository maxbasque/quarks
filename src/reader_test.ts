import { assert, assertEquals, assertRejects } from "@std/assert";
import { Reader } from "./reader.ts";
import { bg, serve } from "./testutil.ts";

const page = `<!doctype html><html><head><title>Site</title></head><body>
<header>nav junk</header>
<article>
  <h1>The Headline</h1>
  <p>First real paragraph with enough words to look like body content and survive the readability score threshold.</p>
  <p>Second paragraph, also substantive, discussing the topic at some length so the extractor keeps it.</p>
  <script>window.evil = 1;</script>
  <p onclick="steal()">Third paragraph with an inline handler that must be stripped.</p>
</article>
<footer>footer junk</footer>
</body></html>`;

const html = (body: string) => () => new Response(body, { headers: { "Content-Type": "text/html; charset=utf-8" } });

Deno.test("get extracts and sanitizes", async () => {
  await using srv = serve(html(page));
  const art = await new Reader().get(srv.url, bg());
  assert(art.html.includes("First real paragraph"), art.html);
  assert(!art.html.includes("<script") && !art.html.includes("window.evil"), "script not stripped");
  assert(!art.html.includes("onclick"), "inline handler not stripped");
  // class names from the page must not survive — they collide with the
  // dashboard stylesheet (readability's own wrapper class among them)
  assert(!art.html.includes("class="), "class attribute not stripped");
});

Deno.test("get rejects a contentless page", async () => {
  // An audio/video page: title and outlet, but no article prose.
  await using srv = serve(html(`<!doctype html><html><head><title>Podcast Episode</title>
<meta property="og:site_name" content="Some Radio"></head><body>
<h1>Podcast Episode</h1><div class="player">▶</div><p>Listen now.</p>
</body></html>`));
  await assertRejects(() => new Reader().get(srv.url, bg()));
});

Deno.test("get rejects non-http", async () => {
  await assertRejects(() => new Reader().get("file:///etc/passwd", bg()));
});

Deno.test("get caches", async () => {
  let hits = 0;
  await using srv = serve(() => {
    hits++;
    return new Response(page, { headers: { "Content-Type": "text/html" } });
  });
  const r = new Reader();
  for (let i = 0; i < 3; i++) await r.get(srv.url, bg());
  assertEquals(hits, 1);
});

Deno.test("links are absolute and nofollow", async () => {
  await using srv = serve(html(page.replace("First real", 'First <a href="/x">real</a>')));
  const art = await new Reader().get(srv.url, bg());
  assert(art.html.includes(`href="${srv.url}/x"`), art.html);
  assert(art.html.includes('rel="nofollow"'), art.html);
});
