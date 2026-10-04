import { assert, assertEquals, assertRejects, assertThrows } from "@std/assert";
import type { Item } from "../../core/types.ts";
import { bg, serve, serveDir, widgetConfig } from "../../testutil.ts";
import { newRSS } from "./rss.ts";

const testdata = new URL("./testdata/", import.meta.url);

async function fetchItems(cfgSrc: string): Promise<Item[]> {
  return (await newRSS(widgetConfig(cfgSrc)).fetch(bg())).items;
}

const xml = (body: string) => () => new Response(body, { headers: { "Content-Type": "application/xml" } });

Deno.test("youtube feed", async () => {
  await using srv = serveDir(testdata);
  const items = await fetchItems(`type: rss\ntitle: YT\nfeeds: [${srv.url}/youtube.xml]\n`);
  assertEquals(items.length, 2);
  const first = items[0];
  assertEquals(first.title, "First video: nested media:group thumbnail");
  assertEquals(first.source, "YT", "source should be the widget title");
  // thumbnail lives in <media:group><media:thumbnail> — the YouTube nesting
  assertEquals(first.thumbnail, "https://i.ytimg.com/vi/aaaaaaaaaaa/hqdefault.jpg");
  assert(first.publishedAt, "first item has no publishedAt");
  assert(items[1].publishedAt! <= first.publishedAt!, "items not sorted newest-first");
  assert(items[1].thumbnail, "second item lost its media:thumbnail");
  assertEquals(items[1].title, "Second video & an ampersand");
});

Deno.test("news feed", async () => {
  await using srv = serveDir(testdata);
  const items = await fetchItems(`type: rss\ntitle: News\nfeeds: [${srv.url}/news.xml]\n`);
  assertEquals(items.length, 3);
  const byTitle = new Map(items.map((it) => [it.title, it]));
  assertEquals(byTitle.get("Council approves budget")?.author, "Priya Nair", "dc:creator not mapped to author");
  assert(byTitle.get("Storm warning issued & extended"), "ampersand entity not decoded in title");
  assertEquals(byTitle.get("Item with no date and no author")?.publishedAt, null);
  for (const it of items) assert(it.id && it.url, `item ${it.title} missing id or url`);
});

Deno.test("error when all feeds fail", async () => {
  const p = newRSS(widgetConfig("type: rss\ntitle: Dead\nfeeds: [http://127.0.0.1:1/nope.xml]\n"));
  await assertRejects(() => p.fetch(bg()));
});

Deno.test("summaries", async () => {
  await using srv = serve(xml(`<?xml version="1.0"?><rss version="2.0"><channel><title>C</title>
    <item><title>Trump tariffs</title><link>https://news.example/1</link><guid>1</guid>
      <pubDate>Wed, 09 Sep 2026 02:00:00 -0400</pubDate>
      <description>&lt;p&gt;Le président a signé cinq proclamations.&lt;/p&gt;</description></item>
    <item><title>VGC story</title><link>https://vgc.example/2</link><guid>2</guid>
      <pubDate>Wed, 09 Sep 2026 01:00:00 -0400</pubDate>
      <description>&lt;img src="https://vgc.example/lead.jpg"&gt; The tour visits Japan and the UK</description></item>
    <item><title>Reddit link</title><link>https://www.reddit.com/r/x/comments/3/t/</link><guid>3</guid>
      <pubDate>Wed, 09 Sep 2026 00:00:00 -0400</pubDate>
      <description>submitted by /u/bob to r/x [link] [comments]</description></item>
  </channel></rss>`));
  const byTitle = new Map((await fetchItems(`type: rss\ntitle: C\nfeeds: [${srv.url}]\n`)).map((it) => [it.title, it]));
  assertEquals(byTitle.get("Trump tariffs")?.summary, "Le président a signé cinq proclamations.");
  assertEquals(byTitle.get("VGC story")?.summary, "The tour visits Japan and the UK");
  assertEquals(byTitle.get("VGC story")?.thumbnail, "https://vgc.example/lead.jpg");
  assertEquals(byTitle.get("Reddit link")?.summary, "", "Reddit boilerplate should be dropped");
});

Deno.test("CDATA descriptions", async () => {
  await using srv = serve(xml(`<?xml version="1.0"?><rss version="2.0"><channel><title>C</title>
    <item><title>X</title><link>https://x/1</link><guid>1</guid>
      <description><![CDATA[<p>Plain <b>bold</b> &amp; more</p>]]></description></item>
  </channel></rss>`));
  const items = await fetchItems(`type: rss\ntitle: C\nfeeds: [${srv.url}]\n`);
  assertEquals(items[0].summary, "Plain bold & more");
});

Deno.test("summary can be disabled", async () => {
  await using srv = serve(xml(`<?xml version="1.0"?><rss version="2.0"><channel><title>C</title>
    <item><title>X</title><link>https://x/1</link><guid>1</guid>
      <description>a real description</description></item>
  </channel></rss>`));
  const items = await fetchItems(`type: rss\ntitle: C\nsummary: false\nfeeds: [${srv.url}]\n`);
  assertEquals(items[0].summary, "");
});

Deno.test("media:content thumbnail", async () => {
  await using srv = serve(xml(`<?xml version="1.0"?>
  <rss version="2.0" xmlns:media="http://search.yahoo.com/mrss/"><channel><title>N</title>
    <item>
      <title>Story</title><link>https://news.example/s</link><guid>s</guid>
      <pubDate>Wed, 09 Sep 2026 02:00:00 -0400</pubDate>
      <media:content url="https://news.example/img.jpg" type="image/jpeg"/>
    </item>
  </channel></rss>`));
  const items = await fetchItems(`type: rss\ntitle: N\nfeeds: [${srv.url}]\n`);
  assertEquals(items.map((i) => i.thumbnail), ["https://news.example/img.jpg"]);
});

