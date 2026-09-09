// Progressive enhancement only. Without this file the dashboard still renders;
// it just won't tick times, switch tabs, refresh in place, or show the inline
// reader (the "read here" links still open as standalone pages).
(() => {
  "use strict";

  const root = document.documentElement;
  let openReaders = 0;
  let focused = -1;

  // ---- theme toggle -------------------------------------------------------
  const storedTheme = localStorage.getItem("quarks-theme");
  if (storedTheme) root.dataset.theme = storedTheme;

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
    let s = (Date.now() - new Date(iso).getTime()) / 1000;
    const future = s < 0;
    s = Math.abs(s);
    if (future && s < 3600) return "just now"; // small future offset = clock skew
    let v;
    if (s < 60) return "just now";
    if (s < 3600) v = Math.floor(s / 60) + "m";
    else if (s < 86400) v = Math.floor(s / 3600) + "h";
    else v = Math.floor(s / 86400) + "d";
    return future ? "in " + v : v + " ago";
  };
  const tickTimes = () => {
    document.querySelectorAll("time[datetime]").forEach((el) => {
      el.textContent = rel(el.getAttribute("datetime"));
    });
  };
  tickTimes();
  setInterval(tickTimes, 30000);

  // ---- tabs ----------------------------------------------------------
  const tabKey = (box) => "quarks-tab-" + box.dataset.box;

  function activateTab(box, idx) {
    const tabs = [...box.querySelectorAll(".box__tab")];
    const panels = [...box.querySelectorAll(".panel")];
    if (!tabs[idx]) idx = 0;
    tabs.forEach((t, i) => t.classList.toggle("is-active", i === idx));
    panels.forEach((p, i) => p.classList.toggle("is-active", i === idx));

    const active = tabs[idx];
    const status = box.querySelector(".box__status");
    if (active && status) {
      const badge = status.querySelector(".badge");
      const fresh = status.querySelector(".fresh");
      if (fresh) fresh.textContent = active.dataset.fresh || "";
      if (badge) {
        badge.textContent = active.dataset.badge || "";
        badge.hidden = !active.dataset.badge;
        badge.classList.toggle("badge--danger", active.dataset.danger === "1");
      }
    }
    try { localStorage.setItem(tabKey(box), String(idx)); } catch (_) {}
  }

  function bindTabs(scope) {
    scope.querySelectorAll(".box--tabbed").forEach((box) => {
      let saved = 0;
      try { saved = parseInt(localStorage.getItem(tabKey(box)) || "0", 10) || 0; } catch (_) {}
      activateTab(box, saved);
      box.querySelectorAll(".box__tab").forEach((tab, i) => {
        tab.addEventListener("click", () => activateTab(box, i));
      });
    });
  }
  bindTabs(document);

  // ---- page tabs (top-level) --------------------------------------
  const PAGE_KEY = "quarks-page";

  function activatePage(slug) {
    const pages = [...document.querySelectorAll(".page")];
    if (!pages.length) return;
    if (!pages.some((p) => p.dataset.page === slug)) slug = pages[0].dataset.page;
    pages.forEach((p) => p.classList.toggle("is-active", p.dataset.page === slug));
    document.querySelectorAll(".pagetab").forEach((t) => {
      t.classList.toggle("is-active", t.dataset.page === slug);
    });
    focused = -1;
    try { localStorage.setItem(PAGE_KEY, slug); } catch (_) {}
  }

  function bindPages() {
    let saved = null;
    try { saved = localStorage.getItem(PAGE_KEY); } catch (_) {}
    if (saved) activatePage(saved);
    document.querySelectorAll(".pagetab").forEach((t) => {
      t.addEventListener("click", () => activatePage(t.dataset.page));
    });
  }
  bindPages();

  // ---- manual refresh ----------------------------------------------
  async function forceRefresh(key, spinner) {
    if (spinner) spinner.classList.add("is-refreshing");
    document.querySelectorAll(".reader-panel").forEach((p) => p.remove());
    openReaders = 0;
    try {
      await fetch("/refresh", {
        method: "POST",
        body: key ? new URLSearchParams({ key }) : null,
      });
      await refresh(); // pull the fresh content into the page
    } catch (_) {
      /* ignore */
    }
    if (spinner) spinner.classList.remove("is-refreshing");
  }

  const refreshAllBtn = document.getElementById("refresh-all");
  if (refreshAllBtn) {
    refreshAllBtn.addEventListener("click", () => forceRefresh("", refreshAllBtn));
  }
  document.addEventListener("click", (e) => {
    const btn = e.target.closest(".box__refresh");
    if (!btn) return;
    const panel = btn.closest(".box").querySelector(".panel.is-active");
    forceRefresh(panel ? panel.dataset.key : "", btn);
  });

  // ---- open external links in the OS default browser -----------------
  // Only when running as a standalone app-window — a normal browser tab should
  // keep native behaviour.
  const standalone =
    window.matchMedia("(display-mode: standalone)").matches ||
    window.navigator.standalone === true;

  if (standalone) {
    document.addEventListener(
      "click",
      (e) => {
        const a = e.target.closest('a[target="_blank"]');
        if (!a || !/^https?:\/\//i.test(a.href)) return;
        e.preventDefault();
        fetch("/open", { method: "POST", body: new URLSearchParams({ url: a.href }) })
          .catch(() => window.open(a.href, "_blank", "noopener"));
      },
      true,
    );
  }

  // ---- inline reader --------------------------------------------------
  async function toggleReader(item) {
    if (!item) return;
    const readLink = item.querySelector(".item__read");
    if (!readLink) return;

    const next = item.nextElementSibling;
    if (next && next.classList.contains("reader-panel")) {
      next.remove();
      openReaders--;
      return;
    }

    const panel = document.createElement("li");
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

  // ---- keyboard nav (visible items only) ----------------------------
  const realItems = () =>
    [...document.querySelectorAll(".page.is-active .panel.is-active .item:not(.item--empty)")];

  function setFocus(next) {
    const list = realItems();
    if (!list.length) return;
    if (focused >= 0 && list[focused]) list[focused].classList.remove("is-focused");
    focused = Math.max(0, Math.min(next, list.length - 1));
    list[focused].classList.add("is-focused");
    list[focused].scrollIntoView({ block: "nearest" });
  }

  document.addEventListener("keydown", (e) => {
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    const tag = (e.target.tagName || "").toLowerCase();
    if (tag === "input" || tag === "textarea" || e.target.isContentEditable) return;

    const list = realItems();
    switch (e.key) {
      case "j": e.preventDefault(); setFocus(focused < 0 ? 0 : focused + 1); break;
      case "k": e.preventDefault(); setFocus(focused < 0 ? 0 : focused - 1); break;
      case "Enter":
        if (list[focused]) { e.preventDefault(); toggleReader(list[focused]); }
        break;
      case "o":
        if (list[focused]) {
          const a = list[focused].querySelector(".item__link");
          if (a) a.click();
        }
        break;
      case "Escape":
        document.querySelectorAll(".reader-panel").forEach((p) => p.remove());
        openReaders = 0;
        break;
    }
  });

  // ---- in-place refresh (keeps scroll + active page/tab) -----------
  let pagesEl = document.getElementById("pages");
  if (!pagesEl) return;
  const secs = parseInt(pagesEl.dataset.refresh || "0", 10);
  const sync = document.getElementById("lastsync");

  const showSync = () => {
    if (!sync) return;
    sync.hidden = false;
    sync.textContent = "synced " + new Date().toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  };

  let refreshing = false;
  async function refresh() {
    if (refreshing || openReaders > 0) return;
    refreshing = true;
    try {
      const scrolls = {};
      pagesEl.querySelectorAll(".panel[data-key]").forEach((p) => { scrolls[p.dataset.key] = p.scrollTop; });

      const html = await (await fetch(location.pathname, { cache: "no-store" })).text();
      const next = new DOMParser().parseFromString(html, "text/html").getElementById("pages");
      if (next) {
        pagesEl.replaceWith(next);
        pagesEl = next;
        focused = -1;
        bindTabs(pagesEl);
        let saved = null;
        try { saved = localStorage.getItem(PAGE_KEY); } catch (_) {}
        activatePage(saved);
        pagesEl.querySelectorAll(".panel[data-key]").forEach((p) => {
          if (scrolls[p.dataset.key] != null) p.scrollTop = scrolls[p.dataset.key];
        });
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
