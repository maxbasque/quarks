import { encodeBase64Url } from "@std/encoding/base64url";
import type { Layout, Toggles } from "../config/layout.ts";
import { saveLayout } from "../config/layout.ts";
import { deleteSecret, listSecretKeys, readSecretKeys, safeValue, setSecrets } from "../config/secrets.ts";
import type { Client as SpotifyClient } from "../spotifyapi.ts";
import { formValues, type Handler, httpError, redirect, type Server } from "./server.ts";
import {
  type LayoutSettingsVM,
  settingsPage,
  type SettingsVM,
  type SpotifySettingsVM,
  spotifyStatusLine,
} from "./templates.ts";

const spotifyScope = "user-follow-read";

// mediaSnippet is the config.yaml block a connected user pastes in to get the
// Media tab — connecting only ever writes secrets.yaml (like every other
// integration in this app), it never edits config.yaml on the user's behalf.
// The three client_id/client_secret/refresh_token fields use the same
// ${secret:...} substitution every other credential-backed widget already
// uses (see reddit_home in config.example.yaml) — a provider only ever sees
// its widget config, so this is how it reaches the credentials at all.
const mediaSnippet = `- name: Media
  columns: 1
  widgets:
    - type: spotify
      title: Sorties à venir
      column: 1
      ttl: 15m
      client_id: "\${secret:spotify_client_id}"
      client_secret: "\${secret:spotify_client_secret}"
      refresh_token: "\${secret:spotify_refresh_token}"`;

// SettingsState is the settings page's in-memory state, kept on the Server.
export interface SettingsState {
  spotify: SpotifyClient;
  // pending is an in-flight authorize->callback round trip. Single-user, one
  // flow at a time, so a single slot is enough — no session store.
  pending: { state: string; expires: number } | null;
  // status is the last-known connected identity (or error), cached so GET
  // /settings doesn't have to hit Spotify on every view once it's been
  // checked once this process.
  status: { displayName: string; err: string } | null;
}

const settingsErrors: Record<string, string> = {
  no_credentials: "Enregistrez d’abord un Client ID et un Client Secret.",
  denied: "L’autorisation Spotify a été annulée ou refusée.",
  state_mismatch: "Ce lien d’autorisation a expiré ou a déjà servi — reconnectez-vous.",
  exchange_failed: "Spotify a refusé le code d’autorisation — vérifiez le Client ID et le Client Secret, " +
    "ainsi que l’adresse de redirection ci-dessous, inscrite telle quelle dans votre application Spotify.",
};

// reservedSecretKeys are managed by their own dedicated section (Spotify's
// connect flow) rather than the generic Secrets editor, so the same key isn't
// editable two different ways at once.
const reservedSecretKeys = new Set(["spotify_client_id", "spotify_client_secret", "spotify_refresh_token"]);

// secretKeyRe restricts hand-entered secret key names to the same shape every
// existing key in secrets.example.yaml already follows — lowercase, digits,
// underscores. Keeps them valid as-is inside a ${secret:key} token.
const secretKeyRe = /^[a-z][a-z0-9_]*$/;

const secretsErrors: Record<string, string> = {
  bad_key: "Le nom d’une clé ne contient que des minuscules, des chiffres et des _, et commence par une lettre.",
  reserved_key: "Cette clé est gérée par la section Spotify ci-dessus.",
  empty_value: "Entrez une valeur à enregistrer.",
  bad_value: "Une valeur ne peut pas contenir de guillemets, de barres obliques inverses ni de retours à la ligne.",
};

const layoutErrors: Record<string, string> = {
  no_pages: "Gardez au moins une page.",
  no_columns: "Chaque page doit garder au moins une colonne.",
  too_many: "C’est plus de colonnes que cette page n’en permet.",
};

// reload re-runs the config pipeline after a settings write; a broken config
// is the file watcher's to report, not this request's.
async function reload(s: Server) {
  try {
    await s.reloadNow();
  } catch { /* keep the previous generation */ }
}

