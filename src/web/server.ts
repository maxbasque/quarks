import { extname, fromFileUrl, join, relative } from "@std/path";
import type { ColumnChoice } from "../config/config.ts";
import { isStale, type Store, type WidgetState } from "../core/store.ts";
import { Reader } from "../reader.ts";
import type { Client as SpotifyClient } from "../spotifyapi.ts";
import { raw, type Safe } from "./html.ts";
import { settingsRoutes, type SettingsState } from "./settings.ts";
import { type BoxVM, indexPage, type IndexVM, readerPage, type ReaderVM, type TabVM } from "./templates.ts";

// Meta is the config-derived context the renderer needs beyond what the Store
// already carries. The app publishes a new one on every config reload.
export interface Meta {
  theme: string;
  ttls: Map<string, number>; // widget key -> ttl (ms), for the stale badge
  pages: Page[]; // shown pages only
  // layout is every named page, shown or not, with its column choices — what
  // the Settings page offers to toggle.
  layout: LayoutPage[];
}

// LayoutPage is one page as Settings sees it.
export interface LayoutPage {
  name: string;
  enabled: boolean;
  columns: ColumnChoice[]; // empty unless the page declares named columns
  maxColumns: number;
}

// Page is one top-level tab: its column layout and cards.
export interface Page {
  name: string;
  columns: number;
  columnWeights: number[];
  boxes: Box[];
}

// Box is one card. members are widget keys; more than one means a tabbed card.
export interface Box {
  column: number;
  order: number;
  title: string;
  members: string[];
}

export type Handler = (req: Request) => Response | Promise<Response>;

// Server renders the dashboard from whatever is currently in the store.
export class Server {
  meta: Meta = { theme: "dark", ttls: new Map(), pages: [], layout: [] };
  #metaGen = 0;
  #rendered: { storeGen: number; metaGen: number; etag: string; body: string } | null = null;
  #reader = new Reader();
  #static: Map<string, { body: Uint8Array<ArrayBuffer>; type: string }>;
  #staticETag: string;
  readonly settings: SettingsState;

  constructor(
    readonly store: Store,
    readonly refresh: ((key: string) => Promise<boolean>) | null, // fetch now, bypassing the schedule
    readonly secretsPath: string, // for the settings page to read/write credentials
    readonly layoutPath: string, // for the settings page to save column choices
    public reloadNow: () => Promise<void>, // re-run the app's config.load -> registry -> scheduler pipeline
    spotify: SpotifyClient,
  ) {
    this.settings = { spotify, pending: null, status: null };
    [this.#static, this.#staticETag] = loadStatic();
  }

  // publish swaps in a new config-derived Meta.
  publish(m: Meta) {
    this.meta = m;
    this.#metaGen++;
  }

  routes(): Handler {
    const settings = settingsRoutes(this);
    const exact: Record<string, Handler> = {
      "/manifest.webmanifest": (r) => this.#handleManifest(r),
      "/open": (r) => this.#handleOpen(r),
      "/refresh": (r) => this.#handleRefresh(r),
      "/reader": (r) => this.#handleReader(r),
      "/": (r) => this.indexHTML(r),
      ...settings,
    };
    const mux: Handler = (req) => {
      const path = new URL(req.url).pathname;
      if (path.startsWith("/static/")) return this.#handleStatic(req, path);
      const h = exact[path];
      return h ? h(req) : httpError("404 page not found", 404);
    };
    // The server has no auth — it's only safe because nothing but the user's
    // own browser should reach it. Any web page the user visits can still aim
    // requests at 127.0.0.1, so: refuse cross-site POSTs (a hidden form
    // rewriting secrets.yaml), and refuse Host names other than localhost or
    // an IP literal (DNS rebinding — evil.example resolving to 127.0.0.1 so
    // its scripts can read the dashboard, or use /reader to fetch LAN pages).
    return checkHost(crossOriginProtection(mux));
  }

  #handleStatic(req: Request, path: string): Response {
    const f = this.#static.get(path.slice("/static/".length));
    if (!f || (req.method !== "GET" && req.method !== "HEAD")) return httpError("404 page not found", 404);
    // revalidate on every load (cheap: a 304 when the ETag matches) instead of
    // serving a stale copy after a CSS/JS change
    const headers = { "Cache-Control": "no-cache", ETag: this.#staticETag };
    if (etagMatches(req, this.#staticETag)) return new Response(null, { status: 304, headers });
    return new Response(req.method === "HEAD" ? null : f.body, {
      headers: { ...headers, "Content-Type": f.type, "Content-Length": String(f.body.length) },
    });
  }

  #handleManifest(_req: Request): Response {
    const f = this.#static.get("manifest.webmanifest");
    if (!f) return httpError("manifest missing", 500);
    return new Response(f.body, {
      headers: { "Content-Type": "application/manifest+json", "Cache-Control": "no-cache" },
    });
  }

  // handleOpen hands a URL to the OS default browser. The native windows
  // (quarks-window on Linux, the macOS app) can't open new windows, so their
  // injected script sends off-site links here; app.js does the same when the
  // page runs as an installed web app.
  async #handleOpen(req: Request): Promise<Response> {
    if (req.method !== "POST") return httpError("POST only", 405);
    const target = (await formValues(req)).get("url") ?? "";
    let u: URL;
    try {
      u = new URL(target);
    } catch {
      return httpError("bad url", 400);
    }
    if (u.protocol !== "http:" && u.protocol !== "https:") return httpError("bad url", 400);
    try {
      openInBrowser(u.toString());
    } catch (err) {
      return httpError((err as Error).message, 500);
    }
    return new Response(null, { status: 204 });
  }

  // handleRefresh fetches a widget (?key=…) or all widgets (no key)
  // immediately. It waits for the fetch, so the client can reload right after.
  async #handleRefresh(req: Request): Promise<Response> {
    if (req.method !== "POST") return httpError("POST only", 405);
    if (!this.refresh) return httpError("unavailable", 503);
    const key = (await formValues(req)).get("key") ?? "";
    if (!(await this.refresh(key))) return httpError("unknown widget", 404);
    return new Response(null, { status: 204 });
  }

