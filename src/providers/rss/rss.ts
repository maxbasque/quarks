// The generic feed provider: RSS, Atom and JSON Feed. Radio-Canada, YouTube
// per-channel feeds and the Reddit private home feed are all handled here —
// build this one well and most of M0-M2 follows (plan §11).

import { decodeHTML } from "entities";
import { type Provider, Settings, type WidgetConfig } from "../../core/provider.ts";
import { feed, type Item, item, type Payload } from "../../core/types.ts";
import { readLimited, userAgent } from "../../httpx.ts";
import { decode, type FeedItem, parseFeed } from "./feedparser.ts";

// New is the core.Factory for "rss".
export function newRSS(cfg: WidgetConfig): Provider {
  const s = Settings.of(cfg);
  const feeds = s.strList("feeds");
  if (feeds.length === 0) throw new Error(`rss widget ${JSON.stringify(cfg.title)}: no feeds`);
  // interleave: with several feeds, round-robin their items (newest from each,
  // then next from each) instead of merging into one date-sorted list. Keeps a
  // busy feed from crowding out the others — the default for youtube.
  const interleave = s.bool("interleave") ?? false;
  // summary: show a short plain-text excerpt under each title (default true).
  const summaries = s.bool("summary") ?? true;
  return new RSSProvider(feeds, s.str("source") || cfg.title, interleave, summaries);
}

interface CondTags {
  etag: string;
  lastMod: string;
}

export class RSSProvider implements Provider {
  // per-feed conditional-request state. fetch is never concurrent for one
  // widget (the scheduler serializes it).
  #cond = new Map<string, CondTags>();
  #cache = new Map<string, Item[]>();

  constructor(
    private feeds: string[],
    private source: string,
    private interleave: boolean,
    private summaries: boolean, // show a plain-text excerpt under each title
  ) {}

  async fetch(signal: AbortSignal): Promise<Payload> {
    const perFeed: Item[][] = [];
    let firstErr: Error | null = null;

    for (const url of this.feeds) {
      try {
        perFeed.push(await this.#feedItems(url, signal));
      } catch (err) {
        firstErr ??= new Error(`${url}: ${(err as Error).message}`);
      }
    }

    if (perFeed.length === 0) {
      if (firstErr) throw firstErr;
      return feed([]);
    }

    let items: Item[];
    if (this.interleave && perFeed.length > 1) {
      items = roundRobin(perFeed);
    } else {
      items = perFeed.flat();
      sortByDate(items);
    }
    return feed(dedupe(items));
  }

  // feedItems fetches and parses one feed, using an ETag / Last-Modified
  // conditional request. On a 304 it returns the previously parsed items
  // without re-downloading or re-parsing.
  async #feedItems(url: string, signal: AbortSignal): Promise<Item[]> {
    const headers: Record<string, string> = { "User-Agent": userAgent };
    const c = this.#cond.get(url);
    if (c?.etag) headers["If-None-Match"] = c.etag;
    if (c?.lastMod) headers["If-Modified-Since"] = c.lastMod;

    const resp = await fetch(url, { headers, signal });
    if (resp.status === 304) {
      await resp.body?.cancel();
      return this.#cache.get(url) ?? []; // unchanged since last fetch
    }
    if (resp.status !== 200) {
      await resp.body?.cancel();
      throw new Error(`http ${resp.status}`);
    }

    const parsed = parseFeed(decode(await readLimited(resp, 16 << 20)));
    const source = this.source || parsed.title;
    const items = parsed.items.map((e) => this.#toItem(e, source));
    sortByDate(items);

    this.#cond.set(url, { etag: resp.headers.get("etag") ?? "", lastMod: resp.headers.get("last-modified") ?? "" });
    this.#cache.set(url, items);
    return items;
  }

  #toItem(e: FeedItem, source: string): Item {
    const it = item({
      id: e.guid || e.link,
      title: e.title.trim(),
      url: e.link,
      source,
      publishedAt: e.published ?? e.updated,
      author: e.author,
      thumbnail: e.image,
    });
    const media = mediaThumbnail(e);
    if (media) it.thumbnail = media;
    // Some feeds (VGC, other WordPress) put the lead image in the description
    // HTML rather than a media tag.
    if (!it.thumbnail) {
      const m = imgSrcRe.exec(e.description + e.content);
      if (m) it.thumbnail = m[1];
    }
    if (this.summaries) it.summary = summarize(e.description || e.content);

    // A Reddit home-feed RSS mixes many subreddits; surface which one from the
    // permalink (only reddit.com links match, so other feeds are untouched).
    const m = redditPathRe.exec(it.url);
    if (m) it.source = "r/" + m[1];
    return it;
  }
}

// dedupe drops repeats of an item already listed — a story filed under two
// sections shows up in both sections' feeds (La Presse's manchettes and
// actualités do this), with the same GUID but different tracking params.
function dedupe(items: Item[]): Item[] {
  const seen = new Set<string>();
  return items.filter((it) => {
    if (it.id !== "" && seen.has(it.id)) return false;
    seen.add(it.id);
    return true;
  });
}

const time = (it: Item) => it.publishedAt?.getTime() ?? -Infinity;

function sortByDate(items: Item[]) {
  items.sort((a, b) => time(b) - time(a)); // stable
}

// roundRobin takes the 1st item of each feed, then the 2nd of each, and so on.
function roundRobin(feeds: Item[][]): Item[] {
  const out: Item[] = [];
  const longest = Math.max(...feeds.map((f) => f.length));
  for (let i = 0; i < longest; i++) {
    for (const f of feeds) if (i < f.length) out.push(f[i]);
  }
  return out;
}

const redditPathRe = /(?:^|\.)reddit\.com\/r\/([A-Za-z0-9_]+)\//;
const imgSrcRe = /<img[^>]+src=["']([^"']+)["']/;
const redditBoiler = /submitted by\s+\/u\/\S+\s+to\s+r\/\S+/i;

// summarize turns feed description/content HTML into a short plain-text excerpt.
export function summarize(raw: string): string {
  if (!raw) return "";
  let txt = decodeHTML(raw.replace(/<[^>]*>/g, " "));
  txt = txt.replace(/\s+/g, " ").trim();
  txt = trimSuffix(trimSuffix(txt, "[link] [comments]").trim(), "[link]").trim();
  if (redditBoiler.test(txt) && byteLen(txt) < 140) return ""; // Reddit link-post boilerplate, no real summary
  return clip(txt, 260);
}

function trimSuffix(s: string, suffix: string): string {
  return s.endsWith(suffix) ? s.slice(0, -suffix.length) : s;
}

// mediaThumbnail digs an image URL out of the Media RSS extension: a bare
// <media:thumbnail> or <media:content> (many news feeds), or the
// <media:group><media:thumbnail> nesting YouTube's per-channel feeds use.
function mediaThumbnail(e: FeedItem): string {
  const m = e.media;
  return m.thumbnail || m.groupThumbnail || m.groupContent || m.content;
}

const enc = new TextEncoder();

export function byteLen(s: string): number {
  return enc.encode(s).length;
}

// clip cuts s to at most max bytes of UTF-8 (the Go version measured in
// bytes), at the last space before the limit, and adds an ellipsis.
export function clip(s: string, max: number): string {
  const bytes = enc.encode(s);
  if (bytes.length <= max) return s;
  // back off to a character boundary
  let end = max;
  while (end > 0 && (bytes[end] & 0xc0) === 0x80) end--;
  let cut = new TextDecoder().decode(bytes.subarray(0, end));
  const sp = cut.lastIndexOf(" ");
  if (sp > 0) cut = cut.slice(0, sp);
  return cut + "…";
}
