// Public subreddit listings from reddit.com/r/<subs>.json.
//
// This is the *fragile* Reddit path — it carries scores and comment counts,
// but the .json endpoint is frequently blocked outside a residential IP. For
// your personalized home feed use a private RSS feed URL through the generic
// rss provider instead (plan §9); that path is not rate-limited and needs no
// code.

import { type Provider, Settings, type WidgetConfig } from "../core/provider.ts";
import { feed, item, type Payload } from "../core/types.ts";
import { readLimited, userAgent } from "../httpx.ts";

// newReddit is the core.Factory for "reddit".
export function newReddit(cfg: WidgetConfig): Provider {
  const s = Settings.of(cfg);
  const subs = s.strList("subreddits");
  if (subs.length === 0) throw new Error(`reddit widget ${JSON.stringify(cfg.title)}: no subreddits`);
  const sort = s.str("sort") || "hot"; // hot | new | top | rising
  return new Reddit(`/r/${subs.join("+")}/${sort}.json`, cfg.limit > 0 ? cfg.limit : 25);
}

export class Reddit implements Provider {
  base = "https://www.reddit.com"; // scheme+host, overridable in tests

  constructor(private path: string, private limit: number) {}

  async fetch(signal: AbortSignal): Promise<Payload> {
    const resp = await fetch(`${this.base}${this.path}?limit=${this.limit}&raw_json=1`, {
      headers: { "User-Agent": userAgent, Accept: "application/json" },
      signal,
    });
    const body = new TextDecoder().decode(await readLimited(resp, 4 << 20));
    if (resp.status !== 200 || !looksLikeJSON(resp.headers.get("content-type") ?? "", body)) {
      throw new Error(
        `reddit returned ${resp.status} and not JSON — the public .json endpoint is often blocked ` +
          "outside a residential IP; use a private RSS feed instead (plan §9)",
      );
    }

    const l = JSON.parse(body);
    const items = [];
    for (const c of l?.data?.children ?? []) {
      const d = c.data ?? {};
      if (d.stickied || !d.title) continue;
      const permalink = "https://www.reddit.com" + (d.permalink ?? "");
      const it = item({
        id: d.id ?? "",
        title: d.title,
        url: d.is_self || !d.url ? permalink : d.url,
        source: d.subreddit_name_prefixed ?? "",
        author: "u/" + (d.author ?? ""),
        publishedAt: new Date(Math.trunc(d.created_utc ?? 0) * 1000),
        score: d.score ?? 0,
        comments: d.num_comments ?? 0,
        commentsUrl: permalink,
      });
      if (typeof d.thumbnail === "string" && d.thumbnail.startsWith("http")) it.thumbnail = d.thumbnail;
      items.push(it);
    }
    return feed(items);
  }
}

function looksLikeJSON(contentType: string, body: string): boolean {
  if (contentType.includes("json")) return true;
  const b = body.trim();
  return b.startsWith("{") || b.startsWith("[");
}
