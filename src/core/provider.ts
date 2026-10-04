import type { Payload } from "./types.ts";

// Provider is one widget instance's data source. That is the whole contract.
// Credentials never appear here: a secret URL and an OAuth token are both just
// "something this provider was configured with".
export interface Provider {
  fetch(signal: AbortSignal): Promise<Payload>;
}

// Factory builds a Provider from its widget config.
export type Factory = (cfg: WidgetConfig) => Provider;

// WidgetConfig is the parsed common shape of a widget entry in the YAML.
// Provider-specific keys (feeds, subreddits, channels, ...) stay in raw and are
// read by the factory through Settings, so core knows nothing about them.
export interface WidgetConfig {
  type: string;
  column: number;
  title: string;
  ttl: number; // ms
  limit: number;
  raw: Record<string, unknown>;
}

// parseWidget turns a raw YAML mapping into a WidgetConfig, applying defaults.
export function parseWidget(node: unknown): WidgetConfig {
  const s = new Settings(node);
  const ttlStr = s.str("ttl");
  return {
    type: s.str("type"),
    column: s.int("column") || 1,
    title: s.str("title"),
    ttl: ttlStr ? parseDuration(ttlStr) : 15 * 60_000,
    limit: s.int("limit") || 15,
    raw: s.raw,
  };
}

// Settings reads typed keys out of a widget's YAML mapping. A key that is
// present with the wrong type is an error, the way decoding into a typed struct
// would be; a missing key reads as the zero value.
export class Settings {
  readonly raw: Record<string, unknown>;

  constructor(node: unknown) {
    if (node == null) node = {};
    if (typeof node !== "object" || Array.isArray(node)) {
      throw new Error(`expected a mapping, got ${describe(node)}`);
    }
    this.raw = node as Record<string, unknown>;
  }

  static of(cfg: WidgetConfig): Settings {
    return new Settings(cfg.raw);
  }

  str(key: string): string {
    const v = this.raw[key];
    if (v == null) return "";
    if (typeof v === "string") return v;
    if (typeof v === "number" || typeof v === "boolean") return String(v);
    throw new Error(`${key}: expected a string, got ${describe(v)}`);
  }

  num(key: string): number {
    const v = this.raw[key];
    if (v == null) return 0;
    if (typeof v === "number") return v;
    if (typeof v === "string" && v.trim() !== "" && !isNaN(Number(v))) return Number(v);
    throw new Error(`${key}: expected a number, got ${describe(v)}`);
  }

  int(key: string): number {
    const n = this.num(key);
    if (!Number.isInteger(n)) throw new Error(`${key}: expected an integer, got ${n}`);
    return n;
  }

  // bool returns undefined when the key is absent, so callers can default it.
  bool(key: string): boolean | undefined {
    const v = this.raw[key];
    if (v == null) return undefined;
    if (typeof v === "boolean") return v;
    throw new Error(`${key}: expected true or false, got ${describe(v)}`);
  }

  strList(key: string): string[] {
    const v = this.raw[key];
    if (v == null) return [];
    if (!Array.isArray(v)) throw new Error(`${key}: expected a list, got ${describe(v)}`);
    return v.map((e, i) => {
      if (e == null) return "";
      if (typeof e === "string") return e;
      if (typeof e === "number" || typeof e === "boolean") return String(e);
      throw new Error(`${key}[${i}]: expected a string, got ${describe(e)}`);
    });
  }
}

function describe(v: unknown): string {
  if (Array.isArray(v)) return "a list";
  if (v === null) return "null";
  if (typeof v === "object") return "a mapping";
  return JSON.stringify(v);
}

// Registry maps a widget type name ("rss", "reddit", ...) to its Factory.
// Adding a feed type = one file + one registry line.
export class Registry {
  #factories = new Map<string, Factory>();

  register(name: string, f: Factory) {
    this.#factories.set(name, f);
  }

  // build resolves a widget config to a live Provider.
  build(cfg: WidgetConfig): Provider {
    const f = this.#factories.get(cfg.type);
    if (!f) throw new Error(`unknown widget type ${JSON.stringify(cfg.type)}`);
    return f(cfg);
  }
}

const units: Record<string, number> = {
  ns: 1e-6,
  us: 1e-3,
  "µs": 1e-3,
  "μs": 1e-3,
  ms: 1,
  s: 1000,
  m: 60_000,
  h: 3_600_000,
};

// parseDuration reads a Go-style duration ("15m", "1h30m", "90s", "1.5h") into
// milliseconds — the config's ttl format.
export function parseDuration(s: string): number {
  const orig = s;
  let sign = 1;
  if (s[0] === "-" || s[0] === "+") {
    if (s[0] === "-") sign = -1;
    s = s.slice(1);
  }
  if (s === "0") return 0;
  if (s === "") throw new Error(`time: invalid duration ${JSON.stringify(orig)}`);
  let total = 0;
  const re = /^(\d+(?:\.\d*)?|\.\d+)(ns|us|µs|μs|ms|s|m|h)/;
  while (s !== "") {
    const m = re.exec(s);
    if (!m) throw new Error(`time: invalid duration ${JSON.stringify(orig)}`);
    total += parseFloat(m[1]) * units[m[2]];
    s = s.slice(m[0].length);
  }
  return sign * total;
}
