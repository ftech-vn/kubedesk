// Shared behavior for every page: the top bar, the ⌘K palette, copy buttons,
// scroll reveals and the table of contents on long pages.
(() => {
  const DMG = "https://pub-e3b2030c705649ef87d35925ea3331eb.r2.dev/Kube-Desk-latest-arm64.dmg";
  const REPO = "https://github.com/ftech-vn/kubedesk";

  document.documentElement.classList.remove("no-js");

  const icon = (d) =>
    `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${d}</svg>`;
  const I = {
    download: icon('<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" x2="12" y1="15" y2="3"/>'),
    hash: icon('<line x1="4" x2="20" y1="9" y2="9"/><line x1="4" x2="20" y1="15" y2="15"/><line x1="10" x2="8" y1="3" y2="21"/><line x1="16" x2="14" y1="3" y2="21"/>'),
    page: icon('<path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/>'),
    link: icon('<path d="M15 3h6v6"/><path d="M10 14 21 3"/><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/>'),
    copy: icon('<rect width="14" height="14" x="8" y="8" rx="2" ry="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/>'),
    check: icon('<path d="M20 6 9 17l-5-5"/>'),
  };

  // Everything the palette can jump to. Section anchors only show on the page that has them.
  const items = [
    { label: "Download Kube Desk for macOS", hint: ".dmg · Apple silicon", href: DMG, icon: I.download },
    { label: "Install guide", hint: "Download", href: "download.html#install", icon: I.page },
    { label: "Connect an AI agent", hint: "Download", href: "download.html#agents", icon: I.page },
    { label: "Build from source", hint: "Linux, Windows, Intel", href: "download.html#source", icon: I.page },
    { label: "Changelog", hint: "What's new in each release", href: "changelog.html", icon: I.page },
    { label: "Source code on GitHub", hint: "github.com", href: REPO, icon: I.link },
    { label: "All releases", hint: "github.com", href: REPO + "/releases", icon: I.link },
    { label: "Report a bug", hint: "github.com", href: REPO + "/issues/new?template=bug_report.md", icon: I.link },
  ];
  const sections = [...document.querySelectorAll("[data-palette-section]")].map((el) => ({
    label: el.dataset.paletteSection,
    hint: "On this page",
    href: "#" + el.id,
    icon: I.hash,
  }));
  items.splice(1, 0, ...sections);

  /* ---------- top bar ---------- */

  const bar = document.querySelector(".bar");
  if (bar) {
    const onScroll = () => bar.classList.toggle("is-stuck", window.scrollY > 8);
    onScroll();
    window.addEventListener("scroll", onScroll, { passive: true });
    const menu = bar.querySelector(".menu-btn");
    menu?.addEventListener("click", () => {
      const open = bar.classList.toggle("is-open");
      menu.setAttribute("aria-expanded", String(open));
    });
    bar.querySelectorAll(".links a").forEach((a) =>
      a.addEventListener("click", () => {
        bar.classList.remove("is-open");
        menu?.setAttribute("aria-expanded", "false");
      }),
    );
  }

  /* ---------- palette ---------- */

  const isMac = /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
  document.querySelectorAll("[data-mod]").forEach((el) => (el.textContent = isMac ? "⌘" : "Ctrl "));

  const pal = document.createElement("div");
  pal.className = "pal";
  pal.innerHTML = `
    <div class="pal__box" role="dialog" aria-modal="true" aria-label="Jump to">
      <div class="pal__in">
        ${icon('<circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>')}
        <input type="text" placeholder="Jump to a section, page or download…" aria-label="Jump to" aria-controls="pal-list" autocomplete="off" spellcheck="false">
        <kbd>esc</kbd>
      </div>
      <ul class="pal__list" id="pal-list" role="listbox"></ul>
      <div class="pal__foot"><span><kbd>↑</kbd> <kbd>↓</kbd> to move</span><span><kbd>↵</kbd> to open</span></div>
    </div>`;
  document.body.appendChild(pal);
  const input = pal.querySelector("input");
  const list = pal.querySelector(".pal__list");
  let shown = [];
  let sel = 0;
  let lastFocus = null;

  const render = () => {
    const q = input.value.trim().toLowerCase();
    shown = items.filter((it) => !q || (it.label + " " + it.hint).toLowerCase().includes(q));
    sel = Math.min(sel, Math.max(shown.length - 1, 0));
    list.innerHTML = shown.length
      ? shown
          .map(
            (it, i) =>
              `<li role="option" id="pal-${i}" data-i="${i}" aria-selected="${i === sel}">${it.icon}<span>${it.label}</span><small>${it.hint}</small></li>`,
          )
          .join("")
      : `<li class="pal__empty" role="presentation">Nothing matches “${input.value.replace(/[<>&]/g, "")}”. Try “download” or “agents”.</li>`;
    input.setAttribute("aria-activedescendant", shown.length ? "pal-" + sel : "");
    list.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: "nearest" });
  };
  const open = () => {
    lastFocus = document.activeElement;
    pal.classList.add("is-open");
    input.value = "";
    sel = 0;
    render();
    input.focus();
  };
  const close = () => {
    pal.classList.remove("is-open");
    lastFocus?.focus?.();
  };
  const go = (it) => {
    if (!it) return;
    close();
    if (it.href.startsWith("#")) {
      document.querySelector(it.href)?.scrollIntoView();
      history.replaceState(null, "", it.href);
    } else if (it.href.startsWith("http")) {
      window.open(it.href, it.href === DMG ? "_self" : "_blank", "noopener");
    } else {
      location.href = it.href;
    }
  };

  window.kdPalette = { open, isOpen: () => pal.classList.contains("is-open") };
  document.querySelectorAll("[data-palette]").forEach((b) => b.addEventListener("click", open));
  pal.addEventListener("click", (e) => {
    if (e.target === pal) close();
    const li = e.target.closest("li[data-i]");
    if (li) go(shown[+li.dataset.i]);
  });
  pal.addEventListener("mousemove", (e) => {
    const li = e.target.closest("li[data-i]");
    if (li && +li.dataset.i !== sel) {
      sel = +li.dataset.i;
      list.querySelectorAll("li[data-i]").forEach((n) => n.setAttribute("aria-selected", String(+n.dataset.i === sel)));
    }
  });
  input.addEventListener("input", () => {
    sel = 0;
    render();
  });
  input.addEventListener("keydown", (e) => {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      if (!shown.length) return;
      sel = (sel + (e.key === "ArrowDown" ? 1 : -1) + shown.length) % shown.length;
      render();
    } else if (e.key === "Enter") {
      e.preventDefault();
      go(shown[sel]);
    } else if (e.key === "Tab") {
      e.preventDefault();
    }
  });
  document.addEventListener("keydown", (e) => {
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
      e.preventDefault();
      pal.classList.contains("is-open") ? close() : open();
    } else if (e.key === "Escape" && pal.classList.contains("is-open")) {
      close();
    }
  });

  /* ---------- copy buttons ---------- */

  document.querySelectorAll("[data-copy]").forEach((btn) => {
    const label = btn.innerHTML;
    btn.addEventListener("click", async () => {
      const src = document.getElementById(btn.dataset.copy);
      const text = (src?.dataset.text ?? src?.innerText ?? "").replace(/^\$ /gm, "").trim();
      try {
        await navigator.clipboard.writeText(text);
        btn.innerHTML = I.check + "Copied";
        btn.classList.add("is-done");
      } catch {
        btn.textContent = "Select and copy";
      }
      setTimeout(() => {
        btn.innerHTML = label;
        btn.classList.remove("is-done");
      }, 1800);
    });
  });

  /* ---------- reveal on scroll ---------- */

  const reveals = document.querySelectorAll(".reveal");
  if ("IntersectionObserver" in window && !matchMedia("(prefers-reduced-motion: reduce)").matches) {
    const io = new IntersectionObserver(
      (entries) =>
        entries.forEach((en) => {
          if (en.isIntersecting) {
            en.target.classList.add("is-in");
            io.unobserve(en.target);
          }
        }),
      { rootMargin: "0px 0px -8% 0px", threshold: 0.08 },
    );
    reveals.forEach((el) => io.observe(el));
  } else {
    reveals.forEach((el) => el.classList.add("is-in"));
  }

  /* ---------- table of contents ---------- */

  const toc = document.querySelectorAll(".toc a[href^='#']");
  if (toc.length && "IntersectionObserver" in window) {
    const byId = new Map([...toc].map((a) => [a.getAttribute("href").slice(1), a]));
    const io = new IntersectionObserver(
      (entries) =>
        entries.forEach((en) => {
          if (!en.isIntersecting) return;
          toc.forEach((a) => a.classList.remove("is-active"));
          byId.get(en.target.id)?.classList.add("is-active");
        }),
      { rootMargin: "-20% 0px -70% 0px" },
    );
    byId.forEach((_, id) => {
      const el = document.getElementById(id);
      if (el) io.observe(el);
    });
  }
})();
