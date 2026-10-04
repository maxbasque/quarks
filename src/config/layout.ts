import { dirname, join } from "@std/path";
import YAML from "yaml";
import { tokenRe } from "./secrets.ts";

// The layout file holds choices made from the Settings page — which pages and
// which named columns are shown — so the app never has to rewrite the
// hand-edited config.yaml:
//
//	pages:
//	  Accueil: true
//	  Media: false
//	columns:
//	  Accueil:
//	    Niches: false
//	    Nouvelles: true
//
// A page or column the file doesn't mention — one added to config.yaml after
// the last save — uses its `enabled:` flag from config.yaml.
export interface Layout {
  pages: Toggles;
  columns: Record<string, Toggles>;
}

// Toggles is an on/off choice per name. Earlier versions of the file wrote a
// list of the names that were on; such a list is exclusive — names missing
// from it are off — and one naming nothing current is ignored.
export interface Toggles {
  on: Record<string, boolean>;
  exclusive?: boolean;
}

export function emptyToggles(): Toggles {
  return { on: {} };
}

function parseToggles(v: unknown, where: string): Toggles {
  if (v == null) return emptyToggles();
  if (Array.isArray(v)) {
    const on: Record<string, boolean> = {};
    for (const name of v) on[String(name)] = true;
    return { on, exclusive: true };
  }
  if (typeof v !== "object") throw new Error(`${where}: expected a mapping or a list`);
  const on: Record<string, boolean> = {};
  for (const [k, b] of Object.entries(v)) {
    if (typeof b !== "boolean") throw new Error(`${where}.${k}: expected true or false`);
    on[k] = b;
  }
  return { on };
}

// resolve says which of names are on, given each one's config default.
export function resolve(t: Toggles, names: string[], defaults: boolean[]): boolean[] {
  if (t.exclusive && !names.some((n) => t.on[n])) return defaults;
  return names.map((n, i) => {
    if (Object.hasOwn(t.on, n)) return t.on[n];
    return t.exclusive ? false : defaults[i];
  });
}

// layoutPath is the layout file that sits beside the config file.
export function layoutPath(configPath: string): string {
  return join(dirname(configPath), "layout.yaml");
}

// loadLayout reads the layout file. A missing file is fine (empty layout).
export function loadLayout(path: string): Layout {
  const l: Layout = { pages: emptyToggles(), columns: {} };
  let data: string;
  try {
    data = Deno.readTextFileSync(path);
  } catch (err) {
    if (err instanceof Deno.errors.NotFound) return l;
    throw err;
  }

  try {
    const top = YAML.parse(data);
    if (top == null) return l;
    if (typeof top !== "object" || Array.isArray(top)) throw new Error("not a mapping");
    // the first shape: page -> [columns] at top level
    const legacy = Object.keys(top).some((k) => k !== "pages" && k !== "columns");
    const cols: Record<string, unknown> = legacy ? top : (top.columns ?? {});
    if (!legacy) l.pages = parseToggles(top.pages, "pages");
    if (typeof cols !== "object" || Array.isArray(cols)) throw new Error("columns: expected a mapping");
    for (const [page, t] of Object.entries(cols)) l.columns[page] = parseToggles(t, `columns.${page}`);
  } catch (err) {
    throw new Error(`parse ${path}: ${(err as Error).message}`);
  }
  return l;
}

// saveLayout writes the layout file whole.
export function saveLayout(path: string, l: Layout) {
  const sorted = (o: Record<string, boolean>) => Object.fromEntries(Object.keys(o).sort().map((k) => [k, o[k]]));
  const out = {
    pages: sorted(l.pages.on),
    columns: Object.fromEntries(Object.keys(l.columns).sort().map((p) => [p, sorted(l.columns[p].on)])),
  };
  // four-space indents, as the Go version wrote it
  Deno.writeTextFileSync(path, YAML.stringify(out, { indent: 4 }), { mode: 0o644 });
}

// missingSecrets lists, in first-seen order, the ${secret:key} references in
// nodes (unexpanded config) that secrets doesn't define.
export function missingSecrets(nodes: unknown[], secrets: Record<string, string>): string[] {
  const out: string[] = [];
  const seen = new Set<string>();
  const scan = (s: string) => {
    for (const m of s.matchAll(tokenRe)) {
      if (!m[1].startsWith("secret:")) continue;
      const key = m[1].slice("secret:".length);
      if (!secrets[key] && !seen.has(key)) {
        seen.add(key);
        out.push(key);
      }
    }
  };
  const walk = (n: unknown) => {
    if (typeof n === "string") scan(n);
    else if (Array.isArray(n)) n.forEach(walk);
    else if (n && typeof n === "object") {
      for (const [k, v] of Object.entries(n)) {
        scan(k);
        walk(v);
      }
    }
  };
  nodes.forEach(walk);
  return out;
}