  async #handleReader(req: Request): Promise<Response> {
    const target = new URL(req.url).searchParams.get("url") ?? "";
    if (!target) return httpError("missing url", 400);
    const vm: ReaderVM = { article: null, err: "", url: target };
    try {
      vm.article = await this.#reader.get(target, AbortSignal.timeout(25_000));
    } catch (err) {
      vm.err = (err as Error).message;
    }
    return new Response(readerPage(vm), { headers: { "Content-Type": "text/html; charset=utf-8" } });
  }

  // indexHTML serves the current rendered "/", rebuilding it only when the
  // store or the config has changed since the last render, so repeated polls
  // of an unchanged dashboard cost a version compare.
  indexHTML(req: Request): Response {
    const sg = this.store.gen, mg = this.#metaGen;
    let ri = this.#rendered;
    if (!ri || ri.storeGen !== sg || ri.metaGen !== mg) {
      let body: string;
      try {
        body = indexPage(this.#buildIndexVM());
      } catch (err) {
        body = "render error: " + (err as Error).message;
      }
      ri = { storeGen: sg, metaGen: mg, etag: `"${etagOf(body)}"`, body };
      this.#rendered = ri;
    }
    const headers = { "Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-cache", ETag: ri.etag };
    if ((req.headers.get("if-none-match") ?? "").includes(ri.etag)) {
      return new Response(null, { status: 304, headers });
    }
    return new Response(ri.body, { headers });
  }

  #buildIndexVM(): IndexVM {
    const meta = this.meta;
    const byKey = new Map<string, WidgetState>(this.store.snapshot().map((s) => [s.key, s]));

    const vm: IndexVM = { theme: meta.theme, multiPage: meta.pages.length > 1, pages: [] };
    meta.pages.forEach((pg, pi) => {
      const n = Math.max(pg.columns, 1);
      const cols: BoxVM[][] = Array.from({ length: n }, () => []);
      const boxes = [...pg.boxes].sort((a, b) => a.order - b.order);

      for (const b of boxes) {
        const bv: BoxVM = { title: b.title, tabs: [], tabbed: false, danger: false };
        for (const key of b.members) {
          const st = byKey.get(key);
          const tv: TabVM = {
            key,
            title: st?.title ?? "",
            items: st?.items ?? [], // already trimmed by the scheduler
            weather: st?.weather ?? null,
            standings: st?.standings ?? null,
            badge: "",
            fresh: freshLabel(st?.lastOk ?? null),
            danger: false,
          };
          const ttl = meta.ttls.get(key) ?? 0;
          if (st) {
            if (st.items.length === 0 && !st.weather && !st.standings && st.lastErr) {
              tv.badge = "hors ligne";
              tv.danger = true;
            } else if (st.lastErr) {
              tv.badge = "en retard · " + compactSince(st.lastOk);
            } else if (ttl > 0 && isStale(st, ttl * 2)) {
              tv.badge = "en retard · " + compactSince(st.lastOk);
            }
          }
          bv.danger ||= tv.danger;
          bv.tabs.push(tv);
        }
        bv.tabbed = bv.tabs.length > 1;
        let ci = b.column - 1;
        if (ci < 0 || ci >= n) ci = 0;
        cols[ci].push(bv);
      }

      const name = pg.name || "Accueil";
      vm.pages.push({ name, slug: pageSlug(name, pi), gridCols: gridColumns(n, pg.columnWeights), columns: cols });
    });
    return vm;
  }
}

