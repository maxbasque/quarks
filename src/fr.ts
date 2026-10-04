// Dates the way the dashboard shows them, in Québec French
// ("sam. 10 oct., 9 h 05"), in the machine's local time zone unless a time
// zone is given.

const days = ["dim.", "lun.", "mar.", "mer.", "jeu.", "ven.", "sam."];
const months = ["janv.", "févr.", "mars", "avr.", "mai", "juin", "juil.", "août", "sept.", "oct.", "nov.", "déc."];

interface Parts {
  year: number;
  month: number; // 1-12
  day: number;
  weekday: number; // 0 = Sunday
  hour: number;
  minute: number;
}

const formatters = new Map<string, Intl.DateTimeFormat>();

// parts splits d into calendar fields, in timeZone (an IANA name) or local
// time when it's undefined.
export function parts(d: Date, timeZone?: string): Parts {
  if (!timeZone) {
    return {
      year: d.getFullYear(),
      month: d.getMonth() + 1,
      day: d.getDate(),
      weekday: d.getDay(),
      hour: d.getHours(),
      minute: d.getMinutes(),
    };
  }
  let f = formatters.get(timeZone);
  if (!f) {
    f = new Intl.DateTimeFormat("en-US", {
      timeZone,
      hourCycle: "h23",
      year: "numeric",
      month: "numeric",
      day: "numeric",
      weekday: "short",
      hour: "numeric",
      minute: "numeric",
    });
    formatters.set(timeZone, f);
  }
  const p = Object.fromEntries(f.formatToParts(d).map((x) => [x.type, x.value]));
  return {
    year: +p.year,
    month: +p.month,
    day: +p.day,
    weekday: ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"].indexOf(p.weekday),
    hour: +p.hour % 24,
    minute: +p.minute,
  };
}

// validTimeZone reports whether tz is an IANA zone this runtime knows.
export function validTimeZone(tz: string): boolean {
  if (!tz) return false;
  try {
    new Intl.DateTimeFormat("en-US", { timeZone: tz });
    return true;
  } catch {
    return false;
  }
}

// day is the abbreviated weekday: "lun.".
export function day(d: Date, tz?: string): string {
  return days[parts(d, tz).weekday];
}

// date is the weekday and date: "sam. 10 oct.".
export function date(d: Date, tz?: string): string {
  const p = parts(d, tz);
  return `${days[p.weekday]} ${p.day} ${months[p.month - 1]}`;
}

// dateTime adds the 24-hour time: "sam. 10 oct., 9 h 05".
export function dateTime(d: Date, tz?: string): string {
  return `${date(d, tz)}, ${clock(d, tz)}`;
}

// clock is the 24-hour time: "9 h 05".
export function clock(d: Date, tz?: string): string {
  const p = parts(d, tz);
  return `${p.hour} h ${String(p.minute).padStart(2, "0")}`;
}

// dayMonthYear is "13 oct. 2026".
export function dayMonthYear(d: Date): string {
  return `${d.getDate()} ${months[d.getMonth()]} ${d.getFullYear()}`;
}

// monthYear is "oct. 2026".
export function monthYear(d: Date): string {
  return `${months[d.getMonth()]} ${d.getFullYear()}`;
}
