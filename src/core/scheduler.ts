import { withTimeout } from "../ctx.ts";
import type { Logger } from "../log.ts";
import type { Provider, WidgetConfig } from "./provider.ts";
import type { Store } from "./store.ts";

// job binds a live provider to its widget key and TTL.
interface Job {
  key: string;
  title: string; // widget display title, for trimming redundant source
  ttl: number;
  limit: number;
  provider: Provider;
  lock: Promise<void>; // serializes a scheduled fetch with a manual refresh
}

// Scheduler runs one loop per widget, each on its own TTL, writing results into
// the Store. A failing widget never affects the others.
export class Scheduler {
  #store: Store;
  #log: Logger;
  #jobs: Job[] = [];
  #signal: AbortSignal | null = null;

  constructor(store: Store, log: Logger) {
    this.#store = store;
    this.#log = log;
  }

  // add registers a widget instance. key must be unique and stable across
  // restarts (it is the disk-cache filename).
  add(key: string, title: string, cfg: WidgetConfig, p: Provider) {
    this.#jobs.push({ key, title, ttl: cfg.ttl, limit: cfg.limit, provider: p, lock: Promise.resolve() });
  }

  // run starts every widget loop and resolves once signal is aborted and every
  // loop has returned. That lets a caller (config reload) know the old
  // scheduler is fully stopped before starting a replacement.
  async run(signal: AbortSignal): Promise<void> {
    this.#signal = signal;
    await Promise.all(this.#jobs.map((j, i) => this.#loop(signal, j, i)));
  }

  // refresh fetches now, outside the schedule. An empty key refreshes every
  // widget (concurrently). It resolves once the fetch(es) finish and reports
  // whether any widget matched.
  async refresh(key: string): Promise<boolean> {
    const signal = this.#signal;
    if (!signal) return false;

    if (key === "") {
      await Promise.all(this.#jobs.map((j) => this.#fetch(signal, j)));
      return this.#jobs.length > 0;
    }
    const j = this.#jobs.find((j) => j.key === key);
    if (!j) return false;
    await this.#fetch(signal, j);
    return true;
  }

  async #loop(signal: AbortSignal, j: Job, idx: number) {
    // Fetch straight away only if the cache (from disk, or a previous config
    // generation) isn't already fresh — so editing the config doesn't restart
    // a fetch storm. Stagger the first fetch a little so a cold start doesn't
    // hit every feed at once.
    if (!this.#store.fresh(j.key, j.ttl)) {
      const d = Math.min(idx, 20) * 150;
      if (d > 0 && !(await sleep(d, signal))) return;
      await this.#fetch(signal, j);
    }
    while (await sleep(j.ttl, signal)) {
      await this.#fetch(signal, j);
    }
  }

  #fetch(signal: AbortSignal, j: Job): Promise<void> {
    const run = j.lock.then(() => this.#fetchNow(signal, j));
    j.lock = run.catch(() => {});
    return run;
  }

  async #fetchNow(signal: AbortSignal, j: Job) {
    if (signal.aborted) return;
    const fsignal = withTimeout(signal, 30_000);
    let payload;
    try {
      payload = await j.provider.fetch(fsignal);
    } catch (err) {
      if (signal.aborted) return; // shutting down or reloading — not a real feed failure
      this.#log.warn("widget fetch failed", { widget: j.key, err });
      this.#store.setError(j.key, err);
      return;
    }
    if (j.limit > 0 && payload.items.length > j.limit) {
      payload.items = payload.items.slice(0, j.limit);
    }
    // Trim redundant labels once here, not on every render.
    for (const it of payload.items) {
      if (equalFold(it.source, j.title)) it.source = "";
      if (equalFold(it.author, it.source)) it.author = "";
    }
    this.#log.info("widget refreshed", { widget: j.key, items: payload.items.length });
    this.#store.setPayload(j.key, payload);
  }
}

export function equalFold(a: string, b: string): boolean {
  return a.toLowerCase() === b.toLowerCase();
}

// sleep waits ms, resolving false early if signal aborts.
export function sleep(ms: number, signal?: AbortSignal): Promise<boolean> {
  return new Promise((resolve) => {
    if (signal?.aborted) return resolve(false);
    const t = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolve(true);
    }, ms);
    const onAbort = () => {
      clearTimeout(t);
      resolve(false);
    };
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}
