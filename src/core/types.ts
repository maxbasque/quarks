// The normalized shapes every provider produces and the UI renders.

// Item is the normalized unit rendered on screen. Every feed-type provider
// returns a list of these. Weather and standings are deliberate exceptions and
// get their own types (see the plan, §6).
export interface Item {
  id: string; // stable, for dedupe + "seen" tracking
  title: string;
  url: string; // where a click goes
  source: string; // "Hacker News", "r/selfhosted", "Radio-Canada"
  author: string;
  publishedAt: Date | null;
  thumbnail: string;
  score: number; // upvotes / HN points; 0 if N/A
  comments: number;
  commentsUrl: string; // discussion link, distinct from url
  summary: string; // short plain-text excerpt shown under the title
  body: string; // full text, populated lazily by the reader view
  hero: boolean; // render thumbnail as a full-width lead image, not a side thumb
}

// item fills in the zero value for every field not given.
export function item(fields: Partial<Item>): Item {
  return {
    id: "",
    title: "",
    url: "",
    source: "",
    author: "",
    publishedAt: null,
    thumbnail: "",
    score: 0,
    comments: 0,
    commentsUrl: "",
    summary: "",
    body: "",
    hero: false,
    ...fields,
  };
}

// Weather is the payload for a weather widget. It deliberately does not reuse
// Item — forcing it into that shape would help nothing (plan §6).
export interface Weather {
  location: string;
  current: number; // °C
  feelsLike: number; // °C
  code: number; // WMO weather-interpretation code
  condition: string; // human label for code
  today: WeatherDay;
  forecast: WeatherDay[]; // upcoming days, today excluded
  observedAt: Date;
}

export interface WeatherDay {
  date: Date | null;
  high: number; // °C
  low: number; // °C
  code: number;
  condition: string;
}

// Standings is the payload for a sports-standings widget: one or more grouped
// tables (by division / conference / league). Like Weather, it does not fit the
// Item shape and gets its own renderer.
export interface Standings {
  groups: StandingsGroup[];
}

export interface StandingsGroup {
  name: string; // "Atlantic", "AL East", … ("" for a single flat table)
  columns: string[]; // right-aligned stat headers, parallel to each row's values
  rows: StandingsRow[];
}

export interface StandingsRow {
  rank: number;
  team: string;
  abbrev: string;
  logo: string;
  values: string[]; // parallel to the group's columns
  highlight: boolean; // e.g. the configured favourite team
}

// Payload is what one fetch produces. Feed widgets fill items; the handful of
// widgets that don't fit the Item shape fill their own field. Exactly one field
// is populated.
export interface Payload {
  items: Item[];
  weather?: Weather;
  standings?: Standings;
}

// feed is a convenience for the common case.
export function feed(items: Item[]): Payload {
  return { items };
}

// wmoCondition maps a WMO weather-interpretation code to a short label.
// https://open-meteo.com/en/docs — "Weather variable documentation".
export function wmoCondition(code: number): string {
  switch (code) {
    case 0:
      return "Dégagé";
    case 1:
      return "Généralement dégagé";
    case 2:
      return "Partiellement nuageux";
    case 3:
      return "Couvert";
    case 45:
    case 48:
      return "Brouillard";
    case 51:
    case 53:
    case 55:
      return "Bruine";
    case 56:
    case 57:
      return "Bruine verglaçante";
    case 61:
    case 63:
    case 65:
      return "Pluie";
    case 66:
    case 67:
      return "Pluie verglaçante";
    case 71:
    case 73:
    case 75:
      return "Neige";
    case 77:
      return "Neige en grains";
    case 80:
    case 81:
    case 82:
      return "Averses de pluie";
    case 85:
    case 86:
      return "Averses de neige";
    case 95:
      return "Orages";
    case 96:
    case 99:
      return "Orages avec grêle";
    default:
      return "—";
  }
}