Deno.test("reddit home feed shows the subreddit", async () => {
  await using srv = serve(xml(`<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom">
    <title>home feed</title>
    <entry>
      <id>t3_a</id><title>A post</title>
      <category term="selfhosted" label="r/selfhosted"/>
      <link href="https://www.reddit.com/r/selfhosted/comments/a/a_post/"/>
      <updated>2026-09-08T12:00:00Z</updated>
    </entry>
  </feed>`));
  const items = await fetchItems(`type: rss\ntitle: Reddit\nfeeds: [${srv.url}]\n`);
  assertEquals(items.map((i) => i.source), ["r/selfhosted"]);
});

Deno.test("a non-reddit feed keeps its source", async () => {
  await using srv = serveDir(testdata);
  for (const it of await fetchItems(`type: rss\ntitle: News\nfeeds: [${srv.url}/news.xml]\n`)) {
    assertEquals(it.source, "News");
  }
});

Deno.test("no feeds configured", () => {
  assertThrows(() => newRSS(widgetConfig("type: rss\ntitle: Empty\n")));
});

const item = (title: string, pub: string) =>
  `<item><title>${title}</title><link>https://x/${title}</link><guid>${title}</guid><pubDate>${pub}</pubDate></item>`;

Deno.test("interleave balances busy feeds", async () => {
  // feed A: 5 recent items; feed B: 1 old item.
  await using srv = serve((req) => {
    const path = new URL(req.url).pathname;
    if (path === "/a.xml") {
      return xml(
        `<rss version="2.0"><channel><title>A</title>` +
          item("a1", "2026-09-08T12:00:00Z") + item("a2", "2026-09-08T11:00:00Z") +
          item("a3", "2026-09-08T10:00:00Z") + item("a4", "2026-09-08T09:00:00Z") +
          item("a5", "2026-09-08T08:00:00Z") + `</channel></rss>`,
      )();
    }
    return xml(
      `<rss version="2.0"><channel><title>B</title>` + item("b1", "2026-09-01T00:00:00Z") +
        `</channel></rss>`,
    )();
  });
  const items = await fetchItems(
    `type: rss\ntitle: Mix\ninterleave: true\nfeeds: [${srv.url}/a.xml, ${srv.url}/b.xml]\n`,
  );
  assertEquals(items.length, 6);
  // round-robin: a1, b1, a2, a3, a4, a5 — B's single item is 2nd, not last.
  assertEquals(items[1].title, "b1");
});

Deno.test("merged feeds drop duplicates", async () => {
  // the same story (same GUID) filed in both sections' feeds
  await using srv = serve((req) => {
    const path = new URL(req.url).pathname;
    const other = path === "/a.xml" ? item("a1", "2026-09-08T11:00:00Z") : item("b1", "2026-09-08T10:00:00Z");
    return xml(
      `<rss version="2.0"><channel><title>X</title>` + item("shared", "2026-09-08T12:00:00Z") + other +
        `</channel></rss>`,
    )();
  });
  const items = await fetchItems(`type: rss\ntitle: Mix\nfeeds: [${srv.url}/a.xml, ${srv.url}/b.xml]\n`);
  assertEquals(items.map((i) => i.title), ["shared", "a1", "b1"]);
});

Deno.test("conditional requests reuse the cached items on a 304", async () => {
  let hits = 0;
  await using srv = serve((req) => {
    hits++;
    if (req.headers.get("if-none-match") === '"v1"') return new Response(null, { status: 304 });
    return new Response(
      `<rss version="2.0"><channel><title>C</title>${item("x", "2026-09-08T12:00:00Z")}</channel></rss>`,
      {
        headers: { ETag: '"v1"' },
      },
    );
  });
  const p = newRSS(widgetConfig(`type: rss\ntitle: C\nfeeds: [${srv.url}]\n`));
  assertEquals((await p.fetch(bg())).items.length, 1);
  assertEquals((await p.fetch(bg())).items.length, 1);
  assertEquals(hits, 2);
});

Deno.test("latin-1 feeds are decoded by their declaration", async () => {
  const body = new Uint8Array([
    ...new TextEncoder().encode(
      `<?xml version="1.0" encoding="ISO-8859-1"?><rss version="2.0"><channel><title>C</title><item><title>Qu`,
    ),
    0xe9,
    ...new TextEncoder().encode(`bec</title><link>https://x/1</link></item></channel></rss>`),
  ]);
  await using srv = serve(() => new Response(body));
  const items = await fetchItems(`type: rss\ntitle: C\nfeeds: [${srv.url}]\n`);
  assertEquals(items[0].title, "Québec");
});

Deno.test("JSON Feed", async () => {
  await using srv = serve(() =>
    Response.json({
      version: "https://jsonfeed.org/version/1.1",
      title: "J",
      items: [{ id: "1", url: "https://j/1", title: "One", date_published: "2026-09-08T12:00:00Z", summary: "s" }],
    })
  );
  const items = await fetchItems(`type: rss\ntitle: J\nfeeds: [${srv.url}]\n`);
  assertEquals([items[0].title, items[0].url, items[0].summary], ["One", "https://j/1", "s"]);
});