export function settingsRoutes(s: Server): Record<string, Handler> {
  const st = s.settings;

  const page = async (req: Request): Promise<Response> => {
    if (req.method !== "GET") return httpError("GET only", 405);
    const q = new URL(req.url).searchParams;

    const sp = await spotifyVM(req);
    const code = q.get("err");
    if (code && settingsErrors[code]) sp.error = settingsErrors[code];

    const secrets = { keys: otherSecretKeys(), error: secretsErrors[q.get("serr") ?? ""] ?? "" };

    const vm: SettingsVM = {
      sections: [],
      layout: { pages: [], error: "" },
      spotify: sp,
      secrets,
    };
    const layout = layoutVM();
    if (layout.pages.length > 0) {
      layout.error = layoutErrors[q.get("lerr") ?? ""] ?? "";
      vm.layout = layout;
      const shown = layout.pages.filter((p) => p.enabled).length;
      vm.sections.push({
        id: "layout",
        label: "Pages et colonnes",
        status: `${shown} pages sur ${layout.pages.length} affichées`,
      });
    }
    vm.sections.push(
      { id: "spotify", label: "Spotify", status: spotifyStatusLine(sp) },
      {
        id: "secrets",
        label: "Clés secrètes",
        status: secrets.keys.length === 1 ? "1 configurée" : `${secrets.keys.length} configurées`,
      },
    );
    return new Response(settingsPage(vm), { headers: { "Content-Type": "text/html; charset=utf-8" } });
  };

  // layoutVM lists every named page and its columns, for the checkboxes.
  const layoutVM = (): LayoutSettingsVM => ({
    pages: s.meta.layout.map((pg) => ({
      name: pg.name,
      enabled: pg.enabled,
      max: pg.maxColumns,
      shown: pg.columns.filter((c) => c.enabled).length,
      columns: pg.columns.map((c) => ({ name: c.name, enabled: c.enabled, missingSecrets: c.missingSecrets })),
    })),
    error: "",
  });

  // otherSecretKeys lists every secrets.yaml key not already owned by a
  // dedicated section (Spotify's) — the generic editor for anything else a
  // widget config references via ${secret:key}.
  const otherSecretKeys = (): string[] => {
    try {
      return listSecretKeys(s.secretsPath).filter((k) => !reservedSecretKeys.has(k));
    } catch {
      return [];
    }
  };

  const readSecrets = (...keys: string[]): Record<string, string> | null => {
    try {
      return readSecretKeys(s.secretsPath, ...keys);
    } catch {
      return null;
    }
  };

  const spotifyVM = async (req: Request): Promise<SpotifySettingsVM> => {
    const secrets = readSecrets("spotify_client_id", "spotify_client_secret", "spotify_refresh_token") ?? {};
    const sp: SpotifySettingsVM = {
      clientIdSet: !!secrets.spotify_client_id,
      clientSecretSet: !!secrets.spotify_client_secret,
      connected: false,
      displayName: "",
      error: "",
      redirectURI: redirectURI(req),
      mediaSnippet: "",
    };
    if (!secrets.spotify_refresh_token) return sp;

    const status = st.status ?? (await checkStatus(secrets));
    if (status.displayName) {
      sp.connected = true;
      sp.displayName = status.displayName;
      sp.mediaSnippet = mediaSnippet;
    } else if (status.err) {
      // A refresh token is on file but we couldn't verify it (network hiccup,
      // or it was revoked on Spotify's side) — still "connected" as far as
      // config goes, just flag it rather than silently claiming success.
      sp.connected = true;
      sp.error = "Connecté, mais la dernière vérification a échoué : " + status.err;
      sp.mediaSnippet = mediaSnippet;
    }
    return sp;
  };

  // checkStatus does a live token refresh + whoAmI to populate the display
  // name shown on the settings page, and caches the result.
  const checkStatus = async (secrets: Record<string, string>) => {
    const creds = {
      clientId: secrets.spotify_client_id ?? "",
      clientSecret: secrets.spotify_client_secret ?? "",
      refreshToken: secrets.spotify_refresh_token ?? "",
    };
    const status = { displayName: "", err: "" };
    try {
      const { accessToken } = await st.spotify.refreshAccessToken(creds, AbortSignal.timeout(20_000));
      status.displayName = await st.spotify.whoAmI(accessToken, AbortSignal.timeout(20_000));
    } catch (err) {
      status.err = (err as Error).message;
    }
    st.status = status;
    return status;
  };

  // layoutSet saves which pages, and which named columns of each, are shown.
  // The form always carries the whole choice: `page` once per shown page,
  // `cols.<page>` once per shown column. It goes to layout.yaml, never
  // config.yaml.
  const layoutSet = async (req: Request): Promise<Response> => {
    if (req.method !== "POST") return httpError("POST only", 405);
    const form = await formValues(req);
    const shown = new Set(form.all("page"));

    // every page and column the config has gets an explicit on/off; names the
    // config doesn't have are dropped
    const l: Layout = { pages: { on: {} }, columns: {} };
    let anyPage = false;
    for (const pg of s.meta.layout) {
      l.pages.on[pg.name] = shown.has(pg.name);
      anyPage ||= shown.has(pg.name);
      if (pg.columns.length === 0) continue;
      const want = new Set(form.all("cols." + pg.name));
      const cols: Toggles = { on: {} };
      let n = 0;
      for (const c of pg.columns) {
        cols.on[c.name] = want.has(c.name);
        if (want.has(c.name)) n++;
      }
      if (n === 0) return redirect("/settings?lerr=no_columns#layout", 303);
      if (n > pg.maxColumns) return redirect("/settings?lerr=too_many#layout", 303);
      l.columns[pg.name] = cols;
    }
    if (!anyPage) return redirect("/settings?lerr=no_pages#layout", 303);

    try {
      saveLayout(s.layoutPath, l);
    } catch (err) {
      return httpError((err as Error).message, 500);
    }
    await reload(s);
    return redirect("/settings#layout", 303);
  };

  const spotifyCredentials = async (req: Request): Promise<Response> => {
    if (req.method !== "POST") return httpError("POST only", 405);
    const form = await formValues(req);
    const kv: Record<string, string> = {};
    const id = (form.get("client_id") ?? "").trim();
    const secret = (form.get("client_secret") ?? "").trim();
    if (id && safeValue(id)) kv.spotify_client_id = id;
    if (secret && safeValue(secret)) kv.spotify_client_secret = secret;
    if (Object.keys(kv).length > 0) {
      try {
        setSecrets(s.secretsPath, kv);
      } catch (err) {
        return httpError((err as Error).message, 500);
      }
      st.status = null; // credentials changed, re-check on next view
      await reload(s);
    }
    return redirect("/settings", 303);
  };

  const spotifyAuthorize = (req: Request): Response => {
    if (req.method !== "GET") return httpError("GET only", 405);
    const secrets = readSecrets("spotify_client_id");
    if (!secrets?.spotify_client_id) return redirect("/settings?err=no_credentials", 302);

    const state = encodeBase64Url(crypto.getRandomValues(new Uint8Array(16)));
    st.pending = { state, expires: Date.now() + 5 * 60_000 };
    return redirect(st.spotify.authorizeURL(secrets.spotify_client_id, redirectURI(req), state, spotifyScope), 302);
  };

  const spotifyCallback = async (req: Request): Promise<Response> => {
    if (req.method !== "GET") return httpError("GET only", 405);
    const q = new URL(req.url).searchParams;
    if (q.get("error")) return redirect("/settings?err=denied", 302);

    const pending = st.pending;
    st.pending = null; // one-shot: valid for at most one callback
    const gotState = q.get("state") ?? "";
    if (!pending || !gotState || gotState !== pending.state || Date.now() > pending.expires) {
      return redirect("/settings?err=state_mismatch", 302);
    }

    const secrets = readSecrets("spotify_client_id", "spotify_client_secret");
    if (!secrets?.spotify_client_id || !secrets.spotify_client_secret) {
      return redirect("/settings?err=no_credentials", 302);
    }
    const creds = {
      clientId: secrets.spotify_client_id,
      clientSecret: secrets.spotify_client_secret,
      refreshToken: "",
    };

    let tr;
    try {
      tr = await st.spotify.exchangeCode(creds, q.get("code") ?? "", redirectURI(req), AbortSignal.timeout(20_000));
    } catch {
      return redirect("/settings?err=exchange_failed", 302);
    }

    let displayName = "";
    try {
      displayName = await st.spotify.whoAmI(tr.accessToken, AbortSignal.timeout(20_000)); // best-effort; checkStatus retries later
    } catch { /* blank is fine */ }
    try {
      setSecrets(s.secretsPath, { spotify_refresh_token: tr.refreshToken });
    } catch (err) {
      return httpError((err as Error).message, 500);
    }
    st.status = { displayName, err: "" };
    await reload(s);
    return redirect("/settings", 302);
  };

  const spotifyDisconnect = async (req: Request): Promise<Response> => {
    if (req.method !== "POST") return httpError("POST only", 405);
    // Clears only the refresh token — client_id/secret (the app registration)
    // stay, so reconnecting doesn't require re-pasting them. This doesn't
    // revoke access on Spotify's side; that's done from the user's Spotify
    // account "Apps" page.
    try {
      setSecrets(s.secretsPath, { spotify_refresh_token: "" });
    } catch (err) {
      return httpError((err as Error).message, 500);
    }
    st.status = null;
    await reload(s);
    return redirect("/settings", 303);
  };

  const secretsSet = async (req: Request): Promise<Response> => {
    if (req.method !== "POST") return httpError("POST only", 405);
    const form = await formValues(req);
    const key = (form.get("key") ?? "").trim();
    const value = form.get("value") ?? "";

    if (!secretKeyRe.test(key)) return redirect("/settings?serr=bad_key#secrets", 303);
    if (reservedSecretKeys.has(key)) return redirect("/settings?serr=reserved_key#secrets", 303);
    if (value === "") return redirect("/settings?serr=empty_value#secrets", 303);
    if (!safeValue(value)) return redirect("/settings?serr=bad_value#secrets", 303);

    try {
      setSecrets(s.secretsPath, { [key]: value });
    } catch (err) {
      return httpError((err as Error).message, 500);
    }
    await reload(s);
    return redirect("/settings#secrets", 303);
  };

  const secretsDelete = async (req: Request): Promise<Response> => {
    if (req.method !== "POST") return httpError("POST only", 405);
    const key = ((await formValues(req)).get("key") ?? "").trim();
    if (key && !reservedSecretKeys.has(key)) {
      try {
        deleteSecret(s.secretsPath, key);
      } catch (err) {
        return httpError((err as Error).message, 500);
      }
      await reload(s);
    }
    return redirect("/settings#secrets", 303);
  };

  return {
    "/settings": page,
    "/settings/spotify/credentials": spotifyCredentials,
    "/settings/spotify/authorize": spotifyAuthorize,
    "/settings/spotify/callback": spotifyCallback,
    "/settings/spotify/disconnect": spotifyDisconnect,
    "/settings/layout": layoutSet,
    "/settings/secrets/set": secretsSet,
    "/settings/secrets/delete": secretsDelete,
  };
}

function redirectURI(req: Request): string {
  return "http://" + (req.headers.get("host") ?? new URL(req.url).host) + "/settings/spotify/callback";
}
