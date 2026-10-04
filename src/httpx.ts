// Small fetch helpers shared by the providers.

export const userAgent = "quarks/0.1 (+https://github.com/maxbasque/quarks)";
export const browserUserAgent = "Mozilla/5.0 (compatible; quarks/0.1; +https://github.com/maxbasque/quarks)";

// readLimited reads at most max bytes of a response body, dropping the rest.
export async function readLimited(resp: Response, max: number): Promise<Uint8Array> {
  if (!resp.body) return new Uint8Array();
  const reader = resp.body.getReader();
  const chunks: Uint8Array[] = [];
  let size = 0;
  try {
    while (size < max) {
      const { done, value } = await reader.read();
      if (done) break;
      const take = Math.min(value.length, max - size);
      chunks.push(take === value.length ? value : value.subarray(0, take));
      size += take;
    }
  } finally {
    reader.cancel().catch(() => {});
  }
  const out = new Uint8Array(size);
  let off = 0;
  for (const c of chunks) {
    out.set(c, off);
    off += c.length;
  }
  return out;
}

// getJSON GETs url and decodes the body, failing on any status but 200.
// errPrefix labels the status error ("open-meteo" → "open-meteo: http 503").
export async function getJSON<T = any>(
  url: string,
  signal: AbortSignal,
  opts: { ua?: string; accept?: boolean; errPrefix?: string } = {},
): Promise<T> {
  const headers: Record<string, string> = { "User-Agent": opts.ua ?? userAgent };
  if (opts.accept) headers.Accept = "application/json";
  const resp = await fetch(url, { headers, signal });
  if (resp.status !== 200) {
    await resp.body?.cancel();
    throw new Error(opts.errPrefix ? `${opts.errPrefix}: http ${resp.status}` : `http ${resp.status}`);
  }
  return await resp.json();
}

// trim shortens an error body for a log line.
export function trim(b: string): string {
  const max = 300;
  return b.length > max ? b.slice(0, max) + "…" : b;
}
