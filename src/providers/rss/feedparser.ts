// A feed parser for RSS 2.0, RSS 1.0 (RDF), Atom and JSON Feed, normalizing
// all four into one shape — the subset of gofeed's model the rss provider
// reads: titles, links, ids, dates, authors, descriptions, images, and the
// Media RSS extension.

import { decodeHTML } from "entities";
import { XMLParser } from "fast-xml-parser";

export interface Feed {
  title: string;
  items: FeedItem[];
}

export interface FeedItem {
  title: string;
  link: string;
  guid: string;
  published: Date | null;
  updated: Date | null;
  author: string;
  description: string; // HTML
  content: string; // HTML
  image: string;
  media: Media;
}

// Media holds the Media RSS bits used for thumbnails: bare <media:thumbnail>
// and <media:content>, and the same nested in a <media:group>.
export interface Media {
  thumbnail: string;
  content: string;
  groupThumbnail: string;
  groupContent: string;
}

const NS: Record<string, string> = {
  "http://search.yahoo.com/mrss/": "media",
  "http://search.yahoo.com/mrss": "media",
  "http://purl.org/dc/elements/1.1/": "dc",
  "http://purl.org/rss/1.0/modules/content/": "content",
  "http://www.itunes.com/dtds/podcast-1.0.dtd": "itunes",
  "http://www.w3.org/1999/02/22-rdf-syntax-ns#": "rdf",
};

// raw-HTML elements: kept unparsed so markup inside them (Atom xhtml content,
// a stray unescaped tag in a description) survives as text
const htmlElements = ["description", "summary", "content", "encoded"];

