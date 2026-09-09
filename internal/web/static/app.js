// Progressive enhancement only. The dashboard renders and works with this file
// absent — no ticking times, no in-place refresh, no inline reader (the "read
// here" links still work as standalone pages).
(() => {
  "use strict";

  // ---- theme toggle -------------------------------------------------------
  const root = document.documentElement;
  const stored = localStorage.getItem("quarks-theme");
  if (stored) root.dataset.theme = stored;

  const themeBtn = document.getElementById("theme-toggle");
  const paintTheme = () => { themeBtn.textContent = root.dataset.theme === "dark" ? "☀" : "☾"; };
  paintTheme();
  themeBtn.addEventListener("click", () => {
    root.dataset.theme = root.dataset.theme === "dark" ? "light" : "dark";
    localStorage.setItem("quarks-theme", root.dataset.theme);
    paintTheme();
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

  // ---- inline reader --------------------------------------------------
  let openReaders = 0;

  async function toggleReader(item) {
    if (!item) return;
    const readLink = item.querySelector(".item__read");
    if (!readLink) return;

    // a panel is stored as a sibling <li> right after the item
    let panel = item.nextElementSibling;
    if (panel && panel.classList.contains("reader-panel")) {
      panel.remove();
      openReaders--;
      return;
    }

    panel = document.createElement("li");
    panel.className = "reader-panel";
    panel.innerHTML = '<span class="reader-panel__loading">Extracting…</span>';
    item.after(panel);
    openReaders++;

    try {
      const html = await (await fetch(readLink.getAttribute("href"), { cache: "no-store" })).text();
      const article = new DOMParser().parseFromString(html, "text/html").querySelector(".reader__article");
      panel.innerHTML = article ? article.innerHTML : '<p class="reader__err">No content.</p>';
      panel.scrollIntoView({ block: "nearest", behavior: "smooth" });
    } catch (_) {
      panel.innerHTML = '<p class="reader__err">Couldn’t load the reader.</p>';
    }
  }

  document.addEventListener("click", (e) => {
    const rl = e.target.closest(".item__read");
    if (rl) {
      e.preventDefault();
      toggleReader(rl.closest(".item"));
    }
  });

  // ---- keyboard nav -------------------------------------------------
  let focused = -1;

  const realItems = () => [...document.querySelectorAll(".item:not(.item--empty):not(.reader-panel)")];

  function setFocus(next) {
    const list = realItems();
    if (!list.length) return;
    if (focused >= 0 && list[focused]) list[focused].classList.remove("is-focused");
    focused = Math.max(0, Math.min(next, list.length - 1));
    const el = list[focused];
    el.classList.add("is-focused");
    el.scrollIntoView({ block: "nearest" });
  }

  document.addEventListener("keydown", (e) => {
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    const tag = (e.target.tagName || "").toLowerCase();
    if (tag === "input" || tag === "textarea" || e.target.isContentEditable) return;

    const list = realItems();
    switch (e.key) {
      case "j":
        e.preventDefault();
        setFocus(focused < 0 ? 0 : focused + 1);
        break;
      case "k":
        e.preventDefault();
        setFocus(focused < 0 ? 0 : focused - 1);
        break;
      case "Enter":
        if (focused >= 0 && list[focused]) { e.preventDefault(); toggleReader(list[focused]); }
        break;
      case "o":
        if (focused >= 0 && list[focused]) {
          const a = list[focused].querySelector(".item__link");
          if (a) window.open(a.href, "_blank", "noopener");
        }
        break;
      case "Escape":
        document.querySelectorAll(".reader-panel").forEach((p) => { p.remove(); openReaders--; });
        openReaders = 0;
        break;
    }
  });

  // ---- in-place refresh --------------------------------------------
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
    if (refreshing || openReaders > 0) return; // don't yank the page while reading
    refreshing = true;
    try {
      const html = await (await fetch(location.pathname, { cache: "no-store" })).text();
      const next = new DOMParser().parseFromString(html, "text/html").querySelector(".grid");
      if (next) {
        grid.replaceWith(next);
        grid = next;
        focused = -1;
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
