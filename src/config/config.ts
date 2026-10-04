import YAML from "yaml";
import { parseWidget, Settings, type WidgetConfig } from "../core/provider.ts";
import { layoutPath, loadLayout, missingSecrets, resolve, type Toggles } from "./layout.ts";
import { expandTokens, loadSecrets, secretsPath } from "./secrets.ts";

export interface Window {
  theme: string;
}

// Page is one top-level tab of the app: its own column layout and cards.
export interface Page {
  name: string;
  // enabled is false for a page switched off (`enabled: false`, or from
  // Settings). It's still fully parsed so Settings can list it and its
  // columns, but the app neither shows nor fetches it.
  enabled: boolean;
  columns: number;
  columnWeights: number[];
  boxes: Box[];

  // choices is set when the page declares named columns: every column the
  // config offers, shown or not, in config order. Settings toggles them.
  // boxes / columns / columnWeights above hold only the enabled ones.
  choices: ColumnChoice[];
  maxColumns: number;
}

// ColumnChoice is one named, toggleable column of a page.
export interface ColumnChoice {
  name: string;
  enabled: boolean;
  // missingSecrets lists ${secret:key} references in this column's widgets
  // that secrets.yaml doesn't define — Settings flags them.
  missingSecrets: string[];
}

// Box is one card. It holds one widget, or several shown as tabs (`type: group`).
export interface Box {
  column: number;
  title: string;
  widgets: WidgetConfig[];
}

export interface Config {
  window: Window;
  pages: Page[];
}

// PageShape is a page entry as written in the file. columns is either a count
// (cards place themselves with `column: N`) or a list of named columns, each
// holding its own widgets.
interface PageShape {
  name: string;
  enabled?: boolean; // default true
  columns: unknown;
  maxColumns: number;
  columnWeights: number[];
  widgets: unknown[];
}

function mapping(v: unknown, where: string): Settings {
  try {
    return new Settings(v);
  } catch (err) {
    throw new Error(`${where}: ${(err as Error).message}`);
  }
}

function list(v: unknown, where: string): unknown[] {
  if (v == null) return [];
  if (!Array.isArray(v)) throw new Error(`${where}: expected a list`);
  return v;
}

function numList(v: unknown, where: string): number[] {
  return list(v, where).map((n, i) => {
    if (typeof n !== "number") throw new Error(`${where}[${i}]: expected a number`);
    return n;
  });
}

function pageShape(v: unknown, where: string): PageShape {
  const s = mapping(v, where);
  return {
    name: s.str("name"),
    enabled: s.bool("enabled"),
    columns: s.raw.columns,
    maxColumns: s.int("max_columns"),
    columnWeights: numList(s.raw.column_weights, `${where}: column_weights`),
    widgets: list(s.raw.widgets, `${where}: widgets`),
  };
}

function parseYAML(text: string, path: string): Record<string, unknown> {
  let v: unknown;
  try {
    v = YAML.parse(text);
  } catch (err) {
    throw new Error(`parse ${path}: ${(err as Error).message}`);
  }
  if (v == null) return {};
  if (typeof v !== "object" || Array.isArray(v)) throw new Error(`parse ${path}: the file is not a mapping`);
  return v as Record<string, unknown>;
}

// load reads and validates the config file at path.
export function load(path: string): Config {
  let data = Deno.readTextFileSync(path);
  const secrets = loadSecrets(secretsPath(path));
  const layout = loadLayout(layoutPath(path));

  // the raw (unexpanded) tree is only for spotting unresolved secrets per
  // named column; everything else reads the expanded one
  const raw = parseYAML(data, path);
  data = expandTokens(data, secrets);
  const fs = parseYAML(data, path);

  const win = mapping(fs.window, "window");
  const cfg: Config = { window: { theme: win.str("theme") || "dark" }, pages: [] };

  const rawPages = Array.isArray(raw.pages) ? raw.pages : [];
  let shapes = list(fs.pages, "pages").map((p, i) => pageShape(p, `page ${i}`));
  if (shapes.length === 0) {
    // legacy: top-level widgets → one unnamed page
    shapes = [{
      name: "",
      columns: win.raw.columns ?? undefined,
      maxColumns: 0,
      columnWeights: numList(win.raw.column_weights, "window: column_weights"),
      widgets: list(fs.widgets, "widgets"),
    }];
  }

  const names = new Set<string>();
  shapes.forEach((ps, i) => {
    if (ps.name !== "" && names.has(ps.name)) {
      throw new Error(`${path}: page ${JSON.stringify(ps.name)} listed twice`);
    }
    names.add(ps.name);
    const page: Page = {
      name: ps.name,
      enabled: ps.enabled ?? true,
      columns: 0,
      columnWeights: ps.columnWeights,
      boxes: [],
      choices: [],
      maxColumns: 0,
    };

    if (Array.isArray(ps.columns)) {
      const rawCols = Array.isArray((rawPages[i] as any)?.columns) ? (rawPages[i] as any).columns : [];
      try {
        loadNamedColumns(page, ps, rawCols, layout.columns[ps.name] ?? { on: {} }, secrets);
      } catch (err) {
        throw new Error(`page ${JSON.stringify(ps.name)}: ${(err as Error).message}`);
      }
    } else if (ps.columns != null) {
      if (typeof ps.columns !== "number" || !Number.isInteger(ps.columns)) {
        throw new Error(`page ${JSON.stringify(ps.name)}: columns: expected a number or a list of columns`);
      }
      page.columns = ps.columns;
    }
    if (page.columns === 0) page.columns = 3;

    ps.widgets.forEach((node, j) => {
      try {
        page.boxes.push(parseBox(node));
      } catch (err) {
        throw new Error(`page ${i}, widget ${j}: ${(err as Error).message}`);
      }
    });
    if (page.boxes.length === 0) {
      throw new Error(`${path}: page ${JSON.stringify(page.name)} has no widgets`);
    }
    cfg.pages.push(page);
  });
  applyPageChoice(cfg.pages, layout.pages);
  return cfg;
}

