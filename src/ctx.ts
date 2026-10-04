// AbortSignal stands in for Go's context.Context. A signal made by
// withTimeout also remembers its deadline, so code that budgets its own work
// (the Spotify poll, MusicBrainz retries) can ask how much time is left — the
// one thing a plain AbortSignal can't tell you.

const deadlines = new WeakMap<AbortSignal, number>();

// withTimeout returns a signal that aborts when parent does or after ms,
// carrying the earlier of its own deadline and the parent's.
export function withTimeout(parent: AbortSignal, ms: number): AbortSignal {
  return withDeadline(parent, Date.now() + ms);
}

export function withDeadline(parent: AbortSignal, deadline: number): AbortSignal {
  const inherited = deadlines.get(parent);
  if (inherited !== undefined && inherited < deadline) deadline = inherited;
  const s = AbortSignal.any([parent, AbortSignal.timeout(Math.max(0, deadline - Date.now()))]);
  deadlines.set(s, deadline);
  return s;
}

// deadlineOf is the signal's deadline (epoch ms), or undefined if it has none.
export function deadlineOf(signal: AbortSignal): number | undefined {
  return deadlines.get(signal);
}

// timeLeft is how many ms remain before signal's deadline (Infinity if none).
export function timeLeft(signal: AbortSignal): number {
  const d = deadlines.get(signal);
  return d === undefined ? Infinity : d - Date.now();
}
