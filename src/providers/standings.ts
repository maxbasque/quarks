// Live league standings — NHL or MLB — as grouped tables. Both APIs are public
// and keyless.

import { type Provider, Settings, type WidgetConfig } from "../core/provider.ts";
import type { Payload, Standings, StandingsGroup, StandingsRow } from "../core/types.ts";
import { browserUserAgent, getJSON } from "../httpx.ts";

// newStandings is the core.Factory for "standings".
export function newStandings(cfg: WidgetConfig): Provider {
  const s = Settings.of(cfg);
  const league = s.str("league").trim().toLowerCase(); // nhl | mlb
  if (league !== "nhl" && league !== "mlb") {
    throw new Error(`standings widget ${JSON.stringify(cfg.title)}: league must be nhl or mlb`);
  }
  return new StandingsProvider(
    league,
    s.str("group").trim().toLowerCase() || "division", // division | conference | league (nhl only)
    s.str("team").trim().toUpperCase(), // optional: highlight this team's row
  );
}

// nhlGroupNames puts the API's division and conference names in French.
const nhlGroupNames: Record<string, string> = {
  Atlantic: "Atlantique",
  Metropolitan: "Métropolitaine",
  Central: "Centrale",
  Pacific: "Pacifique",
  Eastern: "Association de l’Est",
  Western: "Association de l’Ouest",
};

const mlbDivisions: Record<number, { name: string; order: number }> = {
  201: { name: "AL Est", order: 0 },
  202: { name: "AL Centrale", order: 1 },
  200: { name: "AL Ouest", order: 2 },
  204: { name: "NL Est", order: 3 },
  205: { name: "NL Centrale", order: 4 },
  203: { name: "NL Ouest", order: 5 },
};

export class StandingsProvider implements Provider {
  nhlURL = "https://api-web.nhle.com/v1/standings/now";
  mlbURL = "https://statsapi.mlb.com/api/v1/standings?leagueId=103,104&standingsTypes=regularSeason";

  constructor(private league: string, private group: string, private highlight: string) {}

  fetch(signal: AbortSignal): Promise<Payload> {
    return this.league === "mlb" ? this.#fetchMLB(signal) : this.#fetchNHL(signal);
  }

  async #get(url: string, signal: AbortSignal, label: string) {
    try {
      return await getJSON(url, signal, { ua: browserUserAgent });
    } catch (err) {
      throw new Error(`${label}: ${(err as Error).message}`);
    }
  }

  // ---- NHL ---------------------------------------------------------------

  async #fetchNHL(signal: AbortSignal): Promise<Payload> {
    const data = await this.#get(this.nhlURL, signal, "nhl standings");

    const groups = new Map<string, { seq: number; r: StandingsRow }[]>();
    for (const t of data.standings ?? []) {
      let name: string, seq: number;
      switch (this.group) {
        case "conference":
          [name, seq] = [t.conferenceName ?? "", t.conferenceSequence ?? 0];
          break;
        case "league":
          [name, seq] = ["", t.leagueSequence ?? 0];
          break;
        default:
          [name, seq] = [t.divisionName ?? "", t.divisionSequence ?? 0];
      }
      name = nhlGroupNames[name] ?? name;
      const streak = t.streakCode && t.streakCount > 0 ? t.streakCode + t.streakCount : "";
      const abbrev = t.teamAbbrev?.default ?? "";
      if (!groups.has(name)) groups.set(name, []);
      groups.get(name)!.push({
        seq,
        r: {
          rank: 0,
          team: t.teamCommonName?.default ?? "",
          abbrev,
          logo: t.teamLogo ?? "",
          values: [
            String(t.gamesPlayed ?? 0),
            `${t.wins ?? 0}-${t.losses ?? 0}-${t.otLosses ?? 0}`,
            String(t.points ?? 0),
            streak,
          ],
          highlight: abbrev === this.highlight,
        },
      });
    }

    const st: Standings = { groups: [] };
    for (const name of [...groups.keys()].sort()) {
      const rs = groups.get(name)!.sort((a, b) => a.seq - b.seq);
      const g: StandingsGroup = { name, columns: ["PJ", "V-D-DP", "PTS", "SÉQ"], rows: [] };
      rs.forEach((r, i) => g.rows.push({ ...r.r, rank: i + 1 }));
      st.groups.push(g);
    }
    return { items: [], standings: st };
  }

  // ---- MLB ---------------------------------------------------------------

  async #fetchMLB(signal: AbortSignal): Promise<Payload> {
    const data = await this.#get(this.mlbURL, signal, "mlb standings");
    const order = (r: any) => mlbDivisions[r.division?.id]?.order ?? 0;
    const records = [...(data.records ?? [])].sort((a, b) => order(a) - order(b));

    const st: Standings = { groups: [] };
    for (const rec of records) {
      const g: StandingsGroup = {
        name: mlbDivisions[rec.division?.id]?.name ?? "",
        columns: ["V-D", "PCT", "ÉCART", "SÉQ"],
        rows: [],
      };
      const rows = [...(rec.teamRecords ?? [])].sort((a, b) => atoi(a.divisionRank) - atoi(b.divisionRank));
      rows.forEach((t: any, i: number) => {
        const name = t.team?.name ?? "";
        const gb = t.gamesBack === "-" ? "—" : (t.gamesBack ?? "");
        g.rows.push({
          rank: i + 1,
          team: name,
          abbrev: name,
          logo: `https://www.mlbstatic.com/team-logos/${t.team?.id ?? 0}.svg`,
          values: [
            `${t.wins ?? 0}-${t.losses ?? 0}`,
            (t.winningPercentage ?? "").replace(/^0/, ""),
            gb,
            t.streak?.streakCode ?? "",
          ],
          highlight: name.toLowerCase() === this.highlight.toLowerCase(),
        });
      });
      st.groups.push(g);
    }
    return { items: [], standings: st };
  }
}

function atoi(s: unknown): number {
  const n = typeof s === "string" && /^[+-]?\d+$/.test(s) ? parseInt(s, 10) : 0;
  return n;
}
