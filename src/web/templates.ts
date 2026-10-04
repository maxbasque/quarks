// The three pages: the dashboard, the reader, and settings. Markup and class
// names match the Go templates exactly — app.js and style.css depend on them.

import type { Item, Standings, Weather } from "../core/types.ts";
import * as fr from "../fr.ts";
import type { Article } from "../reader.ts";
import { html, query, raw, type Safe, url } from "./html.ts";

// ---- dashboard ---------------------------------------------------------------

export interface TabVM {
  key: string;
  title: string;
  items: Item[];
  weather: Weather | null;
  standings: Standings | null;
  badge: string; // "", "en retard · 14 min", "hors ligne"
  fresh: string; // "mis à jour il y a 3 min" / "jamais mis à jour"
  danger: boolean;
}

export interface BoxVM {
  title: string; // optional group label
  tabs: TabVM[];
  tabbed: boolean;
  danger: boolean;
}

export interface PageVM {
  name: string;
  slug: string;
  gridCols: Safe; // value for grid-template-columns, built from numbers only
  columns: BoxVM[][];
}

export interface IndexVM {
  theme: string;
  multiPage: boolean;
  pages: PageVM[];
}

export function indexPage(vm: IndexVM): string {
  return html`
    <!doctype html>
    <html lang="fr">
      <head>
        <meta charset="utf-8">
        <meta name="viewport" content="width=device-width, initial-scale=1">
        <title>Quark's</title>
        <link rel="icon" href="/static/favicon.svg">
        <link rel="icon" type="image/png" sizes="32x32" href="/static/favicon-32.png">
        <link rel="apple-touch-icon" href="/static/icon-192.png">
        <link rel="manifest" href="/manifest.webmanifest">
        <meta name="theme-color" content="#16171f">
        <link rel="preload" as="font" type="font/woff2" href="/static/fonts/IBMPlexSans-400.woff2" crossorigin>
        <link rel="stylesheet" href="/static/style.css">
        <script src="/static/app.js" defer></script>
      </head>
      <body>
        <header class="topbar">
        <div class="wordmark">Quark<span>'s</span></div>
        ${vm.multiPage &&
          html`<nav class="pagetabs" role="tablist">
      ${
            vm.pages.map((p, i) =>
              html`
                <button type="button" class="pagetab${i === 0 && " is-active"}" data-page="${p.slug}">${p.name}</button>
              `
            )
          }</nav>`}
        <div class="topbar__right">
          <span class="tick" id="lastsync" hidden></span>
          <button class="iconbtn" id="refresh-all" type="button" title="Tout actualiser" aria-label="Tout actualiser">↻</button>
          <a class="iconbtn" href="/settings" title="Paramètres" aria-label="Paramètres">⚙</a>
        </div>
      </header>

        <div id="pages" data-refresh="90">
        ${vm.pages.map((p, i) =>
          html`
            <main class="grid page${i === 0 && " is-active"}" data-page="${p.slug}" style="--grid-cols: ${p.gridCols}">
                  ${p.columns.map((col) => html`<div class="col">${col.map(box)}</div>`)}
                </main>
          `
        )}</div>
      </body>
    </html>
  `.value;
}

function box(b: BoxVM): Safe {
  const first = b.tabs[0];
  const head = b.tabbed
    ? html`
      <div class="box__headtop">
                    ${b.title && html`<span class="box__label">${b.title}</span>`}
                    ${boxStatus(first)}
                  </div>
      <div class="box__tabs" role="tablist">
                    ${b.tabs.map((t, i) =>
                      html`
                        <button type="button" class="box__tab${i === 0 && " is-active"}" role="tab"
                          data-tab="${i}" data-fresh="${t.fresh}" data-badge="${t.badge}"
                          ${t.danger && raw('data-danger="1"')}>${t.title}</button>
                      `
                    )}</div>
    `
    : html`<h2 class="box__title">${first.title}</h2>
            ${boxStatus(first)}`;
  return html`
    <section class="box${b.tabbed && " box--tabbed"}${b.danger && " box--danger"}"
      data-box="${first.key}">
      <header class="box__head">
        ${head}
      </header>
      <div class="box__panels">
        ${b.tabs.map((t, i) =>
          html`
            <div class="panel${i === 0 && " is-active"}" data-panel="${i}" data-key="${t.key}">
                          ${tabBody(t)}
                        </div>
          `
        )}</div>
    </section>
  `;
}

