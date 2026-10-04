// Wires config, providers, the scheduler, the store and the web server into a
// running dashboard, and reloads the widget set in place when the config file
// changes.

import { load } from "./config/config.ts";
import { layoutPath } from "./config/layout.ts";
import { secretsPath } from "./config/secrets.ts";
import { Registry, type WidgetConfig } from "./core/provider.ts";
import { Scheduler } from "./core/scheduler.ts";
import { errorMessage, Store } from "./core/store.ts";
import type { Logger } from "./log.ts";
import { newF1 } from "./providers/f1.ts";
import { newHackerNews } from "./providers/hackernews.ts";
import { newNHL } from "./providers/nhl.ts";
import { newOnThisDay } from "./providers/onthisday.ts";
import { newPOTD } from "./providers/potd.ts";
import { newReddit } from "./providers/reddit.ts";
import { newRSS } from "./providers/rss/rss.ts";
import { newSpotify } from "./providers/spotify/spotify.ts";
import { newStandings } from "./providers/standings.ts";
import { newWeather } from "./providers/weather.ts";
import { newYouTube } from "./providers/youtube.ts";
import { Client as SpotifyClient } from "./spotifyapi.ts";
import type { Box, LayoutPage, Page } from "./web/server.ts";
import { Server } from "./web/server.ts";

// Runtime is one generation of the scheduler — abort it and await done to
// stop it fully before starting the next.
interface Runtime {
  abort: AbortController;
  done: Promise<void>;
  sched: Scheduler;
}

// InitialConfigError marks a start that failed because the config couldn't be
// loaded — a YAML error, or a widget this version doesn't know.
export class InitialConfigError extends Error {
  constructor(cause: unknown) {
    super(`initial config: ${errorMessage(cause)}`, { cause });
  }
}

export class App {
  readonly registry = new Registry();
  readonly store: Store;
  readonly srv: Server;

  #current: Runtime | null = null;
  // set once start runs; reloadNow needs it to tie a reload-triggered
  // scheduler generation to the app's real shutdown signal
  #signal: AbortSignal | null = null;
  #lastMod = 0;
  #reloading: Promise<void> = Promise.resolve();

  constructor(readonly cfgPath: string, cacheDir: string, readonly log: Logger) {
    this.store = new Store(cacheDir);

    const reg = this.registry;
    reg.register("rss", newRSS);
    reg.register("hackernews", newHackerNews);
    reg.register("weather", newWeather);
    reg.register("reddit", newReddit);
    reg.register("youtube", newYouTube);
    reg.register("nhl", newNHL);
    reg.register("standings", newStandings);
    reg.register("onthisday", newOnThisDay);
    reg.register("potd", newPOTD);
    reg.register("spotify", newSpotify);
    reg.register("f1", newF1);

    this.srv = new Server(
      this.store,
      (key) => this.refresh(key),
      secretsPath(cfgPath),
      layoutPath(cfgPath),
      () => this.reloadNow(),
      new SpotifyClient(),
    );
  }

  // refresh fetches widget key (or all, if key is empty) immediately,
  // bypassing the schedule. Resolves once done, with whether any widget
  // matched.
  refresh(key: string): Promise<boolean> {
    return this.#current ? this.#current.sched.refresh(key) : Promise.resolve(false);
  }

  // run loads the config, starts the HTTP server and the config watcher, and
  // resolves once signal is aborted and the server has shut down.
  async run(signal: AbortSignal, hostname: string, port: number) {
    await this.start(signal);
    await this.serve(signal, hostname, port);
  }

  // start loads the config and starts the scheduler and the config watcher.
  // run does this itself; the macOS app calls it separately so it can offer
  // to reset a broken config before it opens a window. A failed start can be
  // retried.
  async start(signal: AbortSignal) {
    this.#signal = signal;
    try {
      await this.#reload(signal);
    } catch (err) {
      throw new InitialConfigError(err);
    }
    this.#lastMod = this.#watchStamp();
    this.#watch(signal);
  }

  // serve serves the dashboard on hostname:port until signal is aborted. Call
  // start first. If the preferred port is busy and fallback is set, any free
  // port is used. onListen gets the address actually bound.
  async serve(
    signal: AbortSignal,
    hostname: string,
    port: number,
    opts: { fallback?: boolean; onListen?: (addr: Deno.NetAddr) => void } = {},
  ) {
    const handler = this.srv.routes();
    const listen = (port: number) =>
      Deno.serve({
        hostname,
        port,
        signal,
        onListen: (addr) => {
          this.log.info("quarks listening", { addr: `http://${addr.hostname}:${addr.port}` });
          opts.onListen?.(addr);
        },
        onError: (err) => {
          this.log.error("request failed", { err });
          return new Response("internal error\n", { status: 500 });
        },
      }, handler);
    let server: Deno.HttpServer;
    try {
      server = listen(port);
    } catch (err) {
      if (!opts.fallback || !(err instanceof Deno.errors.AddrInUse)) throw err;
      server = listen(0);
    }
    await server.finished;
  }

