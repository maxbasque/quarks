// Extracts the main text of an article for inline reading. It is invoked
// lazily when the user opens an item — never during background polling (plan
// §11).

import { Readability } from "@mozilla/readability";
import { parseHTML } from "linkedom";
import sanitizeHtml from "sanitize-html";
import { browserUserAgent, readLimited } from "./httpx.ts";

const cacheTTL = 3_600_000;
const cacheMax = 64;
const maxBodySize = 8 << 20; // 8 MiB

// minReadableChars is the shortest extracted body we treat as an article.
// Below this, readability has almost certainly grabbed a caption or blurb.
const minReadableChars = 250;

// Article is the sanitized, readable form of a page.
export interface Article {
  title: string;
  byline: string;
  siteName: string;
  url: string;
  html: string; // sanitized
  excerpt: string;
}

// The policy follows bluemonday's UGC policy, which the Go version used:
// structural and inline text markup, tables, images and links (forced
// rel="nofollow"), http/https/mailto URLs only. It deliberately does NOT carry
// `class` (or `id`) through. Extracted pages bring their own class names
// (readability wraps its output in `<div class="page">`, sites add "grid",
// "panel", "hero", …) and those collide with the dashboard's own stylesheet —
// e.g. `.page:not(.is-active){display:none}` was blanking every article. The
// reader styles its content by tag, not class.
const policy: sanitizeHtml.IOptions = {
  allowedTags: [
    "address",
    "article",
    "aside",
    "footer",
    "header",
    "hgroup",
    "nav",
    "section",
    "h1",
    "h2",
    "h3",
    "h4",
    "h5",
    "h6",
    "blockquote",
    "dd",
    "div",
    "dl",
    "dt",
    "figcaption",
    "figure",
    "hr",
    "li",
    "ol",
    "p",
    "pre",
    "ul",
    "a",
    "abbr",
    "acronym",
    "b",
    "bdi",
    "bdo",
    "br",
    "cite",
    "code",
    "data",
    "dfn",
    "em",
    "i",
    "kbd",
    "mark",
    "q",
    "rp",
    "rt",
    "ruby",
    "s",
    "samp",
    "small",
    "span",
    "strike",
    "strong",
    "sub",
    "sup",
    "time",
    "tt",
    "u",
    "var",
    "wbr",
    "caption",
    "col",
    "colgroup",
    "table",
    "tbody",
    "td",
    "tfoot",
    "th",
    "thead",
    "tr",
    "img",
    "del",
    "ins",
    "details",
    "summary",
    "map",
    "area",
    "picture",
  ], // deno-fmt-ignore
  allowedAttributes: {
    "*": ["dir", "lang", "title"],
    a: ["href", "rel"],
    img: ["src", "alt", "width", "height"],
    area: ["alt", "coords", "href", "shape"],
    blockquote: ["cite"],
    q: ["cite"],
    del: ["cite", "datetime"],
    ins: ["cite", "datetime"],
    time: ["datetime"],
    data: ["value"],
    ol: ["start", "reversed", "type"],
    li: ["value"],
    td: ["abbr", "colspan", "rowspan", "headers", "scope"],
    th: ["abbr", "colspan", "rowspan", "headers", "scope"],
    col: ["span"],
    colgroup: ["span"],
    details: ["open"],
  },
  allowedSchemes: ["http", "https", "mailto"],
  allowedSchemesAppliedToAttributes: ["href", "src", "cite"],
  allowProtocolRelative: true,
  transformTags: {
    a: (tagName: string, attribs: sanitizeHtml.Attributes) => ({ tagName, attribs: { ...attribs, rel: "nofollow" } }),
  },
};

// errNoContent means the page fetched fine but held no readable prose.
const errNoContent = "aucun texte lisible sur cette page — essayez « ouvrir l’original »";

export class Reader {
  timeout = 20_000;
  #cache = new Map<string, { art: Article; at: number }>();

  // get fetches and extracts rawURL, using a short-lived in-memory cache.
  async get(rawURL: string, signal: AbortSignal): Promise<Article> {
    let u: URL;
    try {
      u = new URL(rawURL);
    } catch {
      throw new Error(`adresse non prise en charge : ${JSON.stringify(rawURL)}`);
    }
    if (u.protocol !== "http:" && u.protocol !== "https:") {
      throw new Error(`adresse non prise en charge : ${JSON.stringify(rawURL)}`);
    }

    const hit = this.#cache.get(rawURL);
    if (hit && Date.now() - hit.at < cacheTTL) return hit.art;

    const resp = await fetch(rawURL, {
      headers: { "User-Agent": browserUserAgent, Accept: "text/html,application/xhtml+xml" },
      signal: AbortSignal.any([signal, AbortSignal.timeout(this.timeout)]),
    });
    if (resp.status !== 200) {
      await resp.body?.cancel();
      throw new Error(`${rawURL} a répondu ${resp.status}`);
    }
    const ct = resp.headers.get("content-type") ?? "";
    if (ct && !ct.includes("html")) {
      await resp.body?.cancel();
      throw new Error(`${rawURL} n’est pas une page web (${ct})`);
    }

    const page = decodePage(await readLimited(resp, maxBodySize), ct);
    let parsed;
    try {
      const { document } = parseHTML(page) as unknown as { document: any };
      // readability resolves relative links against these; linkedom leaves
      // them unset
      const finalURL = resp.url || rawURL;
      Object.defineProperty(document, "baseURI", { value: finalURL });
      Object.defineProperty(document, "documentURI", { value: finalURL });
      parsed = new Readability(document).parse();
    } catch (err) {
      throw new Error(`extraction impossible de ${rawURL} : ${(err as Error).message}`);
    }

    const sanitized = sanitizeHtml(parsed?.content ?? "", policy).trim();
    // Pages with no real prose — audio/video players, paywalls, link hubs —
    // come back with a title but little or no body. Don't render a blank
    // panel; tell the caller so the UI can point at "ouvrir l’original".
    if (!parsed || (parsed.length ?? 0) < minReadableChars || sanitized === "") {
      throw new Error(errNoContent);
    }

    let byline = (parsed.byline ?? "").trim();
    const siteName = (parsed.siteName ?? "").trim();
    if (byline.toLowerCase() === siteName.toLowerCase()) byline = ""; // some sites report the outlet as the byline too

    const art: Article = {
      title: (parsed.title ?? "").trim(),
      byline,
      siteName,
      url: rawURL,
      html: sanitized,
      excerpt: (parsed.excerpt ?? "").trim(),
    };
    if (this.#cache.size >= cacheMax) this.#cache.clear(); // crude eviction; articles are cheap to refetch
    this.#cache.set(rawURL, { art, at: Date.now() });
    return art;
  }
}

// decodePage decodes an HTML body by its declared charset: the Content-Type
// header first, then a <meta> in the first few KB, then UTF-8.
function decodePage(bytes: Uint8Array, contentType: string): string {
  let label = /charset=["']?([\w-]+)/i.exec(contentType)?.[1];
  if (!label) {
    const head = new TextDecoder("latin1").decode(bytes.subarray(0, 4096));
    label = /<meta[^>]+charset=["']?([\w-]+)/i.exec(head)?.[1];
  }
  try {
    return new TextDecoder(label ?? "utf-8").decode(bytes);
  } catch {
    return new TextDecoder().decode(bytes);
  }
}
