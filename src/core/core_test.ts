import { assert, assertEquals, assertThrows } from "@std/assert";
import { join } from "@std/path";
import { App } from "../app.ts";
import { Logger } from "../log.ts";
import { parseDuration, type Provider } from "./provider.ts";
import { Scheduler } from "./scheduler.ts";
import { Store } from "./store.ts";
import { item, type Payload } from "./types.ts";

const quiet = new Logger(() => {});

Deno.test("parseDuration", () => {
  assertEquals(parseDuration("15m"), 15 * 60_000);
  assertEquals(parseDuration("1h30m"), 90 * 60_000);
  assertEquals(parseDuration("1.5h"), 90 * 60_000);
  assertEquals(parseDuration("90s"), 90_000);
  assertEquals(parseDuration("250ms"), 250);
  assertEquals(parseDuration("0"), 0);
  assertThrows(() => parseDuration("15"));
  assertThrows(() => parseDuration("soon"));
});

Deno.test("store: snapshots survive a restart", () => {
  const dir = Deno.makeTempDirSync();
  const a = new Store(dir);
  a.register("w", "W", 0, 1, "rss");
  a.setPayload("w", { items: [item({ id: "1", title: "One", publishedAt: new Date("2026-09-08T12:00:00Z") })] });

  const b = new Store(dir);
  b.register("w", "Renamed", 3, 2, "rss");
  const st = b.snapshot()[0];
  assertEquals([st.title, st.order, st.column], ["Renamed", 3, 2], "identity comes from the config, not the cache");
  assertEquals(st.items[0].title, "One");
  assertEquals(st.items[0].publishedAt?.toISOString(), "2026-09-08T12:00:00.000Z");
  assert(b.fresh("w", 60_000));
});

Deno.test("store: reads a snapshot the Go version wrote", () => {
  const dir = Deno.makeTempDirSync();
  Deno.writeTextFileSync(
    join(dir, "w.json"),
    JSON.stringify({
      key: "w",
      items: [{
        ID: "1",
        Title: "From Go",
        URL: "https://x",
        PublishedAt: "2026-09-08T08:00:00-04:00",
        Thumbnail: "",
        Score: 3,
        CommentsURL: "https://c",
        Hero: false,
      }],
      weather: null,
      standings: { Groups: [{ Name: "A", Columns: ["PTS"], Rows: [{ Rank: 1, Team: "MTL", Values: ["9"] }] }] },
      last_ok: new Date().toISOString(),
      last_err: "",
      last_try: "0001-01-01T00:00:00Z",
    }),
  );
  const s = new Store(dir);
  s.register("w", "W", 0, 1, "rss");
  const st = s.snapshot()[0];
  assertEquals([st.items[0].title, st.items[0].score, st.items[0].commentsUrl], ["From Go", 3, "https://c"]);
  assertEquals(st.items[0].publishedAt?.toISOString(), "2026-09-08T12:00:00.000Z");
  assertEquals(st.standings?.groups[0].rows[0].team, "MTL");
  assertEquals(st.lastTry, null);
});

Deno.test("store: an unchanged refetch doesn't bump the version; retain drops removed widgets", () => {
  const dir = Deno.makeTempDirSync();
  const s = new Store(dir);
  s.register("a", "A", 0, 1, "rss");
  s.register("b", "B", 1, 1, "rss");
  s.setPayload("a", { items: [item({ id: "1" })] });
  const gen = s.gen;
  s.setPayload("a", { items: [item({ id: "1" })] });
  assertEquals(s.gen, gen);
  s.retain(new Set(["a"]));
  assertEquals(s.snapshot().map((x) => x.key), ["a"]);
  assertThrows(() => Deno.statSync(join(dir, "b.json")));
});

function provider(fn: () => Payload | Promise<Payload>): Provider {
  return { fetch: () => Promise.resolve(fn()) };
}

