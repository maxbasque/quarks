// fakefeed is a development-only server that serves feeds shaped like the
// real ones (a YouTube per-channel Atom feed, a Radio-Canada-style news RSS
// feed, a Reddit-style Atom feed) with fresh timestamps on every request.
// Point quarks at it via config.fake.yaml to iterate on the UI without
// touching real APIs.
//
//	deno task fakefeed        # serves on 127.0.0.1:7400

import { parseArgs } from "@std/cli/parse-args";
import { escape as esc } from "../src/web/html.ts";

interface Entry {
  id: string;
  videoId: string;
  title: string;
  link: string;
  author: string;
  published: string; // RFC 3339
  summary: string;
  thumbURL: string;
  comments: string;
}

interface FeedData {
  base: string;
  channelId: string;
  updated: string;
  entries: Entry[];
}

const rfc3339 = (d: Date) => d.toISOString().replace(/\.\d{3}Z$/, "Z");

function feedData(base: string, channelId: string, titles: string[], withThumbs: boolean): FeedData {
  const now = Date.now();
  return {
    base,
    channelId,
    updated: rfc3339(new Date(now)),
    entries: titles.map((title, i) => {
      // newest a few minutes old, then spreading roughly one item per ~40 min
      const ts = new Date(now - i * 37 * 60_000 - 4 * 60_000);
      const slug = slugify(title);
      return {
        id: `fake:${slug}:${i}`,
        videoId: `vid${String(hashInt(slug)).padStart(8, "0")}`,
        title,
        link: `${base}/x/${slug}`,
        author: authors[i % authors.length],
        published: rfc3339(ts),
        summary: `Placeholder summary for a fake item. ${title}.`,
        thumbURL: withThumbs ? `${base}/img/${slug}` : "",
        comments: `${base}/x/${slug}#comments`,
      };
    }),
  };
}

function slugify(s: string): string {
  let out = "";
  for (const ch of s.toLowerCase()) {
    if (/[a-z0-9]/.test(ch)) out += ch;
    else if (out.length > 0 && !out.endsWith("-")) out += "-";
  }
  return out.replace(/^-+|-+$/g, "");
}

// hashInt is FNV-1a, 32-bit, folded to 8 digits.
function hashInt(s: string): number {
  let h = 0x811c9dc5;
  for (const b of new TextEncoder().encode(s)) h = Math.imul(h ^ b, 0x01000193) >>> 0;
  return h % 100_000_000;
}

// ---- content pools -----------------------------------------------------------

const authors = ["Alex Rivera", "Sam Okonkwo", "Priya Nair", "Jonas Berg", "Mei Lin"];

const youtubeTitles = [
  "I Built a Mechanical Keyboard From Scratch",
  "The Physics of Skipping Stones, Explained",
  "Why Old Synthesizers Sound Better (feat. a $12,000 Moog)",
  "Rewriting My Home Server for the Fourth Time",
  "Every Espresso Machine Under $500, Ranked",
  "How Bridges Actually Handle Wind",
  "A Weekend With the New RISC-V Laptop",
  "Restoring a 1974 Reel-to-Reel Tape Deck",
  "The Weird History of the QWERTY Layout",
  "Making Bread With a Sourdough Starter From 1998",
  "I Drove an EV Until It Died on the Highway",
  "What's Inside a Nuclear Density Gauge?",
  "The Lost Art of the Foldable Map",
  "Building a Telescope That Fits in a Backpack",
  "Can You Cool a Room With Just Physics?",
];

const newsTitles = [
  "City council approves expanded bike lane network for downtown core",
  "Provincial budget adds funding for rural broadband over three years",
  "Heat warning issued for the region as temperatures climb past 33 C",
  "Local hospital opens new emergency wing after two-year construction",
  "Transit agency proposes fare changes and a new night-bus service",
  "Farmers report strong harvest despite an unusually dry August",
  "University researchers publish study on lake water quality trends",
  "Bridge repairs to close one lane of the expressway for six weeks",
  "Museum acquires collection of early-20th-century photographs",
  "Housing starts rise for a second consecutive quarter, data shows",
  "Wildfire smoke prompts air quality advisory across the valley",
  "School board debates later start times for high school students",
  "Port authority reports record container traffic for the year",
  "New composting program to roll out to remaining neighbourhoods",
  "Regional election turnout up sharply from the previous cycle",
  "Power utility outlines plan to bury lines in storm-prone areas",
  "Snowfall totals break a decades-old record for the month",
  "Downtown library extends weekend hours after pilot succeeds",
  "Cyclist advocacy group calls for protected intersections",
  "Water main break floods a stretch of the market district",
];

