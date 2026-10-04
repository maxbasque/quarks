// A thin FFI binding to the webview C library (github.com/webview/webview) —
// the same library webview_go wrapped through cgo. The shared library comes
// from webview_deno's release builds (GTK 4 / WebKitGTK 6 on Linux, WKWebView
// on macOS); `make install` and the macOS bundle script fetch and pin it, so
// nothing is downloaded at run time.

import { dirname, join } from "@std/path";
import { homeDir } from "../paths.ts";

// LIBWEBVIEW_VERSION is the webview_deno release the library comes from.
export const LIBWEBVIEW_VERSION = "0.9.0";

export const libName = `libwebview.${Deno.build.arch}.${Deno.build.os === "darwin" ? "dylib" : "so"}`;

const symbols = {
  webview_create: { parameters: ["i32", "pointer"], result: "pointer" },
  webview_destroy: { parameters: ["pointer"], result: "void" },
  webview_run: { parameters: ["pointer"], result: "void" },
  webview_terminate: { parameters: ["pointer"], result: "void" },
  webview_get_window: { parameters: ["pointer"], result: "pointer" },
  webview_set_title: { parameters: ["pointer", "buffer"], result: "void" },
  webview_set_size: { parameters: ["pointer", "i32", "i32", "i32"], result: "void" },
  webview_navigate: { parameters: ["pointer", "buffer"], result: "void" },
  webview_set_html: { parameters: ["pointer", "buffer"], result: "void" },
  webview_init: { parameters: ["pointer", "buffer"], result: "void" },
  webview_bind: { parameters: ["pointer", "buffer", "function", "pointer"], result: "void" },
} as const;

// candidates are where the library may live, most specific first: an explicit
// override, the per-user install (~/.local/lib/quarks), the macOS bundle's
// Frameworks folder, or right beside the executable.
function candidates(): string[] {
  const exeDir = dirname(Deno.execPath());
  const out = [
    Deno.env.get("QUARKS_LIBWEBVIEW") ?? "",
    join(homeDir(), ".local", "lib", "quarks", libName),
    join(exeDir, "..", "Frameworks", libName),
    join(exeDir, libName),
  ];
  return out.filter((p) => p !== "");
}

function open() {
  const errors: string[] = [];
  for (const path of candidates()) {
    try {
      Deno.statSync(path);
    } catch {
      continue;
    }
    try {
      return Deno.dlopen(path, symbols);
    } catch (err) {
      errors.push(`${path}: ${(err as Error).message}`);
    }
  }
  throw new Error(
    `can't load ${libName}` + (errors.length ? ` (${errors.join("; ")})` : ` — looked in ${candidates().join(", ")}`) +
      ". Run `make install` in the Quarks repo.",
  );
}

const enc = new TextEncoder();
const cstr = (s: string) => enc.encode(s + "\0");

export class Webview {
  #lib = open();
  #handle: Deno.PointerValue;
  #callbacks: { close(): void }[] = [];

  constructor(debug = false) {
    this.#handle = this.#lib.symbols.webview_create(debug ? 1 : 0, null);
    if (!this.#handle) throw new Error("webview: could not create a window (is there a display?)");
  }

  // window is the native window: a GtkWindow* or an NSWindow*.
  get window(): Deno.PointerValue {
    return this.#lib.symbols.webview_get_window(this.#handle);
  }

  set title(t: string) {
    this.#lib.symbols.webview_set_title(this.#handle, cstr(t));
  }

  setSize(width: number, height: number) {
    this.#lib.symbols.webview_set_size(this.#handle, width, height, 0); // WEBVIEW_HINT_NONE
  }

  navigate(url: string) {
    this.#lib.symbols.webview_navigate(this.#handle, cstr(url));
  }

  setHTML(html: string) {
    this.#lib.symbols.webview_set_html(this.#handle, cstr(html));
  }

  // init injects js into every page before its own scripts run.
  init(js: string) {
    this.#lib.symbols.webview_init(this.#handle, cstr(js));
  }

  // bind exposes fn to pages as a global async function called name. fn runs
  // on the UI thread, synchronously, while run() is blocked.
  bind(name: string, fn: () => void) {
    const cb = new Deno.UnsafeCallback(
      { parameters: ["pointer", "pointer", "pointer"], result: "void" } as const,
      () => fn(),
    );
    this.#callbacks.push(cb);
    this.#lib.symbols.webview_bind(this.#handle, cstr(name), cb.pointer, null);
  }

  // run blocks in the UI event loop until the window closes or terminate is
  // called. Nothing else on this thread runs meanwhile, apart from bound
  // callbacks.
  run() {
    this.#lib.symbols.webview_run(this.#handle);
  }

  terminate() {
    this.#lib.symbols.webview_terminate(this.#handle);
  }

  destroy() {
    this.#lib.symbols.webview_destroy(this.#handle);
    for (const cb of this.#callbacks) cb.close();
    this.#lib.close();
  }
}

// linkScript runs in every page the window loads. The window can't open new
// windows, so links meant for one (articles, "ouvrir l'original") and links
// to other sites go to the default browser through the server's /open.
export const linkScript = `
document.addEventListener("click", function (e) {
  var a = e.target.closest && e.target.closest("a[href]");
  if (!a || !/^https?:/i.test(a.href)) return;
  if (a.target !== "_blank" && a.origin === location.origin) return;
  e.preventDefault();
  e.stopImmediatePropagation();
  fetch("/open", { method: "POST", body: new URLSearchParams({ url: a.href }) });
}, true);
`;