// gridColumns builds the grid-template-columns value from the column count
// and optional per-column weights. The output is derived only from an int and
// parsed numbers, so it is safe as trusted CSS.
export function gridColumns(n: number, weights: number[]): Safe {
  if (weights.length === n) {
    return raw(weights.map((w) => String(w > 0 ? w : 1) + "fr").join(" "));
  }
  return raw(`repeat(${n}, 1fr)`);
}

function pageSlug(name: string, i: number): string {
  const s = name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
  return s || "p" + i;
}

function freshLabel(t: Date | null): string {
  if (!t) return "jamais mis à jour";
  if (Date.now() - t.getTime() < 60_000) return "mis à jour à l'instant";
  return "mis à jour il y a " + compactSince(t);
}

// compactSince is a short "3 min" / "5 h" / "2 j" duration.
function compactSince(t: Date | null): string {
  // a zero time is "very long ago", as in Go
  const d = t ? Date.now() - t.getTime() : Date.now() + 62135596800000;
  if (d < 60_000) return "0 min";
  if (d < 3_600_000) return Math.floor(d / 60_000) + " min";
  if (d < 86_400_000) return Math.floor(d / 3_600_000) + " h";
  return Math.floor(d / 86_400_000) + " j";
}

// ---- HTTP plumbing -----------------------------------------------------------

export function httpError(msg: string, status: number): Response {
  return new Response(msg + "\n", {
    status,
    headers: { "Content-Type": "text/plain; charset=utf-8", "X-Content-Type-Options": "nosniff" },
  });
}

export function redirect(location: string, status: 302 | 303): Response {
  return new Response(null, { status, headers: { Location: location } });
}

// formValues merges a request's query string with its urlencoded or
// multipart body, the body winning — what Go's r.FormValue read.
export async function formValues(
  req: Request,
): Promise<{ get(k: string): string | undefined; all(k: string): string[] }> {
  const q = new URL(req.url).searchParams;
  let body: FormData | null = null;
  const ct = req.headers.get("content-type") ?? "";
  if (
    req.method === "POST" && (ct.includes("application/x-www-form-urlencoded") || ct.includes("multipart/form-data"))
  ) {
    try {
      body = await req.formData();
    } catch {
      body = null;
    }
  }
  const all = (k: string): string[] => [
    ...(body ? body.getAll(k).filter((v): v is string => typeof v === "string") : []),
    ...q.getAll(k),
  ];
  return { get: (k) => all(k)[0], all };
}

function checkHost(next: Handler): Handler {
  return (req) => {
    let host = req.headers.get("host") ?? new URL(req.url).host;
    const m = /^(\[[^\]]*\]|[^:]*)(?::\d*)?$/.exec(host);
    if (m) host = m[1];
    host = host.replace(/^\[/, "").replace(/\]$/, "");
    if (host !== "localhost" && !isIP(host)) return httpError("unrecognized host", 403);
    return next(req);
  };
}

