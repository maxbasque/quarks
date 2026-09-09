// Progressive enhancement only. The dashboard renders and works with this file
// absent — it just won't tick relative times or refresh without a reload.
(() => {
  "use strict";

  // ---- theme toggle -------------------------------------------------------
  const root = document.documentElement;
  const stored = localStorage.getItem("quarks-theme");
  if (stored) root.dataset.theme = stored;

  const btn = document.getElementById("theme-toggle");
  const paint = () => { btn.textContent = root.dataset.theme === "dark" ? "☀" : "☾"; };
  paint();
  btn.addEventListener("click", () => {
    root.dataset.theme = root.dataset.theme === "dark" ? "light" : "dark";
    localStorage.setItem("quarks-theme", root.dataset.theme);
    paint();
  });

  // ---- relative timestamps ---------------------------------------------
  const rel = (iso) => {
    const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
    if (s < 60) return "just now";
    if (s < 3600) return Math.floor(s / 60) + "m ago";
    if (s < 86400) return Math.floor(s / 3600) + "h ago";
    return Math.floor(s / 86400) + "d ago";
  };
  const tickTimes = () => {
    document.querySelectorAll("time[datetime]").forEach((el) => {
      el.textContent = rel(el.getAttribute("datetime"));
    });
  };
  tickTimes();
  setInterval(tickTimes, 30000);

  // ---- in-place refresh ----------------------------------------------
  let grid = document.querySelector(".grid");
  if (!grid) return;
  const secs = parseInt(grid.dataset.refresh || "0", 10);
  const sync = document.getElementById("lastsync");

  const showSync = () => {
    if (!sync) return;
    sync.hidden = false;
    sync.textContent = "synced " + new Date().toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  };

  let refreshing = false;
  async function refresh() {
    if (refreshing) return;
    refreshing = true;
    try {
      const html = await (await fetch(location.pathname, { cache: "no-store" })).text();
      const next = new DOMParser().parseFromString(html, "text/html").querySelector(".grid");
      if (next) {
        grid.replaceWith(next);
        grid = next;
        tickTimes();
        showSync();
      }
    } catch (_) {
      /* offline; keep what we have */
    } finally {
      refreshing = false;
    }
  }

  if (secs > 0) setInterval(refresh, secs * 1000);
  document.addEventListener("visibilitychange", () => {
    if (!document.hidden) refresh();
  });
})();
