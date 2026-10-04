// Hacker News stories from the Algolia search API. One request returns points
// and comment counts, which the per-channel RSS feed (hnrss) does not carry —
// so this is the provider for HN, with the generic rss provider left as a
// fallback (plan §11).

import { type Provider, Settings, type WidgetConfig } from "../core/provider.ts";
import { feed, item, type Payload } from "../core/types.ts";
import { getJSON } from "../httpx.ts";

const itemURL = "https://news.ycombinator.com/item?id=";

// newHackerNews is the core.Factory for "hackernews".
export function newHackerNews(cfg: WidgetConfig): Provider {
  const s = Settings.of(cfg);
  return new HackerNews(
    s.str("tags") || "front_page", // Algolia tag filter
    s.str("query"), // optional search term
    cfg.limit > 0 ? cfg.limit : 30,
  );
}

export class HackerNews implements Provider {
  endpoint = "https://hn.algolia.com/api/v1/search";

  constructor(private tags: string, private query: string, private limit: number) {}

  async fetch(signal: AbortSignal): Promise<Payload> {
    const q = new URLSearchParams({ hitsPerPage: String(this.limit), tags: this.tags });
    if (this.query) q.set("query", this.query);
    q.sort();
    const data = await getJSON(`${this.endpoint}?${q}`, signal, { errPrefix: "algolia" });

    const items = [];
    for (const h of data.hits ?? []) {
      if (!h.title) continue;
      const discussion = itemURL + h.objectID;
      items.push(item({
        id: String(h.objectID ?? ""),
        title: h.title,
        url: h.url || discussion, // Ask HN / text posts have no url
        source: "Hacker News",
        author: h.author ?? "",
        publishedAt: new Date((h.created_at_i ?? 0) * 1000),
        score: h.points ?? 0,
        comments: h.num_comments ?? 0,
        commentsUrl: discussion,
        body: h.story_text ?? "",
      }));
    }
    return feed(items);
  }
}
