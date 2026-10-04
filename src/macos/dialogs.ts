import { hardExit } from "../exit.ts";

// The macOS app's dialogs. They go through osascript, so they work even when
// the error came before (or instead of) the window — and from the server
// worker, whose thread has no UI of its own.

const headless = () => Deno.env.get("QUARKS_HEADLESS") !== undefined; // CI: no window, no dialogs

function osaQuote(s: string): string {
  return s.replaceAll("\\", "\\\\").replaceAll('"', '\\"');
}

// askReset explains a config error and asks whether to start over from the
// default config. True means Réinitialiser.
export async function askReset(cause: string): Promise<boolean> {
  const msg = "La configuration de Quark's contient une erreur :\n\n" + cause +
    "\n\nRéinitialiser remet la configuration par défaut. L’ancienne est gardée à côté, " +
    "dans Application Support › quarks.";
  try {
    const out = await new Deno.Command("/usr/bin/osascript", {
      args: [
        "-e",
        `display dialog "${osaQuote(msg)}" with title "Quark's" buttons {"Quitter", "Réinitialiser"} ` +
        `default button "Réinitialiser" cancel button "Quitter" with icon caution`,
      ],
      stderr: "null",
    }).output();
    return out.success && new TextDecoder().decode(out.stdout).includes("Réinitialiser");
  } catch {
    return false;
  }
}

// alert shows a plain dialog (or, headless, prints to stderr).
export function alert(msg: string) {
  if (headless() || Deno.build.os !== "darwin") {
    console.error(msg);
    return;
  }
  try {
    new Deno.Command("/usr/bin/osascript", {
      args: [
        "-e",
        `display dialog "${osaQuote(msg)}" with title "Quark's" buttons {"OK"} default button 1 with icon caution`,
      ],
    }).outputSync();
  } catch { /* nowhere left to report it */ }
}

// fatal reports msg and ends the whole process at once, from any thread.
export function fatal(msg: string): never {
  alert(msg);
  hardExit(1);
}
