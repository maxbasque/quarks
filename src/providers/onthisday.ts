// Wikipedia's "On this day" entries for the current date — historical events,
// births, deaths and holidays. It uses the keyless Wikimedia REST feed, which
// is available for a handful of languages (en, fr, de, sv, pt, ru, es, …).

import { decodeHTML } from "entities";
import { type Provider, Settings, type WidgetConfig } from "../core/provider.ts";
import { feed, type Item, item, type Payload } from "../core/types.ts";
import { getJSON } from "../httpx.ts";
import { clip as clipBytes } from "./rss/rss.ts";

// validTypes are the feed sub-types the REST endpoint accepts. "selected" is
// the curated highlight reel and the sensible default; "all" merges every
// bucket.
const validTypes = new Set(["selected", "events", "births", "deaths", "holidays", "all"]);

// newOnThisDay is the core.Factory for "onthisday".
export function newOnThisDay(cfg: WidgetConfig): Provider {
  const s = Settings.of(cfg);
  const lang = s.str("lang").trim().toLowerCase() || "fr"; // wiki language code
  const kind = s.str("mode").trim().toLowerCase() || "selected";
  if (!validTypes.has(kind)) {
    throw new Error(
      `onthisday widget ${
        JSON.stringify(cfg.title)
      }: mode must be one of selected, events, births, deaths, holidays, all`,
    );
  }
  return new OnThisDay(lang, kind, cfg.limit > 0 ? cfg.limit : 15);
}

const pad2 = (n: number) => String(n).padStart(2, "0");

export class OnThisDay implements Provider {
  base = "https://%s.wikipedia.org/api/rest_v1/feed/onthisday"; // overridable for tests
  now = () => new Date(); // overridable for tests

  constructor(private lang: string, private kind: string, private limit: number) {}

  async fetch(signal: AbortSignal): Promise<Payload> {
    const now = this.now();
    const mm = pad2(now.getMonth() + 1), dd = pad2(now.getDate());
    const url = this.base.replace("%s", this.lang) + `/${this.kind}/${mm}/${dd}`;
    const data = await getJSON(url, signal, { accept: true, errPrefix: "wikimedia" });

    // a single-type request fills one list, the "all" request fills several
    const entries: any[] = [
      ...(data.selected ?? []),
      ...(data.events ?? []),
      ...(data.births ?? []),
      ...(data.deaths ?? []),
      ...(data.holidays ?? []),
    ];
    // Most recent year first — reads like a feed rather than a timeline.
    entries.sort((a, b) => (b.year ?? 0) - (a.year ?? 0));

    let items: Item[] = [];
    for (const e of entries) {
      const text = String(e.text ?? "").trim();
      if (!text) continue;
      const year = e.year ?? 0;
      const it = item({
        id: `otd-${mm}${dd}-${year}-${[...text].slice(0, 60).join("")}`,
        title: year > 0 ? `${year} — ${text}` : text,
        source: "Wikipédia",
        url: `https://${this.lang}.wikipedia.org/wiki/Special:Search?search=${text}`,
      });
      const pg = e.pages?.[0];
      if (pg) {
        const u = pg.content_urls?.desktop?.page;
        if (u) it.url = u;
        else if (pg.title) it.url = `https://${this.lang}.wikipedia.org/wiki/${pg.title}`;
        it.thumbnail = pg.thumbnail?.source ?? "";
        it.summary = clip(pg.extract ?? "", 220);
      }
      items.push(it);
    }
    if (items.length > this.limit) items = items.slice(0, this.limit);
    return feed(items);
  }
}

// clip normalizes an extract to a short single-line plain-text excerpt. The
// REST feed's "extract" is already plain text, but strip tags defensively.
function clip(s: string, max: number): string {
  s = decodeHTML(s.replace(/<[^>]*>/g, "")).replace(/\s+/g, " ").trim();
  return clipBytes(s, max);
}
