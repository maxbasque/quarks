// Current conditions and a short forecast from Open-Meteo — free, no signup,
// no API key (plan §11).

import { type Provider, Settings, type WidgetConfig } from "../core/provider.ts";
import { type Payload, type Weather, type WeatherDay, wmoCondition } from "../core/types.ts";
import { getJSON } from "../httpx.ts";

// newWeather is the core.Factory for "weather".
export function newWeather(cfg: WidgetConfig): Provider {
  const s = Settings.of(cfg);
  const lat = s.num("latitude");
  const lon = s.num("longitude");
  if (lat === 0 && lon === 0) {
    throw new Error(`weather widget ${JSON.stringify(cfg.title)}: latitude and longitude are required`);
  }
  const days = s.int("forecast_days");
  return new WeatherProvider(lat, lon, s.str("location") || cfg.title, days > 0 ? days : 4);
}

export class WeatherProvider implements Provider {
  endpoint = "https://api.open-meteo.com/v1/forecast";

  constructor(private lat: number, private lon: number, private location: string, private days: number) {}

  async fetch(signal: AbortSignal): Promise<Payload> {
    const q = new URLSearchParams({
      current: "temperature_2m,apparent_temperature,weather_code",
      daily: "weather_code,temperature_2m_max,temperature_2m_min",
      forecast_days: String(this.days + 1), // +1 so we keep N days after today
      latitude: this.lat.toFixed(4),
      longitude: this.lon.toFixed(4),
      timezone: "auto",
    });
    const data = await getJSON(`${this.endpoint}?${q}`, signal, { errPrefix: "open-meteo" });

    const cur = data.current ?? {};
    const daily = data.daily ?? {};
    const code = cur.weather_code ?? 0;
    const w: Weather = {
      location: this.location,
      current: cur.temperature_2m ?? 0,
      feelsLike: cur.apparent_temperature ?? 0,
      code,
      condition: wmoCondition(code),
      today: { date: null, high: 0, low: 0, code: 0, condition: "" },
      forecast: [],
      observedAt: new Date(),
    };
    const times: string[] = daily.time ?? [];
    times.forEach((t, i) => {
      const dayCode = daily.weather_code?.[i] ?? 0;
      const d = new Date(t + "T00:00:00Z"); // a calendar date: read it back in UTC
      const day: WeatherDay = {
        date: isNaN(d.getTime()) ? null : d,
        high: daily.temperature_2m_max?.[i] ?? 0,
        low: daily.temperature_2m_min?.[i] ?? 0,
        code: dayCode,
        condition: wmoCondition(dayCode),
      };
      if (i === 0) w.today = day;
      else w.forecast.push(day);
    });
    return { items: [], weather: w };
  }
}
