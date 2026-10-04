// The Formula 1 season from Jolpica (the community-run successor to the Ergast
// API): the race calendar, or the driver or constructor standings. Public and
// keyless.

import { type Provider, Settings, type WidgetConfig } from "../core/provider.ts";
import { feed, type Item, item, type Payload, type StandingsGroup } from "../core/types.ts";
import * as fr from "../fr.ts";
import { browserUserAgent, readLimited } from "../httpx.ts";
import { parseRFC3339 } from "./nhl.ts";

// newF1 is the core.Factory for "f1".
export function newF1(cfg: WidgetConfig): Provider {
  const s = Settings.of(cfg);
  const mode = s.str("mode").trim().toLowerCase() || "schedule"; // schedule | drivers | constructors
  if (!["schedule", "drivers", "constructors"].includes(mode)) {
    throw new Error(`f1 widget ${JSON.stringify(cfg.title)}: mode must be schedule, drivers or constructors`);
  }
  // highlight marks a row in the standings: a driver code or surname ("STR",
  // "Stroll") or a team name ("Aston Martin"). Case-insensitive.
  return new F1(mode, s.str("highlight").trim().toLowerCase());
}

interface Session {
  date?: string;
  time?: string;
}

// sessionAt is the session start; a missing time (some early-season entries)
// means the date alone, at midnight UTC.
function sessionAt(s: Session | undefined): Date | null {
  if (!s?.date) return null;
  return parseRFC3339(s.date + "T" + (s.time || "00:00:00Z"));
}

// raceLength is how long after lights-out a race still counts as upcoming, so
// this weekend's race stays at the top until it's over.
const raceLength = 2 * 3_600_000;

export class F1 implements Provider {
  base = "https://api.jolpi.ca/ergast/f1/current";
  now = () => new Date();

  constructor(private mode: string, private highlight: string) {}

  fetch(signal: AbortSignal): Promise<Payload> {
    switch (this.mode) {
      case "drivers":
        return this.#fetchDrivers(signal);
      case "constructors":
        return this.#fetchConstructors(signal);
      default:
        return this.#fetchSchedule(signal);
    }
  }

  async #get(path: string, signal: AbortSignal): Promise<any> {
    const resp = await fetch(this.base + path, {
      headers: { "User-Agent": browserUserAgent },
      signal: AbortSignal.any([signal, AbortSignal.timeout(20_000)]),
    });
    if (resp.status !== 200) {
      await resp.body?.cancel();
      throw new Error(`http ${resp.status}`);
    }
    return JSON.parse(new TextDecoder().decode(await readLimited(resp, 16 << 20)));
  }

  // fetchSchedule lists the races still to run, soonest first, then the ones
  // already run, most recent first, each with its winner when known.
  async #fetchSchedule(signal: AbortSignal): Promise<Payload> {
    let sched;
    try {
      sched = await this.#get(".json", signal);
    } catch (err) {
      throw new Error(`f1 schedule: ${(err as Error).message}`);
    }
    // winners are a nicety: the calendar still shows if this one fails
    const winners = new Map<string, string>();
    try {
      const res = await this.#get("/results/1.json?limit=100", signal);
      for (const r of res?.MRData?.RaceTable?.Races ?? []) {
        const w = r.Results?.[0];
        if (w) {
          winners.set(
            r.round,
            `${w.Driver?.givenName ?? ""} ${w.Driver?.familyName ?? ""} (${w.Constructor?.name ?? ""})`,
          );
        }
      }
    } catch { /* no winners this time */ }

    const now = this.now().getTime();
    const upcoming: Item[] = [], past: Item[] = [];
    for (const r of sched?.MRData?.RaceTable?.Races ?? []) {
      const start = sessionAt(r);
      const where = `${r.Circuit?.Location?.locality ?? ""}, ${r.Circuit?.Location?.country ?? ""}`;
      const it = item({
        id: "f1-" + r.round,
        title: r.raceName ?? "",
        url: r.url ?? "",
        source: "Manche " + r.round,
        publishedAt: start,
      });
      if ((start?.getTime() ?? -Infinity) + raceLength > now) {
        const sessions = [
          { name: "Qualifs", at: sessionAt(r.Qualifying) },
          { name: "Sprint", at: sessionAt(r.Sprint) },
          { name: "Course", at: start },
        ].sort((a, b) => (a.at?.getTime() ?? -Infinity) - (b.at?.getTime() ?? -Infinity));
        const parts = sessions.filter((s) => s.at).map((s) => s.name + " " + fr.dateTime(s.at!));
        it.summary = where + " · " + parts.join(" · ");
        upcoming.push(it);
        continue;
      }
      it.summary = where;
      const w = winners.get(r.round);
      if (w) it.summary = "🏁 " + w + " · " + it.summary;
      past.push(it);
    }
    const t = (it: Item) => it.publishedAt?.getTime() ?? -Infinity;
    upcoming.sort((a, b) => t(a) - t(b));
    past.sort((a, b) => t(b) - t(a));
    return feed([...upcoming, ...past]);
  }

  async #fetchDrivers(signal: AbortSignal): Promise<Payload> {
    let data;
    try {
      data = await this.#get("/driverStandings.json", signal);
    } catch (err) {
      throw new Error(`f1 driver standings: ${(err as Error).message}`);
    }
    const g: StandingsGroup = { name: "", columns: ["ÉCURIE", "PTS", "V"], rows: [] };
    const list = data?.MRData?.StandingsTable?.StandingsLists?.[0];
    if (list) {
      g.name = "Après la manche " + list.round;
      (list.DriverStandings ?? []).forEach((d: any, i: number) => {
        const cons = d.Constructors ?? [];
        const team = cons.length ? cons[cons.length - 1].name ?? "" : ""; // a mid-season move lists the current team last
        g.rows.push({
          rank: rank(d.position, i),
          team: d.Driver?.familyName ?? "",
          abbrev: d.Driver?.code ?? "",
          logo: "",
          values: [team, d.points ?? "", d.wins ?? ""],
          highlight: this.#matches(d.Driver?.code ?? "", d.Driver?.familyName ?? "", team),
        });
      });
    }
    return { items: [], standings: { groups: [g] } };
  }

  async #fetchConstructors(signal: AbortSignal): Promise<Payload> {
    let data;
    try {
      data = await this.#get("/constructorStandings.json", signal);
    } catch (err) {
      throw new Error(`f1 constructor standings: ${(err as Error).message}`);
    }
    const g: StandingsGroup = { name: "", columns: ["PTS", "V"], rows: [] };
    const list = data?.MRData?.StandingsTable?.StandingsLists?.[0];
    if (list) {
      g.name = "Après la manche " + list.round;
      (list.ConstructorStandings ?? []).forEach((c: any, i: number) => {
        const name = c.Constructor?.name ?? "";
        g.rows.push({
          rank: rank(c.position, i),
          team: name,
          abbrev: "",
          logo: "",
          values: [c.points ?? "", c.wins ?? ""],
          highlight: this.#matches(name),
        });
      });
    }
    return { items: [], standings: { groups: [g] } };
  }

  #matches(...names: string[]): boolean {
    return this.highlight !== "" && names.some((n) => n.toLowerCase() === this.highlight);
  }
}

// rank is the API's position, or the list order when it's missing (a driver
// who hasn't been classified yet).
function rank(pos: unknown, i: number): number {
  return typeof pos === "string" && /^[+-]?\d+$/.test(pos) ? parseInt(pos, 10) : i + 1;
}