const cfg = (ttl: number, limit = 15) => ({ type: "x", column: 1, title: "", ttl, limit, raw: {} });

Deno.test("scheduler: a failing widget doesn't affect the others", async () => {
  const store = new Store(Deno.makeTempDirSync());
  store.register("good", "Good", 0, 1, "x");
  store.register("bad", "Bad", 1, 1, "x");
  const sched = new Scheduler(store, quiet);
  sched.add(
    "good",
    "Good",
    cfg(3_600_000, 2),
    provider(() => ({
      items: [item({ id: "1", source: "good" }), item({ id: "2", author: "Me", source: "Me" }), item({ id: "3" })],
    })),
  );
  sched.add(
    "bad",
    "Bad",
    cfg(3_600_000),
    provider(() => {
      throw new Error("down");
    }),
  );

  const ctl = new AbortController();
  const done = sched.run(ctl.signal);
  await new Promise((r) => setTimeout(r, 300));
  ctl.abort();
  await done;

  const byKey = new Map(store.snapshot().map((s) => [s.key, s]));
  const good = byKey.get("good")!;
  assertEquals(good.items.length, 2, "limit applied");
  assertEquals(good.items[0].source, "", "a source equal to the widget title is trimmed");
  assertEquals(good.items[1].author, "", "an author equal to the source is trimmed");
  assertEquals(byKey.get("bad")!.lastErr, "down");
});

Deno.test("scheduler: refresh serializes with the schedule and reports unknown keys", async () => {
  const store = new Store(Deno.makeTempDirSync());
  store.register("w", "W", 0, 1, "x");
  let running = 0, overlap = false, calls = 0;
  const sched = new Scheduler(store, quiet);
  sched.add(
    "w",
    "W",
    cfg(3_600_000),
    provider(async () => {
      calls++;
      if (running++) overlap = true;
      await new Promise((r) => setTimeout(r, 30));
      running--;
      return { items: [] };
    }),
  );
  const ctl = new AbortController();
  const done = sched.run(ctl.signal);
  assertEquals(await sched.refresh("nope"), false);
  await Promise.all([sched.refresh("w"), sched.refresh("w"), sched.refresh("")]);
  ctl.abort();
  await done;
  assert(!overlap, "fetches of one widget must not overlap");
  assertEquals(calls, 4); // the startup fetch plus three refreshes
});

Deno.test("app: reload swaps the widget set in place and keeps going after a bad edit", async () => {
  const dir = Deno.makeTempDirSync();
  const cfgPath = join(dir, "config.yaml");
  Deno.writeTextFileSync(cfgPath, "widgets:\n  - { type: hackernews, title: HN, ttl: 1h }\n");
  const app = new App(cfgPath, join(dir, "cache"), quiet);
  // no network in tests: every provider is a stub
  for (const t of ["hackernews", "rss"]) app.registry.register(t, () => provider(() => ({ items: [item({ id: t })] })));

  const ctl = new AbortController();
  await app.start(ctl.signal);
  assertEquals(app.srv.meta.pages[0].boxes.flatMap((b) => b.members), ["hn"]);

  Deno.writeTextFileSync(
    cfgPath,
    "widgets:\n  - { type: rss, title: News, feeds: [x] }\n  - { type: rss, title: News, feeds: [y] }\n",
  );
  await app.reloadNow();
  assertEquals(app.srv.meta.pages[0].boxes.flatMap((b) => b.members), ["news", "news-2"]);
  assertEquals(app.store.snapshot().map((s) => s.key).sort(), ["news", "news-2"]);

  Deno.writeTextFileSync(cfgPath, "widgets:\n  - { type: nope }\n");
  let failed = false;
  try {
    await app.reloadNow();
  } catch {
    failed = true;
  }
  assert(failed);
  assertEquals(app.srv.meta.pages[0].boxes.length, 2, "a bad edit keeps the previous generation");
  ctl.abort();
  await new Promise((r) => setTimeout(r, 50));
});