const redditTitles = [
  "My self-hosted setup after 5 years — finally happy with it",
  "PSA: check your backup restores, not just your backups",
  "What are you all using for a read-later service in 2026?",
  "Migrated from Docker Compose to a single binary and don't regret it",
  "Weekly 'what did you deploy' thread",
  "Anyone else running their dashboard as a browser homepage?",
  "Cheap mini PC recommendations for a home server?",
  "I wrote a tiny RSS aggregator and it changed my mornings",
  "How do you handle secrets without a whole vault setup?",
  "Show and tell: my e-ink status display",
];

// ---- templates ---------------------------------------------------------------

function youtubeFeed(f: FeedData): string {
  return `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns:yt="http://www.youtube.com/xml/schemas/2015"
      xmlns:media="http://search.yahoo.com/mrss/"
      xmlns="http://www.w3.org/2005/Atom">
  <yt:channelId>${esc(f.channelId)}</yt:channelId>
  <title>Fake Channel</title>
  <link rel="alternate" href="${esc(f.base)}/x/channel"/>
  <author><name>Fake Channel</name><uri>${esc(f.base)}/x/channel</uri></author>
  <updated>${f.updated}</updated>${
    f.entries.map((e) => `
  <entry>
    <id>yt:video:${e.videoId}</id>
    <yt:videoId>${e.videoId}</yt:videoId>
    <yt:channelId>${esc(f.channelId)}</yt:channelId>
    <title>${esc(e.title)}</title>
    <link rel="alternate" href="${esc(e.link)}"/>
    <author><name>${esc(e.author)}</name></author>
    <published>${e.published}</published>
    <updated>${e.published}</updated>
    <media:group>
      <media:title>${esc(e.title)}</media:title>
      <media:thumbnail url="${esc(e.thumbURL)}" width="480" height="360"/>
      <media:description>${esc(e.summary)}</media:description>
    </media:group>
  </entry>`).join("")
  }
</feed>
`;
}

function newsFeed(f: FeedData): string {
  return `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Fake News — Regional</title>
    <link>${esc(f.base)}/x/news</link>
    <description>Placeholder regional news feed for development.</description>
    <lastBuildDate>${f.updated}</lastBuildDate>${
    f.entries.map((e) => `
    <item>
      <title>${esc(e.title)}</title>
      <link>${esc(e.link)}</link>
      <guid isPermaLink="false">${esc(e.id)}</guid>
      <dc:creator xmlns:dc="http://purl.org/dc/elements/1.1/">${esc(e.author)}</dc:creator>
      <pubDate>${e.published}</pubDate>
      <description>${esc(e.summary)}</description>
    </item>`).join("")
  }
  </channel>
</rss>
`;
}

function redditFeed(f: FeedData): string {
  return `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>fake home feed</title>
  <link rel="alternate" href="${esc(f.base)}/x/reddit"/>
  <updated>${f.updated}</updated>${
    f.entries.map((e) => `
  <entry>
    <id>${esc(e.id)}</id>
    <title>${esc(e.title)}</title>
    <link rel="alternate" href="${esc(e.link)}"/>
    <author><name>u/${esc(e.author)}</name></author>
    <published>${e.published}</published>
    <updated>${e.published}</updated>
    <content type="html">&lt;a href="${esc(e.comments)}"&gt;comments&lt;/a&gt;</content>
  </entry>`).join("")
  }
</feed>
`;
}

// ---- server ------------------------------------------------------------------

const xml = (body: string) => new Response(body, { headers: { "Content-Type": "application/xml; charset=utf-8" } });

// img is a placeholder thumbnail, its hue seeded by the slug.
function img(seed: string): Response {
  const hue = hashInt(seed) % 360;
  return new Response(
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 480 360">` +
      `<rect width="480" height="360" fill="hsl(${hue} 45% 50%)"/>` +
      `<text x="240" y="195" font-family="sans-serif" font-size="40" fill="white" ` +
      `text-anchor="middle" opacity="0.85">▶</text></svg>`,
    { headers: { "Content-Type": "image/svg+xml" } },
  );
}

if (import.meta.main) {
  const args = parseArgs(Deno.args, { string: ["addr"], default: { addr: "127.0.0.1:7400" } });
  const [hostname, port] = [args.addr.replace(/:\d+$/, ""), +args.addr.replace(/^.*:/, "")];
  const base = "http://" + args.addr;

  Deno.serve({ hostname, port, onListen: () => console.log(`fakefeed on ${base}`) }, (req) => {
    const path = new URL(req.url).pathname;
    switch (path) {
      case "/youtube.xml":
        return xml(youtubeFeed(feedData(base, "UC_fake_channel_00000000", youtubeTitles, true)));
      case "/news.xml":
        return xml(newsFeed(feedData(base, "", newsTitles, false)));
      case "/reddit.xml":
        return xml(redditFeed(feedData(base, "", redditTitles, false)));
    }
    if (path.startsWith("/img/")) return img(path.slice("/img/".length));
    return new Response(`fakefeed — try ${base}/youtube.xml, /news.xml, /reddit.xml\n`);
  });
}
