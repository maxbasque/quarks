// Wikimedia Commons' "Picture of the Day" — one large image with its caption
// and credit. It reads the keyless REST "featured" feed (same endpoint family
// as onthisday).

import { decodeHTML } from "entities";
import { type Provider, Settings, type WidgetConfig } from "../core/provider.ts";
import { feed, item, type Payload } from "../core/types.ts";
import { getJSON } from "../httpx.ts";
import { clip as clipBytes } from "./rss/rss.ts";

// thumbWidth is the width we ask Wikimedia to render the lead image at. The
// feed hands back a ~640px thumbnail URL; bumping the width keeps it crisp on a
// wide screen without ever pulling the multi-megabyte original.
const thumbWidth = 1280;

// newPOTD is the core.Factory for "potd".
export function newPOTD(cfg: WidgetConfig): Provider {
  const s = Settings.of(cfg);
  return new POTD(s.str("lang").trim().toLowerCase() || "fr"); // caption language / wiki
}

const pad2 = (n: number) => String(n).padStart(2, "0");

export class POTD implements Provider {
  base = "https://%s.wikipedia.org/api/rest_v1/feed/featured"; // overridable for tests
  now = () => new Date(); // overridable for tests

  constructor(private lang: string) {}

  async fetch(signal: AbortSignal): Promise<Payload> {
    // Today's picture may not be posted yet, and a server clock ahead of UTC
    // can ask for a date the feed rejects — fall back to yesterday once.
    const day = this.now();
    let img;
    try {
      img = await this.#fetchDay(day, signal);
    } catch (err) {
      const yesterday = new Date(day);
      yesterday.setDate(yesterday.getDate() - 1);
      try {
        img = await this.#fetchDay(yesterday, signal);
      } catch {
        throw err;
      }
    }

    const caption = this.#caption(img);
    const title = caption || cleanFileTitle(img.title ?? "");

    const it = item({
      id: img.title ?? "",
      title,
      url: img.file_page ?? "",
      source: "Wikimedia Commons",
      author: stripTags(firstNonEmpty(img.artist?.text ?? "", img.credit?.text ?? "")),
      thumbnail: upscale(img.thumbnail?.source ?? ""),
      hero: true,
    });
    const lic = String(img.license?.type ?? "").trim();
    if (lic) it.author = it.author ? `${it.author} · ${lic}` : lic;
    if (caption && caption !== title) it.summary = caption;
    return feed([it]);
  }

  async #fetchDay(d: Date, signal: AbortSignal): Promise<any> {
    const ymd = `${d.getFullYear()}/${pad2(d.getMonth() + 1)}/${pad2(d.getDate())}`;
    const url = this.base.replace("%s", this.lang) + "/" + ymd;
    const data = await getJSON(url, signal, { accept: true, errPrefix: "wikimedia featured" });
    if (!data?.image?.thumbnail?.source) throw new Error(`no picture of the day for ${ymd.replaceAll("/", "-")}`);
    return data.image;
  }

  // caption picks the best available description: a structured caption in the
  // widget's language, then English, then the free-text description.
  #caption(img: any): string {
    const caps = img.structured?.captions ?? {};
    const c = String(caps[this.lang] ?? "").trim() || String(caps.en ?? "").trim();
    return clip(c || (img.description?.text ?? ""), 240);
  }
}

// upscale rewrites a Wikimedia thumbnail URL to a wider render. Wikimedia
// serves arbitrary widths on demand and never upscales past the original, so a
// no-match or an already-small original just falls through unchanged.
export function upscale(src: string): string {
  return src.replace(/\/(\d+)px-/g, `/${thumbWidth}px-`);
}

export function cleanFileTitle(s: string): string {
  if (s.startsWith("File:")) s = s.slice(5);
  return s.replace(/\.[A-Za-z0-9]+$/, "").replaceAll("_", " ").trim();
}

function stripTags(s: string): string {
  return decodeHTML(s.replace(/<[^>]*>/g, "")).replace(/\s+/g, " ").trim();
}

function clip(s: string, max: number): string {
  return clipBytes(stripTags(s), max);
}

function firstNonEmpty(...vals: string[]): string {
  return vals.find((v) => v.trim() !== "") ?? "";
}
