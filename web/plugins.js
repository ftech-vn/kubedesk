// The plugin marketplace: the plugins of Kube Desk's registry on R2, to
// search, read about and install. The index is signed for the app, which
// checks it; the page only shows it. ?registry=<url> reads another one, such
// as a local copy while writing the page.
(() => {
  const REGISTRY = (new URLSearchParams(location.search).get("registry") || "https://pub-e3b2030c705649ef87d35925ea3331eb.r2.dev/plugins").replace(/\/$/, "");

  const grid = document.querySelector("[data-market-grid]");
  const detail = document.querySelector("[data-market-detail]");
  const search = document.querySelector("[data-market-search]");
  const tagsBox = document.querySelector("[data-market-tags]");
  const count = document.querySelector("[data-market-count]");

  let plugins = [];
  let tag = "";

  const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);
  const file = (rel) => (rel ? `${REGISTRY}/${rel.split("/").map(encodeURIComponent).join("/")}` : "");
  const needs = (engine) => {
    const e = String(engine || "").trim();
    return e.startsWith(">=") ? `${e.slice(2).trim()} or later` : e;
  };
  const installLink = (p) => `kubedesk://plugins/install?id=${encodeURIComponent(p.id)}`;

  const svg = (d) => `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${d}</svg>`;
  const I = {
    puzzle: svg('<path d="M15.39 4.39a1 1 0 0 0 1.68-.474 2.5 2.5 0 1 1 3.014 3.015 1 1 0 0 0-.474 1.68l1.683 1.682a2.414 2.414 0 0 1 0 3.414L19.61 15.39a1 1 0 0 1-1.68-.474 2.5 2.5 0 1 0-3.014 3.015 1 1 0 0 1 .474 1.68l-1.683 1.682a2.414 2.414 0 0 1-3.414 0L8.61 19.61a1 1 0 0 0-1.68.474 2.5 2.5 0 1 1-3.014-3.015 1 1 0 0 0 .474-1.68l-1.683-1.682a2.414 2.414 0 0 1 0-3.414L4.39 8.61a1 1 0 0 1 1.68.474 2.5 2.5 0 1 0 3.014-3.015 1 1 0 0 1-.474-1.68l1.683-1.682a2.414 2.414 0 0 1 3.414 0z"/>'),
    install: svg('<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" x2="12" y1="15" y2="3"/>'),
    back: svg('<path d="m12 19-7-7 7-7"/><path d="M19 12H5"/>'),
    link: svg('<path d="M15 3h6v6"/><path d="M10 14 21 3"/><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/>'),
    check: svg('<path d="M20 6 9 17l-5-5"/>'),
  };

  const iconOf = (p, cls = "pl-icon") =>
    p.icon ? `<img class="${cls}" src="${esc(file(p.icon))}" alt="" loading="lazy" />` : `<span class="${cls} pl-icon--none">${I.puzzle}</span>`;

  /* ---------- a small Markdown renderer for READMEs ---------- */

  // Everything is escaped first; only the Markdown below becomes markup,
  // and links only to http(s).
  function inline(s) {
    return esc(s)
      .replace(/`([^`]+)`/g, "<code>$1</code>")
      .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
      .replace(/(^|[\s(])[*_]([^*_]+)[*_](?=[\s).,;:!?]|$)/g, "$1<em>$2</em>")
      .replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g, '<a href="$2" rel="noopener nofollow">$1</a>');
  }

  function markdown(md) {
    const out = [];
    const lines = md.replace(/\r\n?/g, "\n").split("\n");
    let para = [];
    let list = null;
    const flush = () => {
      if (para.length) out.push(`<p>${inline(para.join(" "))}</p>`);
      para = [];
      if (list) out.push(`<${list.tag}>${list.items.map((i) => `<li>${inline(i)}</li>`).join("")}</${list.tag}>`);
      list = null;
    };
    for (let i = 0; i < lines.length; i++) {
      const line = lines[i];
      const fence = /^```/.exec(line);
      if (fence) {
        flush();
        const code = [];
        while (++i < lines.length && !/^```/.test(lines[i])) code.push(lines[i]);
        out.push(`<div class="code"><pre>${esc(code.join("\n"))}</pre></div>`);
        continue;
      }
      const h = /^(#{1,4})\s+(.*)$/.exec(line);
      if (h) {
        flush();
        const level = Math.min(4, h[1].length + 1);
        out.push(`<h${level}>${inline(h[2])}</h${level}>`);
        continue;
      }
      const li = /^\s*(?:([-*])|(\d+)\.)\s+(.*)$/.exec(line);
      if (li) {
        if (para.length) flush();
        const tag = li[1] ? "ul" : "ol";
        if (list && list.tag !== tag) flush();
        list ??= { tag, items: [] };
        list.items.push(li[3]);
        continue;
      }
      if (!line.trim()) {
        flush();
        continue;
      }
      if (list && /^\s+\S/.test(line)) {
        list.items[list.items.length - 1] += " " + line.trim();
        continue;
      }
      if (list) flush();
      para.push(line.trim());
    }
    flush();
    return out.join("\n");
  }

  /* ---------- the list ---------- */

  function matches(p, words) {
    const hay = [p.id, p.name, p.description, p.author, ...(p.keywords || []), ...(p.features || [])].join(" ").toLowerCase();
    return words.every((w) => hay.includes(w)) && (!tag || (p.keywords || []).includes(tag));
  }

  function renderTags() {
    const all = [...new Set(plugins.flatMap((p) => p.keywords || []))].sort();
    tagsBox.innerHTML = all
      .map((t) => `<button type="button" class="pl-tag${t === tag ? " is-on" : ""}" data-tag="${esc(t)}">${esc(t)}</button>`)
      .join("");
  }

  function card(p) {
    const v = p.versions[0];
    return `
      <article class="pl-card">
        <a class="pl-card__main" href="#/${encodeURIComponent(p.id)}">
          ${iconOf(p)}
          <div>
            <h3 class="pl-card__name">${esc(p.name)}</h3>
            <div class="pl-meta">${p.author ? `by ${esc(p.author)} · ` : ""}${esc(v.version)}</div>
          </div>
        </a>
        <p class="pl-card__desc">${esc(p.description)}</p>
        <div class="pl-card__tags">${(p.keywords || []).slice(0, 4).map((k) => `<span class="pl-chip">${esc(k)}</span>`).join("")}</div>
        <div class="pl-card__foot">
          <span class="pl-meta">Kube Desk ${esc(needs(v.engine))}</span>
          <a class="btn btn--ghost btn--sm" href="${installLink(p)}" data-install>${I.install}Install</a>
        </div>
      </article>`;
  }

  function renderList() {
    const words = search.value.toLowerCase().split(/\s+/).filter(Boolean);
    const shown = plugins.filter((p) => matches(p, words));
    count.textContent = `${shown.length} of ${plugins.length} plugin${plugins.length === 1 ? "" : "s"}`;
    grid.innerHTML = shown.length
      ? shown.map(card).join("")
      : `<div class="market__empty">${plugins.length ? "No plugin matches." : "No plugins yet."}</div>`;
  }

  /* ---------- a plugin ---------- */

  async function renderDetail(id) {
    const p = plugins.find((x) => x.id === id);
    if (!p) {
      location.hash = "";
      return;
    }
    const v = p.versions[0];
    const kb = Math.max(1, Math.round(v.size / 1024));
    detail.innerHTML = `
      <a class="pl-back" href="#find">${I.back}All plugins</a>
      <header class="pl-head">
        ${iconOf(p, "pl-icon pl-icon--lg")}
        <div class="pl-head__text">
          <h2 class="h2">${esc(p.name)}</h2>
          <p class="pl-meta">${p.author ? `by ${esc(p.author)} · ` : ""}<span class="mono">${esc(p.id)}</span></p>
          <p class="pl-head__desc">${esc(p.description)}</p>
        </div>
        <div class="pl-head__act">
          <a class="btn btn--primary" href="${installLink(p)}" data-install>${I.install}Install in Kube Desk</a>
          <span class="pl-meta">Needs Kube Desk ${esc(needs(v.engine))}</span>
        </div>
      </header>
      <div class="pl-body">
        <div class="prose pl-readme" data-readme>${p.readme ? '<p class="pl-meta">Loading…</p>' : ""}</div>
        <aside class="pl-side">
          ${p.features?.length ? `<h4>What it adds</h4><ul class="pl-features">${p.features.map((f) => `<li>${I.check}<span>${esc(f)}</span></li>`).join("")}</ul>` : ""}
          <h4>Details</h4>
          <dl class="pl-facts">
            <div><dt>Version</dt><dd>${esc(v.version)}</dd></div>
            <div><dt>Published</dt><dd>${esc((v.published || "").slice(0, 10))}</dd></div>
            <div><dt>Size</dt><dd>${kb} KB</dd></div>
            ${p.license ? `<div><dt>License</dt><dd>${esc(p.license)}</dd></div>` : ""}
            <div><dt>SHA-256</dt><dd class="mono pl-sha" title="${esc(v.sha256)}">${esc(v.sha256.slice(0, 16))}…</dd></div>
          </dl>
          <div class="pl-links">
            ${/^https?:\/\//.test(p.homepage || "") ? `<a href="${esc(p.homepage)}" rel="noopener">${I.link}Homepage</a>` : ""}
            <a href="${esc(file(v.archive))}">${I.install}Download .tgz</a>
          </div>
          ${p.versions.length > 1 ? `<h4>Versions</h4><ul class="pl-versions">${p.versions.map((x) => `<li><span>${esc(x.version)}</span><span class="pl-meta">Kube Desk ${esc(needs(x.engine))}</span></li>`).join("")}</ul>` : ""}
        </aside>
      </div>`;
    grid.hidden = true;
    document.querySelector(".market__bar").hidden = true;
    detail.hidden = false;
    document.title = `${p.name}: a Kube Desk plugin`;
    window.scrollTo({ top: document.querySelector(".market").offsetTop - 80 });
    if (p.readme) {
      const box = detail.querySelector("[data-readme]");
      try {
        const res = await fetch(file(p.readme));
        if (!res.ok) throw new Error(res.statusText);
        // The README's own title repeats the plugin's name.
        box.innerHTML = markdown((await res.text()).replace(/^#\s+.*\n+/, ""));
      } catch {
        box.innerHTML = "";
      }
    }
  }

  function route() {
    const m = /^#\/(.+)$/.exec(location.hash);
    if (m && plugins.length) return renderDetail(decodeURIComponent(m[1]));
    detail.hidden = true;
    grid.hidden = false;
    document.querySelector(".market__bar").hidden = false;
    document.title = "Plugins for Kube Desk";
  }

  /* ---------- loading ---------- */

  async function load() {
    grid.innerHTML = '<div class="market__empty">Loading plugins…</div>';
    try {
      const res = await fetch(`${REGISTRY}/index.json`, { cache: "no-cache" });
      if (res.status === 404) {
        plugins = [];
      } else {
        if (!res.ok) throw new Error(res.statusText);
        const doc = await res.json();
        plugins = (JSON.parse(doc.signed).plugins || []).filter((p) => p.versions?.length);
      }
      plugins.sort((a, b) => a.name.localeCompare(b.name));
      renderTags();
      renderList();
      route();
    } catch {
      grid.innerHTML = '<div class="market__empty">Could not load the plugins. <button type="button" class="link" data-retry>Try again</button></div>';
    }
  }

  search.addEventListener("input", renderList);
  tagsBox.addEventListener("click", (e) => {
    const b = e.target.closest("[data-tag]");
    if (!b) return;
    tag = tag === b.dataset.tag ? "" : b.dataset.tag;
    renderTags();
    renderList();
  });
  grid.addEventListener("click", (e) => e.target.closest("[data-retry]") && load());
  // A kubedesk:// link does nothing when Kube Desk is not installed: say so.
  document.addEventListener("click", (e) => {
    const a = e.target.closest("[data-install]");
    if (!a) return;
    const note = document.createElement("span");
    note.className = "pl-note";
    note.innerHTML = 'Opening Kube Desk… Nothing? <a href="download.html">Get Kube Desk 0.8 or later</a>.';
    a.parentElement.querySelector(".pl-note")?.remove();
    a.insertAdjacentElement("afterend", note);
    setTimeout(() => note.remove(), 8000);
  });
  window.addEventListener("hashchange", route);
  load();
})();
