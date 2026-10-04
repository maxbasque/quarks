// hardExit ends the process at once with code, skipping C atexit handlers —
// the way a Go program exits. Once a WebKit window has run, the handlers
// WebKit and GTK leave behind can deadlock a normal exit (seen with
// Deno.exit after webview_run), and from a worker Deno.exit would only stop
// the worker.
export function hardExit(code: number): never {
  const libc = Deno.build.os === "darwin" ? "/usr/lib/libSystem.B.dylib" : "libc.so.6";
  Deno.dlopen(libc, { _exit: { parameters: ["i32"], result: "void" } }).symbols._exit(code);
  throw new Error("unreachable");
}
