import { dirname, join } from "@std/path";
import YAML from "yaml";
import { log } from "../log.ts";

// tokenRe matches ${NAME} and ${secret:NAME} placeholders.
export const tokenRe = /\$\{([A-Za-z0-9_.:-]+)\}/g;

// expandTokens substitutes ${VAR} (environment) and ${secret:key} (secrets
// file) placeholders in the raw config text before YAML parsing, so a token
// works anywhere — key, scalar value, or list element. Quote the token in the
// config (`"${secret:x}"`); a secret value must not itself contain a double
// quote. Unresolved tokens become empty and are logged, as do values that
// could break out of a quoted scalar (see safeValue).
export function expandTokens(data: string, secrets: Record<string, string>): string {
  return data.replace(tokenRe, (_m, name: string) => {
    if (name.startsWith("secret:")) {
      const key = name.slice("secret:".length);
      if (Object.hasOwn(secrets, key)) {
        const v = secrets[key];
        if (!safeValue(v)) {
          log.warn("config: secret holds a quote, backslash or control character; ignoring it", { key });
          return "";
        }
        return v;
      }
      log.warn("config: unresolved secret", { key });
      return "";
    }
    const v = Deno.env.get(name);
    if (v !== undefined) {
      if (!safeValue(v)) {
        log.warn("config: environment variable holds a quote, backslash or control character; ignoring it", {
          name,
        });
        return "";
      }
      return v;
    }
    log.warn("config: unresolved environment variable", { name });
    return "";
  });
}

// safeValue reports whether v can be pasted into the config text inside a
// double-quoted scalar without ending it: tokens are expanded before YAML
// parsing, so a value carrying `"` and a newline could otherwise inject whole
// widgets into the config.
export function safeValue(v: string): boolean {
  for (const ch of v) {
    const c = ch.codePointAt(0)!;
    if (ch === '"' || ch === "\\" || c < 0x20 || c === 0x7f) return false;
  }
  return true;
}

// loadSecrets reads a flat key: value YAML file. A missing file is fine
// (returns an empty map); a present file must be mode 0600 or stricter.
export function loadSecrets(path: string): Record<string, string> {
  let info: Deno.FileInfo;
  try {
    info = Deno.statSync(path);
  } catch (err) {
    if (err instanceof Deno.errors.NotFound) return {};
    throw err;
  }
  const perm = (info.mode ?? 0) & 0o777;
  if (perm & 0o077) {
    throw new Error(`${path}: permissions ${perm.toString(8)} are too open, run: chmod 600 ${path}`);
  }

  const data = Deno.readTextFileSync(path);
  let m: unknown;
  try {
    m = YAML.parse(data);
  } catch (err) {
    throw new Error(`parse ${path}: ${(err as Error).message}`);
  }
  if (m == null) return {};
  if (typeof m !== "object" || Array.isArray(m)) throw new Error(`parse ${path}: not a key: value mapping`);
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(m)) {
    if (v != null && typeof v === "object") throw new Error(`parse ${path}: ${k}: expected a single value`);
    out[k] = v == null ? "" : String(v);
  }
  return out;
}

// secretsPath is the secrets file that sits beside the config file.
export function secretsPath(configPath: string): string {
  return join(dirname(configPath), "secrets.yaml");
}

// readSecretKeys reads only the requested keys from the secrets file, omitting
// any that aren't set. A missing file yields an empty result — callers that
// only want a status check (is spotify connected?) shouldn't have to handle
// "file doesn't exist yet" as an error.
export function readSecretKeys(path: string, ...keys: string[]): Record<string, string> {
  const all = loadSecrets(path);
  const out: Record<string, string> = {};
  for (const k of keys) {
    if (all[k]) out[k] = all[k];
  }
  return out;
}

// listSecretKeys returns every key currently set in the secrets file, sorted.
// Values never appear here — only names, since this backs a settings UI that
// must not echo secret values back into HTML. A missing file yields an empty
// list, not an error.
export function listSecretKeys(path: string): string[] {
  const all = loadSecrets(path);
  return Object.keys(all)
    .filter((k) => all[k] !== "") // an empty value counts as "not set", same as readSecretKeys
    .sort();
}

// readDoc parses the secrets file as a YAML document (keeping its comments),
// or returns null for a missing file.
function readDoc(path: string): YAML.Document | null {
  let data: string;
  try {
    data = Deno.readTextFileSync(path);
  } catch (err) {
    if (err instanceof Deno.errors.NotFound) return null;
    throw err;
  }
  const doc = YAML.parseDocument(data);
  if (doc.errors.length > 0) throw new Error(`parse ${path}: ${doc.errors[0].message}`);
  return doc;
}

function writeDoc(path: string, doc: YAML.Document) {
  Deno.writeTextFileSync(path, doc.toString(), { mode: 0o600 });
  // the mode above only applies when creating the file
  Deno.chmodSync(path, 0o600);
}

// setSecrets updates or appends key: value pairs in the secrets file at path,
// preserving existing keys' order, comments, and any keys not mentioned in kv.
// The file is created if missing and is always left at mode 0600 — including
// when it already existed at a looser mode.
export function setSecrets(path: string, kv: Record<string, string>) {
  let doc = readDoc(path);
  if (doc === null || doc.contents === null) doc = new YAML.Document({});
  if (!YAML.isMap(doc.contents)) throw new Error(`${path}: not a YAML mapping`);

  // deterministic order for newly-appended keys; an existing key's scalar is
  // updated in place, which keeps its comments and quoting
  for (const k of Object.keys(kv).sort()) doc.set(k, kv[k]);
  writeDoc(path, doc);
}

// deleteSecret removes key from the secrets file, preserving every other key's
// order and comments the same way setSecrets does. A missing key or a missing
// file is a no-op, not an error.
export function deleteSecret(path: string, key: string) {
  const doc = readDoc(path);
  if (doc === null || doc.contents === null) return;
  if (!YAML.isMap(doc.contents)) throw new Error(`${path}: not a YAML mapping`);
  if (!doc.has(key)) return;
  doc.delete(key);
  writeDoc(path, doc);
}