// applyPageChoice overrides the pages' `enabled:` flags with the Settings
// page's saved choice, and makes sure at least one page stays shown.
function applyPageChoice(pages: Page[], choice: Toggles) {
  const on = resolve(choice, pages.map((p) => p.name), pages.map((p) => p.enabled));
  pages.forEach((p, i) => (p.enabled = on[i]));
  if (!on.includes(true)) pages[0].enabled = true;
}

// loadNamedColumns fills page from a `columns:` list. choice is the Settings
// page's saved choice for this page and overrides each column's `enabled:`
// flag. Only enabled columns become boxes, so a hidden column's widgets are
// never fetched.
function loadNamedColumns(
  page: Page,
  ps: PageShape,
  rawCols: any[],
  choice: Toggles,
  secrets: Record<string, string>,
) {
  const cols = (ps.columns as unknown[]).map((c, k) => {
    const s = mapping(c, `columns: column ${k}`);
    return {
      name: s.str("name"),
      enabled: s.bool("enabled"),
      weight: s.num("weight"),
      widgets: list(s.raw.widgets, `columns: column ${k}: widgets`),
    };
  });
  page.maxColumns = ps.maxColumns > 0 ? ps.maxColumns : 3;

  const seen = new Set<string>();
  cols.forEach((c, k) => {
    if (c.name === "") throw new Error(`column ${k}: missing name`);
    if (seen.has(c.name)) throw new Error(`column ${JSON.stringify(c.name)} listed twice`);
    seen.add(c.name);
  });
  const defaults = cols.map((c) => c.enabled ?? true);
  let on = resolve(choice, cols.map((c) => c.name), defaults);
  if (!on.includes(true)) on = defaults; // a choice switching every column off falls back to the config's

  let shown = 0;
  cols.forEach((c, k) => {
    if (on[k] && shown >= page.maxColumns) on[k] = false;
    const rawWidgets = Array.isArray(rawCols[k]?.widgets) ? rawCols[k].widgets : [];
    page.choices.push({
      name: c.name,
      enabled: on[k],
      missingSecrets: k < rawCols.length ? missingSecrets(rawWidgets, secrets) : [],
    });
    if (!on[k]) return;
    shown++;
    page.columnWeights.push(c.weight);
    c.widgets.forEach((node, j) => {
      let box: Box;
      try {
        box = parseBox(node);
      } catch (err) {
        throw new Error(`column ${JSON.stringify(c.name)}, widget ${j}: ${(err as Error).message}`);
      }
      box.column = shown;
      for (const w of box.widgets) w.column = shown;
      page.boxes.push(box);
    });
  });
  page.columns = shown;

  // weights are optional; any unset one drops them all back to equal widths
  if (page.columnWeights.some((w) => w <= 0)) page.columnWeights = [];
}

function parseBox(node: unknown): Box {
  const probe = new Settings(node);
  const type = probe.str("type");

  if (type === "group" || type === "tabs") {
    const title = probe.str("title");
    const tabs = list(probe.raw.tabs, "tabs");
    if (tabs.length === 0) throw new Error(`group ${JSON.stringify(title)}: no tabs`);
    const column = probe.int("column") || 1;
    const box: Box = { column, title, widgets: [] };
    tabs.forEach((tab, j) => {
      let wc: WidgetConfig;
      try {
        wc = parseWidget(tab);
      } catch (err) {
        throw new Error(`tab ${j}: ${(err as Error).message}`);
      }
      if (wc.type === "") throw new Error(`tab ${j}: missing type`);
      wc.column = column;
      box.widgets.push(wc);
    });
    return box;
  }

  const wc = parseWidget(node);
  if (wc.type === "") throw new Error("missing type");
  return { column: wc.column, title: "", widgets: [wc] };
}
