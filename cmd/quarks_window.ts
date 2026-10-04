// Command quarks-window is the Linux dashboard window: a native WebKitGTK
// window (through webview) on the already-running quarks server. It is only a
// viewer — the server stays the systemd --user service, and closing the window
// just closes the window. No Chromium-family browser is needed.
//
// It ships inside the quarks binary as `quarks window` (one V8 runtime on disk
// instead of two); the installed quarks-window is a wrapper around that.

import { parseArgs } from "@std/cli/parse-args";
import { hardExit } from "../src/exit.ts";
import { setIconName, setProgramName } from "../src/window/gtk.ts";
import { linkScript, Webview } from "../src/window/webview.ts";

// Ctrl+W / Ctrl+Q close the window, as in a browser app window.
const pageScript = linkScript + `
document.addEventListener("keydown", function (e) {
  if (e.ctrlKey && !e.altKey && (e.key === "w" || e.key === "q")) {
    e.preventDefault();
    window.quarksClose();
  }
});
`;

const downPage = `<!doctype html><meta charset="utf-8">
<body style="font:16px system-ui;background:#1c1b1f;color:#e8e2da;display:grid;place-items:center;height:100vh;margin:0">
<div style="max-width:32em;text-align:center">
<h2>Le serveur Quark's ne répond pas</h2>
<p>Vérifiez-le avec <code>systemctl --user status quarks</code>, puis rouvrez cette fenêtre.</p>
</div>`;

async function serverUp(url: string): Promise<boolean> {
  try {
    const resp = await fetch(url, { signal: AbortSignal.timeout(1000) });
    await resp.body?.cancel();
    return true;
  } catch {
    return false;
  }
}

export async function main(argv: string[]) {
  const args = parseArgs(argv, { string: ["url"], default: { url: "http://localhost:7373/" } });
  const url = args.url;

  let up = await serverUp(url);
  if (!up) {
    // the service may just not be running (stopped, or not started yet)
    try {
      await new Deno.Command("systemctl", { args: ["--user", "start", "quarks.service"] }).output();
    } catch { /* no systemd here */ }
    for (let i = 0; i < 25 && !up; i++) {
      await new Promise((r) => setTimeout(r, 200));
      up = await serverUp(url);
    }
  }

  setProgramName("quarks");
  const w = new Webview();
  setIconName(w.window, "quarks");
  w.title = "Quark's";
  w.setSize(1500, 950);
  w.bind("quarksClose", () => w.terminate());
  w.init(pageScript);
  if (up) w.navigate(url);
  else w.setHTML(downPage);
  w.run();
  // the window is gone and there's nothing to flush: skip WebKit's exit-time
  // teardown, which can hang (see hardExit)
  hardExit(0);
}

if (import.meta.main) {
  try {
    await main(Deno.args);
  } catch (err) {
    console.error("quarks-window:", err instanceof Error ? err.message : err);
    Deno.exit(1);
  }
}
