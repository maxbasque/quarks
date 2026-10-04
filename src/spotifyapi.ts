// A thin, dependency-free client for the pieces of Spotify's accounts/Web API
// this app needs: the confidential-client Authorization Code OAuth flow, the
// current user's identity, and their followed artists. Release data comes from
// MusicBrainz instead (see musicbrainz.ts): Spotify's catalog rarely lists a
// record before it's out, and polling it per artist is what got this app rate
// limited. It knows nothing about providers or the HTTP settings handlers —
// both depend on this module, not the other way around.

import { encodeBase64 } from "@std/encoding/base64";
import { trim } from "./httpx.ts";

// Credentials is what's needed to talk to Spotify on a user's behalf: the app
// registration (clientId/clientSecret, from the Spotify Developer dashboard)
// plus that user's long-lived refresh token from a completed Authorization
// Code exchange.
export interface Credentials {
  clientId: string;
  clientSecret: string;
  refreshToken: string;
}

// Artist is a followed artist, as returned by followedArtists.
export interface Artist {
  id: string;
  name: string;
}

// RateLimitedError is a 429 from the Web API. retryAfter (ms) is Spotify's own
// Retry-After — which can be many hours once an app has been flagged, not just
// the few seconds of the rolling-window limit.
export class RateLimitedError extends Error {
  constructor(readonly retryAfter: number) {
    super(`spotify: rate limited, retry after ${formatDuration(retryAfter)}`);
  }
}

// UnauthorizedError is a 401 from the Web API — the access token was rejected
// (expired or revoked), so the caller should drop any cached one.
export class UnauthorizedError extends Error {
  constructor(detail: string) {
    super(`spotify: access token rejected: ${detail}`);
  }
}

export interface TokenResponse {
  refreshToken: string;
  accessToken: string;
  expiresIn: number; // seconds
}

// Client talks to Spotify's accounts + Web API. authBase/apiBase are
// overridable so tests can point them at a local fixture server.
export class Client {
  authBase = "https://accounts.spotify.com";
  apiBase = "https://api.spotify.com/v1";
  timeout = 15_000;

  // authorizeURL builds the URL to send the user's browser to for consent.
  authorizeURL(clientId: string, redirectURI: string, state: string, scope: string): string {
    const q = new URLSearchParams({
      client_id: clientId,
      redirect_uri: redirectURI,
      response_type: "code",
      scope,
      state,
    });
    return this.authBase + "/authorize?" + q;
  }

  // exchangeCode redeems an authorization code (from the callback's ?code=)
  // for a refresh token and an initial access token.
  exchangeCode(creds: Credentials, code: string, redirectURI: string, signal: AbortSignal) {
    return this.#token(creds, { code, grant_type: "authorization_code", redirect_uri: redirectURI }, signal);
  }

  // refreshAccessToken mints a new short-lived access token from creds'
  // refreshToken. On this confidential-client flow Spotify does not rotate the
  // refresh token, so the caller's stored token stays valid.
  async refreshAccessToken(
    creds: Credentials,
    signal: AbortSignal,
  ): Promise<{ accessToken: string; expiresIn: number }> {
    const tr = await this.#token(
      creds,
      { grant_type: "refresh_token", refresh_token: creds.refreshToken },
      signal,
    );
    return { accessToken: tr.accessToken, expiresIn: tr.expiresIn };
  }

  async #token(creds: Credentials, form: Record<string, string>, signal: AbortSignal): Promise<TokenResponse> {
    const resp = await fetch(this.authBase + "/api/token", {
      method: "POST",
      headers: {
        "Content-Type": "application/x-www-form-urlencoded",
        Authorization: "Basic " + encodeBase64(`${creds.clientId}:${creds.clientSecret}`),
      },
      body: new URLSearchParams(form).toString(),
      signal: AbortSignal.any([signal, AbortSignal.timeout(this.timeout)]),
    });
    const body = await resp.text();
    if (resp.status !== 200) throw new Error(`spotify token: http ${resp.status}: ${trim(body)}`);
    let tr: any;
    try {
      tr = JSON.parse(body);
    } catch (err) {
      throw new Error(`spotify token: parse response: ${(err as Error).message}`);
    }
    return {
      refreshToken: tr.refresh_token ?? "",
      accessToken: tr.access_token ?? "",
      expiresIn: tr.expires_in ?? 0,
    };
  }

  // whoAmI returns the display name of the account an access token belongs to.
  async whoAmI(accessToken: string, signal: AbortSignal): Promise<string> {
    const me = await this.#get(accessToken, this.apiBase + "/me", signal);
    return me?.display_name ?? "";
  }

  // followedArtists returns one page of the user's followed artists. Pass an
  // empty after for the first page, then the returned next cursor until it
  // comes back empty.
  async followedArtists(
    accessToken: string,
    after: string,
    signal: AbortSignal,
  ): Promise<{ artists: Artist[]; next: string }> {
    const q = new URLSearchParams({ limit: "50", type: "artist" });
    if (after) q.set("after", after);
    q.sort();
    const page = await this.#get(accessToken, this.apiBase + "/me/following?" + q, signal);
    const items: any[] = page?.artists?.items ?? [];
    return {
      artists: items.map((a) => ({ id: a.id ?? "", name: a.name ?? "" })),
      next: page?.artists?.cursors?.after ?? "",
    };
  }

  // get does an authenticated GET. There's deliberately no inline retry: a 429
  // comes back as a RateLimitedError for the caller to honor across polls (see
  // the provider's blockedUntil).
  async #get(accessToken: string, endpoint: string, signal: AbortSignal): Promise<any> {
    const resp = await fetch(endpoint, {
      headers: { Authorization: "Bearer " + accessToken },
      signal: AbortSignal.any([signal, AbortSignal.timeout(this.timeout)]),
    });

    if (resp.status === 429) {
      await resp.body?.cancel();
      const h = resp.headers.get("retry-after") ?? "";
      let secs = /^\d+$/.test(h) ? parseInt(h, 10) : NaN;
      if (isNaN(secs)) secs = 5; // no usable Retry-After header — a conservative guess
      throw new RateLimitedError(secs * 1000);
    }

    const body = await resp.text();
    if (resp.status === 401) throw new UnauthorizedError(`http 401 on ${endpoint}: ${trim(body)}`);
    if (resp.status !== 200) throw new Error(`spotify: http ${resp.status} on ${endpoint}: ${trim(body)}`);
    try {
      return JSON.parse(body);
    } catch (err) {
      throw new Error(`spotify: parse response from ${endpoint}: ${(err as Error).message}`);
    }
  }
}

// formatDuration renders ms the way Go prints a time.Duration ("20h0m0s").
export function formatDuration(ms: number): string {
  let s = Math.round(ms / 1000);
  const h = Math.floor(s / 3600);
  s -= h * 3600;
  const m = Math.floor(s / 60);
  s -= m * 60;
  if (h > 0) return `${h}h${m}m${s}s`;
  if (m > 0) return `${m}m${s}s`;
  return `${s}s`;
}
