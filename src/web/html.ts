// Escaping for the templates. Go's html/template escaped by context
// automatically; here every interpolation in an html`` template is
// HTML-escaped unless it is already Safe, and URL-valued attributes go through
// url() (scheme check + normalization) or query() (one query parameter).

export class Safe {
  constructor(readonly value: string) {}
  toString() {
    return this.value;
  }
}

// raw marks trusted markup (sanitized article HTML, CSS built from numbers).
export function raw(s: string): Safe {
  return new Safe(s);
}

const escapes: Record<string, string> = {
  "&": "&amp;",
  "<": "&lt;",
  ">": "&gt;",
  '"': "&#34;",
  "'": "&#39;",
};

export function escape(s: string): string {
  return s.replace(/[&<>"']/g, (c) => escapes[c]);
}

type Value = string | number | Safe | null | undefined | false | Value[];

function render(v: Value): string {
  if (v === null || v === undefined || v === false) return "";
  if (v instanceof Safe) return v.value;
  if (Array.isArray(v)) return v.map(render).join("");
  return escape(String(v));
}

// html is a template literal tag that escapes every interpolated value.
export function html(strings: TemplateStringsArray, ...values: Value[]): Safe {
  let out = strings[0];
  for (let i = 0; i < values.length; i++) out += render(values[i]) + strings[i + 1];
  return new Safe(out);
}

// url makes a value safe for an href or src: only http, https and mailto (or
// a relative URL) pass, anything else — javascript:, data: — becomes the same
// "#ZgotmplZ" placeholder html/template used. Characters that aren't valid in
// a URL are percent-encoded.
export function url(u: string): Safe {
  const scheme = /^([a-zA-Z][a-zA-Z0-9+.-]*):/.exec(u.trim())?.[1]?.toLowerCase();
  if (scheme !== undefined && scheme !== "http" && scheme !== "https" && scheme !== "mailto") {
    return new Safe("#ZgotmplZ");
  }
  return new Safe(escape(normalizeURL(u)));
}

// query makes a value safe as one query-parameter value: everything but
// letters, digits and -._~ is percent-encoded.
export function query(v: string): Safe {
  return new Safe(escape(encodeURIComponent(v).replace(/[!'()*]/g, pct)));
}

// normalizeURL percent-encodes what html/template's URL normalizer did:
// everything outside the unreserved set and !#$&*+,/:;=?@[], plus any % not
// starting an escape.
function normalizeURL(u: string): string {
  return u.replace(
    /%(?![0-9A-Fa-f]{2})|[^A-Za-z0-9\-._~!#$&*+,/:;=?@[\]%]/gu,
    (c) => encodeURIComponent(c).replace(/[!'()*]/g, pct),
  );
}

function pct(c: string): string {
  return "%" + c.charCodeAt(0).toString(16).toUpperCase();
}
