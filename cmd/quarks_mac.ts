// Command quarks-mac is the macOS app: the dashboard server and a native
// window (WebKit, through webview) showing it, in one process. Opening the app
// starts both; quitting it, or closing its window, stops both — nothing keeps
// running in the background and nothing starts at login.
//
// The window's event loop owns the main thread, so the server runs in a
// worker (src/macos/server_worker.ts). It's built into Quark's.app by
// packaging/macos/build-app.sh.

import { dirname, join } from "@std/path";
import { App, InitialConfigError } from "../src/app.ts";
import { hardExit } from "../src/exit.ts";
import { Logger, setDefaultLogger } from "../src/log.ts";
import { alert, askReset } from "../src/macos/dialogs.ts";
import { homeDir } from "../src/paths.ts";
import { setUpMenus } from "../src/window/cocoa.ts";
import { linkScript, Webview } from "../src/window/webview.ts";

// preferredPort is the port the Linux build uses too; Spotify's redirect URI
// is registered against it. If something else holds it, any free port works.
const preferredPort = 7373;

class Quit extends Error {} // the user chose Quitter in the broken-config dialog

const home = homeDir();
const cfgPath = join(home, "Library", "Application Support", "quarks", "config.yaml");
const cacheDir = join(home, "Library", "Caches", "quarks");
const logPath = join(home, "Library", "Logs", "quarks.log");

// seedConfig writes the default config (built into the binary) on first run.
function seedConfig() {
  try {
    Deno.statSync(cfgPath);
    return;
  } catch (err) {
    if (!(err instanceof Deno.errors.NotFound)) throw err;
  }
  const data = Deno.readFileSync(new URL("../config.example.yaml", import.meta.url));
  Deno.mkdirSync(dirname(cfgPath), { recursive: true });
  Deno.writeFileSync(cfgPath, data, { mode: 0o644 });
}

// resetConfig moves a broken config aside and seeds a fresh one.
function resetConfig(log: Logger) {
  const p = (n: number) => String(n).padStart(2, "0");
  const d = new Date();
  const stamp = `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}-${p(d.getHours())}${p(d.getMinutes())}${
    p(d.getSeconds())
  }`;
  const backup = `${cfgPath}.ancien-${stamp}`;
  Deno.renameSync(cfgPath, backup);
  log.info("config reset", { backup });
  seedConfig();
}

// headless serves without a window until SIGTERM — for CI.
async function headless(log: Logger) {
  const app = new App(cfgPath, cacheDir, log);
  const ctl = new AbortController();
  for (const sig of ["SIGINT", "SIGTERM"] as const) Deno.addSignalListener(sig, () => ctl.abort());
  await app.start(ctl.signal); // a broken config fails here, with no dialog
  await app.serve(ctl.signal, "127.0.0.1", preferredPort, { fallback: true });
}

// windowed opens the window once the worker is serving. It only settles on a
// startup failure: closing the window ends the process from in here.
function windowed(log: Logger): Promise<never> {
  const worker = new Worker(new URL("../src/macos/server_worker.ts", import.meta.url), { type: "module" });
  return new Promise((_, reject) => {
    worker.onerror = (e) => {
      e.preventDefault();
      reject(new Error(e.message));
    };
    worker.onmessage = async (e: MessageEvent) => {
      const msg = e.data;
      switch (msg.type) {
        case "startFailed":
          // A config that won't load — usually one left by an older version —
          // gets a choice instead of a dead end: start over from the default
          // config (the old file is kept beside it) or quit.
          if (!msg.initialConfig) return reject(new Error(msg.message));
          if (!(await askReset(msg.message))) return reject(new Quit());
          try {
            resetConfig(log);
          } catch (err) {
            return reject(err);
          }
          worker.postMessage({ cmd: "retry" });
          break;
        case "listening": {
          const w = new Webview();
          if (Deno.build.os === "darwin") setUpMenus();
          w.title = "Quark's";
          w.setSize(1280, 800);
          w.init(linkScript);
          w.navigate(`http://127.0.0.1:${msg.port}/`);
          w.run(); // until the window closes
          worker.postMessage({ cmd: "stop" });
          setTimeout(() => hardExit(0), 2000); // don't hang on a stuck shutdown
          break;
        }
        case "stopped":
          // skip WebKit's exit-time teardown, which can hang (see hardExit)
          hardExit(0);
          break;
      }
    };
    worker.postMessage({ cmd: "start", cfgPath, cacheDir, logPath, port: preferredPort });
  });
}

async function main() {
  Deno.mkdirSync(dirname(logPath), { recursive: true });
  // a Finder-launched app has no terminal; headless (CI) logs to stderr
  const log = Deno.env.get("QUARKS_HEADLESS") !== undefined ? new Logger() : Logger.toFile(logPath);
  setDefaultLogger(log);
  seedConfig();
  if (Deno.env.get("QUARKS_HEADLESS") !== undefined) await headless(log);
  else await windowed(log);
}

if (import.meta.main) {
  try {
    await main();
    Deno.exit(0);
  } catch (err) {
    if (err instanceof Quit) Deno.exit(0);
    const msg = err instanceof InitialConfigError || err instanceof Error ? err.message : String(err);
    alert("Quark's n’a pas pu démarrer.\n\n" + msg);
    Deno.exit(1);
  }
}