  // watch polls the config, secrets and layout files' mtimes and reloads on
  // change. A reload that fails to parse is logged and the previous
  // generation keeps running.
  #watch(signal: AbortSignal) {
    const t = setInterval(async () => {
      const mod = this.#watchStamp();
      if (mod === this.#lastMod || mod === 0) return;
      this.#lastMod = mod;
      this.log.info("config changed, reloading", { path: this.cfgPath });
      try {
        await this.#reload(signal);
      } catch (err) {
        this.log.error("reload failed, keeping previous config", { err });
      }
    }, 2000);
    signal.addEventListener("abort", () => clearInterval(t), { once: true });
  }

  // watchStamp is the newest mtime across the config file and the secrets and
  // layout files beside it. Zero if the config file is unreadable.
  #watchStamp(): number {
    let newest: number;
    try {
      newest = Deno.statSync(this.cfgPath).mtime?.getTime() ?? 0;
    } catch {
      return 0;
    }
    for (const p of [secretsPath(this.cfgPath), layoutPath(this.cfgPath)]) {
      try {
        newest = Math.max(newest, Deno.statSync(p).mtime?.getTime() ?? 0);
      } catch { /* optional file */ }
    }
    return newest;
  }

  // reloadNow re-runs the config->registry->scheduler pipeline immediately,
  // outside the 2s file-watch poll — used after the settings page writes
  // secrets.yaml or layout.yaml, so a change takes effect without waiting on
  // the poll. It reuses start's lifetime signal, so the new scheduler
  // generation is still torn down on shutdown like every other reload.
  async reloadNow() {
    if (!this.#signal) throw new Error("app not running yet");
    await this.#reload(this.#signal);
    this.#lastMod = this.#watchStamp();
  }

  // reload is serialized: a settings write and the file watcher can both ask
  // for one at once, and generations must replace each other in order.
  #reload(signal: AbortSignal): Promise<void> {
    const run = this.#reloading.then(() => this.#reloadOnce(signal));
    this.#reloading = run.catch(() => {});
    return run;
  }

  async #reloadOnce(signal: AbortSignal) {
    const cfg = load(this.cfgPath);

    const sched = new Scheduler(this.store, this.log);
    const ttls = new Map<string, number>();
    const keep = new Set<string>();
    const seen = new Set<string>();
    const pages: Page[] = [];
    const layout: LayoutPage[] = [];
    let order = 0;

    for (const pg of cfg.pages) {
      if (pg.name !== "") { // the legacy unnamed single page can't be toggled
        layout.push({ name: pg.name, enabled: pg.enabled, columns: pg.choices, maxColumns: pg.maxColumns });
      }
      if (!pg.enabled) continue; // listed in Settings, but not shown or fetched
      const boxes: Box[] = [];
      pg.boxes.forEach((box, bi) => {
        const members: string[] = [];
        for (const wc of box.widgets) {
          const key = widgetKey(wc, seen);
          let provider;
          try {
            provider = this.registry.build(wc);
          } catch (err) {
            throw new Error(`page ${JSON.stringify(pg.name)}, box ${bi} (${wc.type}): ${errorMessage(err)}`);
          }
          const title = displayTitle(wc);
          this.store.register(key, title, order, wc.column, wc.type);
          sched.add(key, title, wc, provider);
          ttls.set(key, wc.ttl);
          keep.add(key);
          members.push(key);
          order++;
        }
        boxes.push({ column: box.column, order: bi, title: box.title, members });
      });
      pages.push({ name: pg.name, columns: pg.columns, columnWeights: pg.columnWeights, boxes });
    }
    this.store.retain(keep);

    const abort = new AbortController();
    const onParentAbort = () => abort.abort();
    signal.addEventListener("abort", onParentAbort, { once: true });
    const done = sched.run(abort.signal).finally(() => signal.removeEventListener("abort", onParentAbort));

    const old = this.#current;
    this.#current = { abort, done, sched };
    if (old) {
      old.abort.abort();
      const stopped = await Promise.race([
        old.done.then(() => true),
        new Promise<boolean>((r) => setTimeout(() => r(false), 5000)),
      ]);
      if (!stopped) this.log.warn("previous scheduler slow to stop");
    }

    this.srv.publish({ theme: cfg.window.theme, ttls, pages, layout });
    this.log.info("config loaded", { pages: pages.length, widgets: keep.size });
  }
}

// widgetKey is a stable identifier for a widget instance — the disk-cache
// filename and the store map key. Derived from the title so reordering widgets
// in the config keeps caches intact.
export function widgetKey(wc: WidgetConfig, seen: Set<string>): string {
  const base = wc.title.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "") || wc.type;
  let key = base;
  for (let n = 2; seen.has(key); n++) key = `${base}-${n}`;
  seen.add(key);
  return key;
}

function displayTitle(wc: WidgetConfig): string {
  return wc.title || wc.type.slice(0, 1).toUpperCase() + wc.type.slice(1);
}