function isIP(h: string): boolean {
  if (/^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.test(h)) return h.split(".").every((p) => +p <= 255);
  if (!h.includes(":")) return false;
  try {
    new URL(`http://[${h}]/`);
    return true;
  } catch {
    return false;
  }
}

// crossOriginProtection refuses state-changing cross-site requests, the same
// checks as Go's http.CrossOriginProtection: Sec-Fetch-Site when the browser
// sends it, else an Origin that doesn't match the Host.
function crossOriginProtection(next: Handler): Handler {
  return (req) => {
    if (["GET", "HEAD", "OPTIONS"].includes(req.method)) return next(req);
    const site = req.headers.get("sec-fetch-site") ?? "";
    if (site === "same-origin" || site === "none") return next(req);
    if (site !== "") return httpError("cross-origin request detected from Sec-Fetch-Site header", 403);
    const origin = req.headers.get("origin") ?? "";
    if (origin === "") return next(req);
    try {
      if (new URL(origin).host === (req.headers.get("host") ?? new URL(req.url).host)) return next(req);
    } catch { /* unparseable origin: refuse */ }
    return httpError(
      "cross-origin request detected, and/or browser is out of date: " +
        "Sec-Fetch-Site is missing, and Origin does not match Host",
      403,
    );
  };
}

function etagMatches(req: Request, etag: string): boolean {
  const inm = req.headers.get("if-none-match");
  if (!inm) return false;
  return inm.trim() === "*" || inm.split(",").some((t) => t.trim().replace(/^W\//, "") === etag);
}

function etagOf(s: string): string {
  // two 32-bit FNV-style lanes plus the length: a cheap synchronous hash is
  // all an ETag needs
  let h1 = 0x811c9dc5, h2 = 0x01000193 ^ s.length;
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i);
    h1 = Math.imul(h1 ^ c, 0x01000193);
    h2 = Math.imul(h2 ^ c, 0x5bd1e995);
  }
  return (h1 >>> 0).toString(16).padStart(8, "0") + (h2 >>> 0).toString(16).padStart(8, "0") +
    s.length.toString(16);
}

const contentTypes: Record<string, string> = {
  ".css": "text/css; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".woff2": "font/woff2",
  ".webmanifest": "application/manifest+json",
  ".txt": "text/plain; charset=utf-8",
};

// loadStatic reads the static tree into memory (it ships inside the compiled
// binary via --include) and computes one content hash over all of it. The
// files only change on a rebuild, and they change together, so a single
// shared ETag is enough for the browser to know when to drop its cache.
function loadStatic(): [Map<string, { body: Uint8Array<ArrayBuffer>; type: string }>, string] {
  const root = fromFileUrl(new URL("./static/", import.meta.url));
  const files = new Map<string, { body: Uint8Array<ArrayBuffer>; type: string }>();
  const walk = (dir: string) => {
    for (const e of [...Deno.readDirSync(dir)].sort((a, b) => (a.name < b.name ? -1 : 1))) {
      const p = join(dir, e.name);
      if (e.isDirectory) walk(p);
      else if (e.isFile) {
        const rel = relative(root, p).replaceAll("\\", "/");
        files.set(rel, {
          body: Deno.readFileSync(p),
          type: contentTypes[extname(p)] ?? "application/octet-stream",
        });
      }
    }
  };
  walk(root);
  let all = "";
  for (const [p, f] of files) all += p + f.body.length + etagOf(new TextDecoder("latin1").decode(f.body));
  return [files, `"${etagOf(all)}"`];
}

// openInBrowser launches url in the OS default browser.
export function openInBrowser(url: string) {
  const cmd = Deno.build.os === "darwin"
    ? new Deno.Command("open", { args: [url] })
    : Deno.build.os === "windows"
    ? new Deno.Command("rundll32", { args: ["url.dll,FileProtocolHandler", url] })
    : new Deno.Command("xdg-open", { args: [url] });
  try {
    cmd.spawn().status.catch(() => {});
  } catch (err) {
    throw new Error(`open ${JSON.stringify(url)}: ${(err as Error).message}`);
  }
}
