import { assert, assertEquals, assertThrows } from "@std/assert";
import { join } from "@std/path";
import { Settings } from "../core/provider.ts";
import { type Config, load } from "./config.ts";
import { layoutPath, saveLayout } from "./layout.ts";

function writeFile(path: string, content: string, mode: number) {
  Deno.writeTextFileSync(path, content, { mode });
  Deno.chmodSync(path, mode);
}

function tempConfig(content: string): string {
  const dir = Deno.makeTempDirSync();
  const p = join(dir, "config.yaml");
  writeFile(p, content, 0o644);
  return p;
}

// feeds reads the "feeds" list of the single widget in box bi of page 0.
function feeds(cfg: Config, bi: number): string[] {
  return new Settings(cfg.pages[0].boxes[bi].widgets[0].raw).strList("feeds");
}

Deno.test("load with secrets and env", () => {
  const cfgPath = tempConfig(`
window:
  columns: 2
widgets:
  - type: rss
    title: Home
    feeds: [ "\${secret:reddit_home}" ]
  - type: rss
    title: Cal
    feeds: [ "\${QUARKS_TEST_CAL}" ]
`);
  writeFile(join(cfgPath, "..", "secrets.yaml"), "reddit_home: https://reddit.example/.rss?feed=TOKEN\n", 0o600);
  Deno.env.set("QUARKS_TEST_CAL", "https://cal.example/x.ics");
  try {
    const cfg = load(cfgPath);
    assertEquals(cfg.pages[0].columns, 2);
    assertEquals(feeds(cfg, 0), ["https://reddit.example/.rss?feed=TOKEN"]);
    assertEquals(feeds(cfg, 1), ["https://cal.example/x.ics"]);
  } finally {
    Deno.env.delete("QUARKS_TEST_CAL");
  }
});

Deno.test("group becomes one box with tabs", () => {
  const cfg = load(tempConfig(`
widgets:
  - type: hackernews
    column: 1
    title: HN
  - type: group
    column: 2
    title: News
    tabs:
      - { type: rss, title: A, feeds: [http://a] }
      - { type: rss, title: B, feeds: [http://b] }
`));
  const boxes = cfg.pages[0].boxes;
  assertEquals(boxes.length, 2);
  assertEquals(boxes[0].widgets.length, 1, "plain widget box should have 1 widget");
  const g = boxes[1];
  assertEquals([g.title, g.widgets.length], ["News", 2]);
  assertEquals([g.column, g.widgets[0].column, g.widgets[1].column], [2, 2, 2], "tabs inherit the group column");
  assertEquals([g.widgets[0].title, g.widgets[1].title], ["A", "B"]);
});

Deno.test("secrets file must not be world-readable", () => {
  const cfgPath = tempConfig("widgets:\n  - type: rss\n    title: X\n    feeds: [http://x]\n");
  writeFile(join(cfgPath, "..", "secrets.yaml"), "k: v\n", 0o644);
  assertThrows(() => load(cfgPath));
});

Deno.test("load without secrets file", () => {
  load(tempConfig("widgets:\n  - type: rss\n    title: X\n    feeds: [http://x]\n"));
});

Deno.test("unresolved token becomes empty", () => {
  const cfg = load(tempConfig(`
widgets:
  - type: rss
    title: X
    source: "\${secret:nope}"
    feeds: [http://x]
`));
  assertEquals(new Settings(cfg.pages[0].boxes[0].widgets[0].raw).str("source"), "");
});

const namedColumnsConfig = `
pages:
  - name: Accueil
    max_columns: 2
    columns:
      - name: Niches
        enabled: false
        weight: 27
        widgets:
          - { type: rss, title: Reddit, feeds: [ "\${secret:reddit_home}" ] }
      - name: Nouvelles
        weight: 46
        widgets:
          - { type: rss, title: RC, feeds: [https://rc.example/rss] }
      - name: Aujourd'hui
        weight: 27
        widgets:
          - { type: weather, title: Météo }
          - { type: potd, title: Photo }
`;

Deno.test("load named columns", () => {
  const pg = load(tempConfig(namedColumnsConfig)).pages[0];
  assertEquals([pg.columns, pg.maxColumns], [2, 2]);
  assertEquals(pg.columnWeights, [46, 27]);
  assertEquals(pg.boxes.length, 3, "disabled column's widgets dropped");
  assertEquals(pg.boxes.map((b) => b.column), [1, 2, 2]);
  assertEquals(pg.boxes[2].widgets[0].column, 2);
  assertEquals(pg.choices, [
    { name: "Niches", enabled: false, missingSecrets: ["reddit_home"] },
    { name: "Nouvelles", enabled: true, missingSecrets: [] },
    { name: "Aujourd'hui", enabled: true, missingSecrets: [] },
  ]);
});