// decode turns feed bytes into text, honouring the XML declaration's encoding.
export function decode(bytes: Uint8Array): string {
  const head = new TextDecoder("latin1").decode(bytes.subarray(0, 200));
  const m = /^\s*<\?xml[^>]*encoding=["']([A-Za-z0-9._-]+)["']/.exec(head.replace(/^﻿/, ""));
  if (m) {
    try {
      return new TextDecoder(m[1]).decode(bytes);
    } catch {
      // unknown label: fall through to UTF-8
    }
  }
  return new TextDecoder().decode(bytes);
}

export function parseFeed(text: string): Feed {
  const t = text.replace(/^﻿/, "").trimStart();
  if (t.startsWith("{")) return parseJSONFeed(t);

  const doc = xmlParser.parse(t) as Record<string, any[]>;
  const rootName = Object.keys(doc).find((k) => !k.startsWith("?") && !k.startsWith("!"));
  if (!rootName) throw new Error("failed to detect feed type");
  const root = doc[rootName][0];
  const prefixes = nsPrefixes(root);
  const local = rootName.replace(/^.*:/, "");

  switch (local) {
    case "rss": {
      const channel = child(root, "channel") ?? {};
      return { title: text1(child(channel, "title")), items: all(channel, "item").map((i) => rssItem(i, prefixes)) };
    }
    case "RDF": {
      const channel = child(root, "channel") ?? {};
      return { title: text1(child(channel, "title")), items: all(root, "item").map((i) => rssItem(i, prefixes)) };
    }
    case "feed":
      return { title: text1(child(root, "title")), items: all(root, "entry").map((e) => atomEntry(e, prefixes)) };
  }
  throw new Error("failed to detect feed type");
}

const xmlParser = new XMLParser({
  ignoreAttributes: false,
  attributeNamePrefix: "@_",
  textNodeName: "#text",
  removeNSPrefix: false,
  parseTagValue: false,
  parseAttributeValue: false,
  trimValues: true,
  processEntities: true,
  htmlEntities: true,
  isArray: (_name, _jpath, _leaf, isAttribute) => !isAttribute,
  stopNodes: htmlElements.flatMap((n) => [`*.${n}`, `*.content:${n}`]),
});

// ---- tree helpers ------------------------------------------------------------

type Prefixes = (canonical: string) => string;

// nsPrefixes maps a canonical prefix ("media") to whatever prefix this
// document declared for that namespace.
function nsPrefixes(root: any): Prefixes {
  const declared: Record<string, string> = {};
  if (root && typeof root === "object") {
    for (const [k, v] of Object.entries(root)) {
      if (!k.startsWith("@_xmlns:")) continue;
      const canonical = NS[String(v)];
      if (canonical) declared[canonical] = k.slice("@_xmlns:".length);
    }
  }
  return (c) => declared[c] ?? c;
}

function all(node: any, name: string): any[] {
  if (!node || typeof node !== "object") return [];
  const v = node[name];
  return Array.isArray(v) ? v : v === undefined ? [] : [v];
}

function child(node: any, name: string): any {
  return all(node, name)[0];
}

function attr(node: any, name: string): string {
  if (!node || typeof node !== "object") return "";
  const v = node["@_" + name];
  return v == null ? "" : String(v);
}

// text1 is an element's text content.
function text1(node: any): string {
  if (node == null) return "";
  if (typeof node === "string" || typeof node === "number") return String(node).trim();
  if (typeof node === "object") {
    let out = node["#text"] != null ? String(node["#text"]) : "";
    for (const [k, v] of Object.entries(node)) {
      if (k === "#text" || k.startsWith("@_")) continue;
      for (const c of Array.isArray(v) ? v : [v]) out += text1(c);
    }
    return out.trim();
  }
  return "";
}

// html is a raw (stop-node) element's content as HTML: CDATA sections are
// taken literally, everything else has its XML escaping undone — except Atom
// xhtml content, which is markup already.
function html(node: any): string {
  if (node == null) return "";
  const raw = typeof node === "object" ? String(node["#text"] ?? "") : String(node);
  if (attr(node, "type") === "xhtml") return raw.trim();
  let out = "";
  let last = 0;
  for (const m of raw.matchAll(/<!\[CDATA\[([\s\S]*?)\]\]>/g)) {
    out += decodeHTML(raw.slice(last, m.index));
    out += m[1];
    last = m.index! + m[0].length;
  }
  out += decodeHTML(raw.slice(last));
  return out.trim();
}

function media(node: any, p: Prefixes): Media {
  const m = p("media");
  const group = child(node, `${m}:group`);
  return {
    thumbnail: attr(child(node, `${m}:thumbnail`), "url"),
    content: attr(child(node, `${m}:content`), "url"),
    groupThumbnail: attr(child(group, `${m}:thumbnail`), "url"),
    groupContent: attr(child(group, `${m}:content`), "url"),
  };
}

// ---- RSS ---------------------------------------------------------------------

function rssItem(i: any, p: Prefixes): FeedItem {
  const dc = p("dc");
  const itunes = p("itunes");
  const enclosure = all(i, "enclosure").find((e) => attr(e, "type").startsWith("image/"));
  return {
    title: text1(child(i, "title")),
    link: text1(child(i, "link")),
    guid: text1(child(i, "guid")),
    published: parseDate(text1(child(i, "pubDate")) || text1(child(i, `${dc}:date`))),
    updated: parseDate(text1(child(i, `${dc}:date`))),
    author: rssAuthor(text1(child(i, "author"))) || text1(child(i, `${dc}:creator`)) ||
      text1(child(i, `${itunes}:author`)),
    description: html(child(i, "description")),
    content: html(child(i, `${p("content")}:encoded`)),
    image: attr(child(i, `${itunes}:image`), "href") || attr(enclosure, "url"),
    media: media(i, p),
  };
}

// rssAuthor pulls the name out of RSS's "email (Name)" author form.
function rssAuthor(s: string): string {
  const m = /^\S+@\S+\s*\((.+)\)$/.exec(s);
  if (m) return m[1].trim();
  return /^\S+@\S+$/.test(s) ? "" : s;
}

// ---- Atom ----------------------------------------------------------------

function atomEntry(e: any, p: Prefixes): FeedItem {
  const links = all(e, "link");
  const link = links.find((l) => !attr(l, "rel") || attr(l, "rel") === "alternate") ?? links[0];
  const authors = all(e, "author");
  const thumbLink = links.find((l) => attr(l, "rel") === "enclosure" && attr(l, "type").startsWith("image/"));
  return {
    title: atomText(child(e, "title")),
    link: attr(link, "href"),
    guid: text1(child(e, "id")),
    published: parseDate(text1(child(e, "published")) || text1(child(e, "issued"))),
    updated: parseDate(text1(child(e, "updated")) || text1(child(e, "modified"))),
    author: authors.length ? text1(child(authors[0], "name")) : "",
    description: html(child(e, "summary")),
    content: html(child(e, "content")),
    image: attr(thumbLink, "href"),
    media: media(e, p),
  };
}

// atomText reads an Atom text construct; type="html" titles carry escaped
// markup, so their tags are dropped.
function atomText(node: any): string {
  const t = text1(node);
  return attr(node, "type") === "html" ? decodeHTML(t.replace(/<[^>]*>/g, "")).trim() : t;
}

// ---- JSON Feed -----------------------------------------------------------------

function parseJSONFeed(text: string): Feed {
  let doc: any;
  try {
    doc = JSON.parse(text);
  } catch {
    throw new Error("failed to detect feed type");
  }
  if (!doc || typeof doc !== "object" || !Array.isArray(doc.items)) throw new Error("failed to detect feed type");
  const str = (v: unknown) => (typeof v === "string" ? v : "");
  return {
    title: str(doc.title),
    items: doc.items.map((it: any): FeedItem => ({
      title: str(it.title),
      link: str(it.url) || str(it.external_url),
      guid: it.id == null ? "" : String(it.id),
      published: parseDate(str(it.date_published)),
      updated: parseDate(str(it.date_modified)),
      author: str(it.authors?.[0]?.name) || str(it.author?.name),
      description: str(it.summary),
      content: str(it.content_html) || str(it.content_text),
      image: str(it.image) || str(it.banner_image),
      media: { thumbnail: "", content: "", groupThumbnail: "", groupContent: "" },
    })),
  };
}

// ---- dates -----------------------------------------------------------------

// parseDate reads the date formats feeds use in practice — RFC 822/1123 and
// RFC 3339, plus their common sloppy variants. Unparseable means null.
export function parseDate(s: string): Date | null {
  s = s.trim();
  if (!s) return null;
  let t = Date.parse(s);
  if (isNaN(t)) {
    // "Mon, 8 Sep 2026 10:15:00 +0000 (UTC)", "2026-09-08 10:15:00 +0000"
    const cleaned = s.replace(/\s*\([^)]*\)\s*$/, "").replace(/^(\d{4}-\d\d-\d\d) (\d)/, "$1T$2")
      .replace(/ ([+-]\d\d):?(\d\d)$/, "$1:$2");
    t = Date.parse(cleaned);
  }
  return isNaN(t) ? null : new Date(t);
}
