// Command quarks is the dashboard server: it loads the config, keeps every
// widget fresh on its own schedule, and serves the dashboard on 127.0.0.1.
// It runs as a systemd --user service on Linux (launchd on macOS).
//
// `quarks window [--url …]` opens the Linux dashboard window instead (see
// quarks_window.ts) — it lives in this binary so there's one Deno runtime on
// disk, not two.

import { parseArgs } from "@std/cli/parse-args";
import { join } from "@std/path";
import { Logger, setDefaultLogger } from "../src/log.ts";
import { userCacheDir, userConfigDir } from "../src/paths.ts";

async function main() {
  // loaded here, not at the top, so `quarks window` doesn't load the whole
  // server (and its npm packages) just to show a window
  const { App } = await import("../src/app.ts");
  const args = parseArgs(Deno.args, {
    string: ["config", "addr"],
    default: {
      config: join(userConfigDir(), "quarks", "config.yaml"),
      addr: "127.0.0.1:7373",
    },
  });
  const m = /^(.*):(\d+)$/.exec(args.addr);
  if (!m) throw new Error(`--addr: want host:port, got ${JSON.stringify(args.addr)}`);
  const hostname = m[1].replace(/^\[|\]$/g, "") || "0.0.0.0";

  const log = new Logger();
  setDefaultLogger(log);

  const app = new App(args.config, join(userCacheDir(), "quarks"), log);

  const ctl = new AbortController();
  for (const sig of ["SIGINT", "SIGTERM"] as const) Deno.addSignalListener(sig, () => ctl.abort());

  await app.run(ctl.signal, hostname, +m[2]);
}

if (import.meta.main) {
  try {
    if (Deno.args[0] === "window") {
      const window = await import("./quarks_window.ts");
      await window.main(Deno.args.slice(1));
    } else {
      await main();
    }
  } catch (err) {
    console.error("quarks:", err instanceof Error ? err.message : err);
    Deno.exit(1);
  }
  Deno.exit(0); // stop any timers still pending once the server is down
}
