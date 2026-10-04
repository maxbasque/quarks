import { join } from "@std/path";

// The per-user config and cache directories, as Go's os.UserConfigDir and
// os.UserCacheDir define them.

function home(): string {
  return Deno.env.get("HOME") ?? Deno.env.get("USERPROFILE") ?? ".";
}

export function userConfigDir(): string {
  switch (Deno.build.os) {
    case "darwin":
      return join(home(), "Library", "Application Support");
    case "windows":
      return Deno.env.get("AppData") ?? join(home(), "AppData", "Roaming");
    default:
      return Deno.env.get("XDG_CONFIG_HOME") || join(home(), ".config");
  }
}

export function userCacheDir(): string {
  switch (Deno.build.os) {
    case "darwin":
      return join(home(), "Library", "Caches");
    case "windows":
      return Deno.env.get("LocalAppData") ?? join(home(), "AppData", "Local");
    default:
      return Deno.env.get("XDG_CACHE_HOME") || join(home(), ".cache");
  }
}

export function homeDir(): string {
  return home();
}
