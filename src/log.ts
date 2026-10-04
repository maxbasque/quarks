// A small structured logger in the key=value text format the Go version's
// slog.TextHandler wrote, so the service's logs read the same.

export type Level = "DEBUG" | "INFO" | "WARN" | "ERROR";

export class Logger {
  #write: (line: string) => void;

  constructor(write?: (line: string) => void) {
    const enc = new TextEncoder();
    this.#write = write ?? ((line) => Deno.stderr.writeSync(enc.encode(line)));
  }

  // toFile appends to path, or discards logs if it can't be opened — an app
  // launched from Finder has no terminal to write to.
  static toFile(path: string): Logger {
    try {
      const f = Deno.openSync(path, { create: true, append: true, write: true });
      const enc = new TextEncoder();
      return new Logger((line) => f.writeSync(enc.encode(line)));
    } catch {
      return new Logger(() => {});
    }
  }

  log(level: Level, msg: string, attrs: Record<string, unknown> = {}) {
    let line = `time=${new Date().toISOString()} level=${level} msg=${fmt(msg)}`;
    for (const [k, v] of Object.entries(attrs)) line += ` ${k}=${fmt(v)}`;
    try {
      this.#write(line + "\n");
    } catch {
      // nowhere left to report a logging failure
    }
  }

  info(msg: string, attrs?: Record<string, unknown>) {
    this.log("INFO", msg, attrs);
  }
  warn(msg: string, attrs?: Record<string, unknown>) {
    this.log("WARN", msg, attrs);
  }
  error(msg: string, attrs?: Record<string, unknown>) {
    this.log("ERROR", msg, attrs);
  }
}

function fmt(v: unknown): string {
  let s: string;
  if (v instanceof Error) s = v.message;
  else if (v instanceof Date) s = v.toISOString();
  else if (typeof v === "string") s = v;
  else s = JSON.stringify(v) ?? String(v);
  return /[\s"=]/.test(s) || s === "" ? JSON.stringify(s) : s;
}

// log is the process-wide default logger, for code with no logger of its own
// (the config loader's token warnings). The app replaces it at startup.
export let log = new Logger();

export function setDefaultLogger(l: Logger) {
  log = l;
}