Deno.test("load named columns: layout override", () => {
  const cfgPath = tempConfig(namedColumnsConfig);
  writeFile(join(cfgPath, "..", "secrets.yaml"), "reddit_home: https://reddit.example/.rss\n", 0o600);
  writeFile(layoutPath(cfgPath), "columns:\n  Accueil: {Niches: true, Aujourd'hui: false}\n", 0o644);

  let cfg = load(cfgPath);
  const pg = cfg.pages[0];
  assertEquals([pg.columns, pg.boxes.length], [2, 2]);
  assertEquals(feeds(cfg, 0), ["https://reddit.example/.rss"]);
  assert(pg.choices[0].enabled && !pg.choices[2].enabled && pg.choices[0].missingSecrets.length === 0);

  // over max_columns: the first two (config order) win
  writeFile(layoutPath(cfgPath), "columns:\n  Accueil: {Niches: true, Nouvelles: true, Aujourd'hui: true}\n", 0o644);
  cfg = load(cfgPath);
  assertEquals(cfg.pages[0].choices.map((c) => c.enabled), [true, true, false]);

  // switching every column off falls back to the config's flags
  writeFile(layoutPath(cfgPath), "columns:\n  Accueil: {Niches: false, Nouvelles: false, Aujourd'hui: false}\n", 0o644);
  cfg = load(cfgPath);
  assertEquals(cfg.pages[0].choices.map((c) => c.enabled), [false, true, true]);
});

Deno.test("load example config", () => {
  const cfg = load(tempConfig(Deno.readTextFileSync(new URL("../../config.example.yaml", import.meta.url))));
  // the shipped default must work with no secrets.yaml at all
  for (const pg of cfg.pages) {
    for (const c of pg.choices) {
      assert(!(c.enabled && c.missingSecrets.length > 0), `page ${pg.name} column ${c.name} needs secrets`);
    }
  }
  const pg = cfg.pages[0];
  assertEquals(pg.columns, 2);
  assertEquals(pg.choices[1].name, "Nouvelles");
  assert(pg.choices[1].enabled);
});

const twoPagesConfig = `
pages:
  - name: Accueil
    widgets:
      - { type: rss, title: RC, feeds: [https://rc.example/rss] }
  - name: Sports
    enabled: false
    columns:
      - name: Canadiens
        widgets:
          - { type: nhl, title: Canadiens, team: MTL }
      - name: Scores
        widgets:
          - { type: nhl, title: Scores, mode: scores }
  - name: Media
    widgets:
      - { type: rss, title: M, feeds: [https://m.example/rss] }
`;

const enabledPages = (cfg: Config) => cfg.pages.filter((p) => p.enabled).map((p) => p.name);

Deno.test("page toggles", () => {
  const cfgPath = tempConfig(twoPagesConfig);

  // a hidden page is still parsed, so Settings can list its columns
  let cfg = load(cfgPath);
  assertEquals(enabledPages(cfg), ["Accueil", "Media"]);
  assertEquals([cfg.pages[1].choices.length, cfg.pages[1].columns], [2, 2]);

  saveLayout(layoutPath(cfgPath), {
    pages: { on: { Accueil: false, Sports: true, Media: false } },
    columns: { Sports: { on: { Canadiens: false, Scores: true } } },
  });
  cfg = load(cfgPath);
  assertEquals(enabledPages(cfg), ["Sports"]);
  assertEquals(cfg.pages[1].columns, 1);
  assert(!cfg.pages[1].choices[0].enabled);

  // a page the saved choice doesn't mention (added to the config later)
  // follows its own `enabled:` flag
  writeFile(layoutPath(cfgPath), "pages: {Accueil: false, Sports: true}\n", 0o644);
  assertEquals(enabledPages(load(cfgPath)), ["Sports", "Media"]);

  // the list form written before toggles: names left out are off, and a list
  // naming no current page falls back to the config's flags
  writeFile(layoutPath(cfgPath), "pages: [Sports]\n", 0o644);
  assertEquals(enabledPages(load(cfgPath)), ["Sports"]);
  writeFile(layoutPath(cfgPath), "pages: [Gone]\n", 0o644);
  assertEquals(enabledPages(load(cfgPath)).length, 2);
});

Deno.test("all pages hidden shows the first", () => {
  const cfg = load(tempConfig(`
pages:
  - { name: A, enabled: false, widgets: [ { type: hackernews } ] }
  - { name: B, enabled: false, widgets: [ { type: hackernews } ] }
`));
  assertEquals(enabledPages(cfg), ["A"]);
});

Deno.test("legacy layout file", () => {
  const cfgPath = tempConfig(namedColumnsConfig);
  // the first layout.yaml shape: just page -> columns at top level
  writeFile(layoutPath(cfgPath), "Accueil:\n    - Niches\n", 0o644);
  assertEquals(load(cfgPath).pages[0].choices.map((c) => c.enabled), [true, false, false]);
});

Deno.test("ttl and limit defaults, and a bad ttl", () => {
  const cfg = load(tempConfig("widgets:\n  - { type: hackernews, ttl: 1h30m }\n  - { type: hackernews }\n"));
  assertEquals(cfg.pages[0].boxes[0].widgets[0].ttl, 90 * 60_000);
  assertEquals(cfg.pages[0].boxes[1].widgets[0].ttl, 15 * 60_000);
  assertEquals(cfg.pages[0].boxes[1].widgets[0].limit, 15);
  assertThrows(() => load(tempConfig("widgets:\n  - { type: hackernews, ttl: soon }\n")));
});
