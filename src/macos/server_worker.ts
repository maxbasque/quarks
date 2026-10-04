// The macOS app's server half. The window's event loop (webview_run) owns the
// main thread and blocks it, so the dashboard server runs here, in a worker,
// and talks to the main thread by messages:
//
//	main → worker   {cmd: "start", cfgPath, cacheDir, logPath, port}
//	worker → main   {type: "started"} | {type: "startFailed", message, initialConfig}
//	main → worker   {cmd: "retry"}        (after resetting a broken config)
//	worker → main   {type: "listening", port}
//	main → worker   {cmd: "stop"}  →  worker → main {type: "stopped"}

/// <reference lib="deno.worker" />

import { App, InitialConfigError } from "../app.ts";
import { Logger, setDefaultLogger } from "../log.ts";
import { fatal } from "./dialogs.ts";

let app: App | null = null;
const ctl = new AbortController();
let serving: Promise<void> | null = null;
let port = 0;

async function start() {
  try {
    await app!.start(ctl.signal);
  } catch (err) {
    self.postMessage({
      type: "startFailed",
      message: (err as Error).message,
      initialConfig: err instanceof InitialConfigError,
    });
    return;
  }
  self.postMessage({ type: "started" });

  serving = app!.serve(ctl.signal, "127.0.0.1", port, {
    fallback: true,
    onListen: (addr) => self.postMessage({ type: "listening", port: addr.port }),
  }).catch((err) => {
    // a server that dies while the window is up: the main thread is inside
    // the window's event loop and can't hear us, so report and exit here
    fatal("Quark's n’a pas pu démarrer.\n\n" + (err as Error).message);
  });
}

self.onmessage = async (e: MessageEvent) => {
  const msg = e.data;
  switch (msg.cmd) {
    case "start": {
      const log = Logger.toFile(msg.logPath);
      setDefaultLogger(log);
      port = msg.port;
      app = new App(msg.cfgPath, msg.cacheDir, log);
      await start();
      break;
    }
    case "retry":
      await start();
      break;
    case "stop":
      ctl.abort();
      await serving;
      await new Promise((r) => setTimeout(r, 200)); // let in-flight cache writes land
      self.postMessage({ type: "stopped" });
      break;
  }
};