function boxStatus(t: TabVM): Safe {
  return html`
    <div class="box__status">
      <span class="badge${t.danger && " badge--danger"}" ${!t.badge && raw(" hidden")}>${t.badge}</span>
      <span class="fresh">${t.fresh}</span>
      <button class="box__refresh" type="button" title="Actualiser" aria-label="Actualiser">↻</button>
    </div>
  `;
}

function tabBody(t: TabVM): Safe {
  if (t.standings) return standings(t.standings);
  if (t.weather) return weather(t.weather);
  return html`<ul class="items">
    ${t.items.length ? t.items.map(itemRow) : html`<li class="item item--empty">Rien pour l’instant.</li>`}
  </ul>`;
}

function standings(st: Standings): Safe {
  return html`<div class="standings">
    ${
    st.groups.map((g) =>
      html`
        <div class="stgroup">
              ${g.name && html`<div class="stgroup__name">${g.name}</div>`}
              <table>
                <thead><tr>
                  <th class="st-rank"></th><th class="st-team"></th>
                  ${g.columns.map((c) => html`<th>${c}</th>`)}
                </tr></thead>
                <tbody>
                  ${g.rows.map((r) =>
                    html`
                      <tr${r.highlight && raw(' class="is-me"')}>
                                  <td class="st-rank">${r.rank}</td>
                                  <td class="st-team">
                                    ${r.logo &&
                                      html`<img src="${
                                        url(r.logo)
                                      }" alt="" loading="lazy" referrerpolicy="no-referrer">`}
                                    <span>${r.team}</span>
                                  </td>
                                  ${r.values.map((v) => html`<td>${v}</td>`)}
                                </tr>
                    `
                  )}</tbody>
              </table>
            </div>
      `
    )
  }</div>`;
}

// temp rounds a Celsius value to a whole-degree string like "15°".
export function temp(c: number): string {
  return roundHalfAway(c) + "°";
}

// roundHalfAway rounds half away from zero, as Go's math.Round does
// (Math.round sends -2.5 to -2).
function roundHalfAway(x: number): number {
  return Math.sign(x) * Math.round(Math.abs(x)) || 0;
}

function weather(w: Weather): Safe {
  return html`<div class="weather">
    <div class="weather__now">
      <span class="weather__icon">${weatherIcon(w.code)}</span>
      <span class="weather__temp">${temp(w.current)}</span>
      <span class="weather__desc">
        ${w.condition}
        <span class="weather__sub">ressenti ${temp(w.feelsLike)} · aujourd’hui ${temp(w.today.low)} / ${
    temp(w.today.high)
  }</span>
      </span>
    </div>
    ${
    w.forecast.length > 0 &&
    html`<ul class="weather__days">
      ${
      w.forecast.map((d) =>
        html`
          <li>
            <span class="weather__day">${d.date ? fr.day(d.date, "UTC") : ""}</span>
            <span class="weather__dayicon">${weatherIcon(d.code)}</span>
            <span class="weather__hl">${temp(d.high)}<span class="weather__lo">${temp(d.low)}</span></span>
          </li>
        `
      )
    }</ul>`
  }
  </div>`;
}

function itemRow(it: Item): Safe {
  let media: Safe | false = false;
  if (it.hero && it.thumbnail) {
    media = html`
      <a href="${url(it.url)}" target="_blank"
        rel="noopener noreferrer"><img class="item__hero" src="${url(
          it.thumbnail,
        )}" alt="" loading="lazy" referrerpolicy="no-referrer"></a>
    `;
  } else if (it.thumbnail) {
    media = html`<img class="item__thumb" src="${
      url(it.thumbnail)
    }" alt="" loading="lazy" referrerpolicy="no-referrer">`;
  }
  return html`
    <li class="item${it.hero && " item--hero"}">
          ${media}
          <div class="item__body">
            <a class="item__link" href="${url(it.url)}" target="_blank" rel="noopener noreferrer">${it.title}</a>
            ${it.summary && html`<p class="item__summary">${it.summary}</p>`}
            <div class="item__meta">
              ${it.source && html`<span class="item__source">${it.source}</span>`}
              ${it.author && html`<span>${it.author}</span>`}
              ${it.publishedAt && html`<time datetime="${rfc3339(it.publishedAt)}">${ago(it.publishedAt)}</time>`}
              ${it.score > 0 && html`<span>▲ ${it.score}</span>`}
              ${it.comments > 0 &&
                html`<a class="item__comments" href="${
                  url(it.commentsUrl || it.url)
                }" target="_blank" rel="noopener noreferrer">${it.comments} commentaires</a>`}
              ${!it.hero && html`<a class="item__read" href="/reader?url=${query(it.url)}">lire ici</a>`}
            </div>
          </div>
        </li>
  `;
}

// rfc3339 formats a time in UTC with second precision ("2026-10-04T13:05:00Z").
function rfc3339(d: Date): string {
  return d.toISOString().replace(/\.\d{3}Z$/, "Z");
}

// ago renders an item timestamp, past or future ("il y a 3 h" / "dans 2 j");
// client JS keeps it current after load.
export function ago(t: Date): string {
  let d = Date.now() - t.getTime();
  const future = d < 0;
  if (future) d = -d;
  if (future && d < 3_600_000) return "à l'instant"; // small future offset = clock skew, not a real schedule
  let v: string;
  if (d < 60_000) return "à l'instant";
  else if (d < 3_600_000) v = Math.floor(d / 60_000) + " min";
  else if (d < 86_400_000) v = Math.floor(d / 3_600_000) + " h";
  else v = Math.floor(d / 86_400_000) + " j";
  return future ? "dans " + v : "il y a " + v;
}

// weatherIcon maps a WMO code to an emoji.
export function weatherIcon(code: number): string {
  if (code === 0) return "☀️";
  if (code <= 2) return "🌤️";
  if (code === 3) return "☁️";
  if (code >= 45 && code <= 48) return "🌫️";
  if (code >= 51 && code <= 57) return "🌦️";
  if (code >= 61 && code <= 67) return "🌧️";
  if (code >= 71 && code <= 77) return "🌨️";
  if (code >= 80 && code <= 82) return "🌧️";
  if (code >= 85 && code <= 86) return "🌨️";
  if (code >= 95) return "⛈️";
  return "•";
}

// ---- reader ------------------------------------------------------------------

export interface ReaderVM {
  article: Article | null;
  err: string;
  url: string;
}

// readerPage renders one extracted article. It works as a standalone page (a
// plain link, no JS) and as a fragment the dashboard pulls in — the markup is
// the same either way; app.js lifts the <article> out.
export function readerPage(vm: ReaderVM): string {
  const a = vm.article;
  let body: Safe;
  if (vm.err) {
    body = html`<p class="reader__err">Impossible d’extraire cette page.<br><span>${vm.err}</span></p>`;
  } else {
    body = html`${a?.title && html`<h1 class="reader__title">${a.title}</h1>`}
      ${
      (a?.byline || a?.siteName) &&
      html`<p class="reader__byline">${a.byline}${a.byline && a.siteName && " · "}${a.siteName}</p>`
    }
      <div class="reader__content">${raw(a?.html ?? "")}</div>`;
  }
  return html`
    <!doctype html>
    <html lang="fr">
      <head>
        <meta charset="utf-8">
        <meta name="viewport" content="width=device-width, initial-scale=1">
        <title>${a?.title || "Lecteur"} · Quark's</title>
        <link rel="icon" href="/static/favicon.svg">
        <link rel="stylesheet" href="/static/style.css">
      </head>
      <body class="reader-page">
        <article class="reader__article" data-reader data-url="${url(vm.url)}">
        <div class="reader__bar">
          <a href="/" class="reader__back">← tableau de bord</a>
          <a href="${url(vm.url)}" target="_blank" rel="noopener noreferrer">ouvrir l’original ↗</a>
        </div>
        ${body}
      </article>
      </body>
    </html>
  `.value;
}

// ---- settings ----------------------------------------------------------------

// SettingsVM is what the settings page renders. sections drives both the
// left-hand nav and, in the same order, which cards appear in the main column
// — each section id matches a <section id="..."> the nav links to.
export interface SettingsVM {
  sections: { id: string; label: string; status: string }[];
  layout: LayoutSettingsVM;
  spotify: SpotifySettingsVM;
  secrets: { keys: string[]; error: string };
}

export interface LayoutSettingsVM {
  pages: LayoutPageVM[];
  error: string;
}

export interface LayoutPageVM {
  name: string;
  enabled: boolean;
  max: number;
  shown: number;
  columns: { name: string; enabled: boolean; missingSecrets: string[] }[];
}

export interface SpotifySettingsVM {
  clientIdSet: boolean;
  clientSecretSet: boolean;
  connected: boolean;
  displayName: string;
  error: string;
  redirectURI: string;
  mediaSnippet: string;
}

export function settingsPage(vm: SettingsVM): string {
  const sp = vm.spotify;
  const layout = vm.layout.pages.length > 0 &&
    html`<section id="layout" class="settings__card">
        <h2 class="settings__card-title">Pages et colonnes</h2>
        <p class="settings__status">Choisissez les pages affichées dans la barre du haut, et les colonnes de chacune.</p>
        ${vm.layout.error && html`<p class="settings__status settings__status--err">${vm.layout.error}</p>`}

        <form method="post" action="/settings/layout" class="settings__layout">
          ${
      vm.layout.pages.map((p) =>
        html`
          <div class="settings__pagegroup${!p.enabled && " settings__pagegroup--hidden"}">
                      <label class="settings__check settings__check--page">
                        <input type="checkbox" name="page" value="${p.name}" data-page${p.enabled && raw(" checked")}>
                        <span>${p.name}</span>
                      </label>
                      ${p.columns.length > 0 &&
                        html`<div class="settings__cols" data-max="${p.max}">
              <p class="settings__layout-max">Colonnes — jusqu’à ${p.max}</p>
              ${
                          p.columns.map((c) =>
                            html`
                              <label class="settings__check">
                                              <input type="checkbox" name="cols.${p.name}" value="${c
                                                .name}"${c.enabled && raw(" checked")}>
                                              <span>${c.name}</span>
                                              ${c.missingSecrets.length > 0 &&
                                                html`<span class="settings__check-note">requiert ${
                                                  c.missingSecrets.map((k, i) =>
                                                    html`${i > 0 && ", "}<code>${k}</code>`
                                                  )
                                                } — ajoutez-la sous <a href="#secrets">Clés secrètes</a></span>`}
                                            </label>
                            `
                          )
                        }</div>`}
                    </div>
        `
      )
    }<noscript><button class="settings__btn" type="submit">Enregistrer</button></noscript>
        </form>
      </section>`;

  return html`
    <!doctype html>
    <html lang="fr">
      <head>
        <meta charset="utf-8">
        <meta name="viewport" content="width=device-width, initial-scale=1">
        <title>Paramètres · Quark's</title>
        <link rel="icon" href="/static/favicon.svg">
        <link rel="stylesheet" href="/static/style.css">
      </head>
      <body class="settings-page">
        <div class="settings__shell">
          <nav class="settings__nav">
            <a href="/" class="settings__back">← tableau de bord</a>
            <div class="settings__navlinks">
            ${vm.sections.map((s) =>
              html`
                <a href="#${s.id}" class="settings__navlink">
                  <span class="settings__navlabel">${s.label}</span>
                  <span class="settings__navstatus">${s.status}</span>
                </a>
              `
            )}</div>
          </nav>

          <main class="settings__main">
          <h1 class="settings__title">Paramètres</h1>

          ${layout}

          <section id="spotify" class="settings__card">
            <h2 class="settings__card-title">Spotify — sorties à venir</h2>
            <p class="settings__status${sp.connected && " settings__status--ok"}">${spotifyStatusLine(sp)}</p>
            ${sp.error && html`<p class="settings__status settings__status--err">${sp.error}</p>`}

            <form method="post" action="/settings/spotify/credentials">
              <div class="settings__field">
                <label for="spotify_client_id">Client ID${sp.clientIdSet &&
                  " (configuré — laissez vide pour le conserver)"}</label>
                <input id="spotify_client_id" type="text" name="client_id" autocomplete="off" spellcheck="false">
              </div>
              <div class="settings__field">
                <label for="spotify_client_secret">Client Secret${sp.clientSecretSet &&
                  " (configuré — laissez vide pour le conserver)"}</label>
                <input id="spotify_client_secret" type="password" name="client_secret" autocomplete="off">
              </div>
              <button class="settings__btn" type="submit">Enregistrer</button>
            </form>

            <p class="settings__status">
              Créez une application sur
              <a href="https://developer.spotify.com/dashboard" target="_blank" rel="noopener noreferrer">developer.spotify.com/dashboard</a>
              et enregistrez exactement cette adresse de redirection (redirect URI) :
            </p>
            <p class="settings__snippet">${sp.redirectURI}</p>

            ${sp.clientIdSet && sp.clientSecretSet &&
              html`
                <p>
                  <a class="settings__btn settings__btn--link" href="/settings/spotify/authorize">
                            ${sp.connected ? "Reconnecter" : "Connecter"} Spotify
                          </a>
                </p>
              `}

            ${sp.connected &&
              html`
                <form method="post" action="/settings/spotify/disconnect">
                  <button class="settings__btn" type="submit">Déconnecter</button>
                </form>

                <p class="settings__status">Pour ajouter un onglet Media, collez ceci dans <code>config.yaml</code> :</p>
                <p class="settings__snippet">${sp.mediaSnippet}</p>
              `}
          </section>

          <section id="secrets" class="settings__card">
            <h2 class="settings__card-title">Clés secrètes</h2>
            <p class="settings__status">
              Tout ce qu’un module de <code>config.yaml</code> appelle par
              <code>${"${secret:clé}"}</code> — l’adresse de votre fil Reddit, un lien de
              calendrier, etc. Les identifiants Spotify sont dans la section ci-dessus.
            </p>
            ${vm.secrets.error && html`<p class="settings__status settings__status--err">${vm.secrets.error}</p>`}

            ${vm.secrets.keys.length > 0
              ? html`<ul class="settings__secretlist">
          ${
                vm.secrets.keys.map((k) =>
                  html`
                    <li class="settings__secretrow">
                      <code>${k}</code>
                      <form method="post" action="/settings/secrets/delete">
                        <input type="hidden" name="key" value="${k}">
                        <button class="settings__btn settings__btn--danger" type="submit">Retirer</button>
                      </form>
                    </li>
                  `
                )
              }</ul>`
              : html`<p class="settings__status">Aucune autre clé pour l’instant.</p>`}

            <form method="post" action="/settings/secrets/set">
              <div class="settings__field">
                <label for="secret_key">Clé</label>
                <input id="secret_key" type="text" name="key" placeholder="reddit_home" autocomplete="off" spellcheck="false">
              </div>
              <div class="settings__field">
                <label for="secret_value">Valeur</label>
                <input id="secret_value" type="password" name="value" autocomplete="off">
              </div>
              <button class="settings__btn" type="submit">Ajouter / modifier</button>
            </form>
            <p class="settings__status">Ajouter une clé qui existe déjà remplace sa valeur.</p>
          </section>
        </main>
        </div>
        <script>${raw(settingsScript)}</script>
      </body>
    </html>
  `.value;
}

export function spotifyStatusLine(sp: SpotifySettingsVM): string {
  if (sp.connected && sp.displayName) return "Connecté : " + sp.displayName;
  if (sp.connected) return "Connecté";
  if (sp.clientIdSet && sp.clientSecretSet) return "Application inscrite — non connectée";
  return "Non configuré";
}

const settingsScript = `
    // Pages: any number, but the last shown one can't be unchecked.
    // Columns: per page, unchecked boxes gray out at that page's limit and the
    // last checked one can't be unchecked. Every change saves the whole form.
    document.querySelectorAll('.settings__layout').forEach(function (form) {
      var all = form.querySelectorAll('input[type=checkbox]');
      function guard(boxes, max) {
        var n = 0;
        boxes.forEach(function (b) { if (b.checked) n++; });
        boxes.forEach(function (b) {
          b.disabled = b.checked ? n <= 1 : n >= max;
          b.parentElement.classList.toggle('settings__check--off', b.disabled);
        });
      }
      guard(form.querySelectorAll('input[data-page]'), Infinity);
      form.querySelectorAll('.settings__cols').forEach(function (g) {
        guard(g.querySelectorAll('input[type=checkbox]'), +g.dataset.max);
      });
      all.forEach(function (b) {
        b.addEventListener('change', function () {
          all.forEach(function (x) { x.disabled = false; }); // disabled boxes aren't submitted
          form.submit();
        });
      });
    });
  `;
