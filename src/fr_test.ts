import { assertEquals } from "@std/assert";
import * as fr from "./fr.ts";

Deno.test("formats", () => {
  const d = new Date(2026, 9, 10, 9, 5); // local time, as the dashboard shows it
  assertEquals(fr.day(d), "sam.");
  assertEquals(fr.date(d), "sam. 10 oct.");
  assertEquals(fr.dateTime(d), "sam. 10 oct., 9 h 05");
  assertEquals(fr.dayMonthYear(d), "10 oct. 2026");
  assertEquals(fr.monthYear(d), "oct. 2026");
  assertEquals(fr.clock(new Date(2026, 7, 2, 21, 30)), "21 h 30");
});

Deno.test("formats in a named time zone", () => {
  const d = new Date(Date.UTC(2026, 9, 11, 1, 5)); // 21 h 05 the evening before, in Montréal
  assertEquals(fr.dateTime(d, "America/Toronto"), "sam. 10 oct., 21 h 05");
  assertEquals(fr.day(new Date(Date.UTC(2026, 8, 9)), "UTC"), "mer.");
});
