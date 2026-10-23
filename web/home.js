// The home page: the live app window in the hero, and the small interactive
// mocks further down (guardrail confirm, agent permissions, screenshot tabs).
(() => {
  const reduce = matchMedia("(prefers-reduced-motion: reduce)").matches;
  const icon = (d) =>
    `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${d}</svg>`;
  const I = {
    home: icon('<path d="m3 9 9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/><polyline points="9 22 9 12 15 12 15 22"/>'),
    plus: icon('<path d="M5 12h14"/><path d="M12 5v14"/>'),
    overview: icon('<path d="m12 14 4-4"/><path d="M3.34 19a10 10 0 1 1 17.32 0"/>'),
    nodes: icon('<rect width="20" height="8" x="2" y="2" rx="2"/><rect width="20" height="8" x="2" y="14" rx="2"/><line x1="6" x2="6.01" y1="6" y2="6"/><line x1="6" x2="6.01" y1="18" y2="18"/>'),
    events: icon('<path d="M22 12h-4l-3 9L9 3l-3 9H2"/>'),
    pods: icon('<path d="M21 8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16Z"/><path d="m3.3 7 8.7 5 8.7-5"/><path d="M12 22V12"/>'),
    deployments: icon('<path d="m12.83 2.18a2 2 0 0 0-1.66 0L2.6 6.08a1 1 0 0 0 0 1.83l8.58 3.91a2 2 0 0 0 1.66 0l8.58-3.9a1 1 0 0 0 0-1.83Z"/><path d="m22 17.65-9.17 4.16a2 2 0 0 1-1.66 0L2 17.65"/><path d="m22 12.65-9.17 4.16a2 2 0 0 1-1.66 0L2 12.65"/>'),
    services: icon('<path d="M9 3H5a2 2 0 0 0-2 2v4m6-6h10a2 2 0 0 1 2 2v4M9 3v18m0 0h10a2 2 0 0 0 2-2V9M9 21H5a2 2 0 0 1-2-2V9m0 0h18"/>'),
    search: icon('<circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>'),
    chevron: icon('<path d="m6 9 6 6 6-6"/>'),
    x: icon('<path d="M18 6 6 18"/><path d="m6 6 12 12"/>'),
    unplug: icon('<path d="m19 5 3-3"/><path d="m2 22 3-3"/><path d="M6.3 20.3a2.4 2.4 0 0 0 3.4 0L12 18l-6-6-2.3 2.3a2.4 2.4 0 0 0 0 3.4Z"/><path d="M7.5 13.5 10 11"/><path d="M10.5 16.5 13 14"/><path d="m12 6 6 6 2.3-2.3a2.4 2.4 0 0 0 0-3.4l-2.6-2.6a2.4 2.4 0 0 0-3.4 0Z"/>'),
  };
  const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
  const sfx = (n = 5) => Array.from({ length: n }, () => "bcdfghjklmnpqrstvwxz2456789"[(Math.random() * 27) | 0]).join("");

  /* ---------- data ---------- */

  const now = () => Date.now() / 1000;
  const ago = (s) => now() - s;
  const pod = (name, ns, status, restarts, node, cpu, ageS, image) => ({ name, ns, status, restarts, node, cpu, born: ago(ageS), image });
  const D = 86400;
  const H = 3600;

  const clusters = [
    {
      id: "pe",
      name: "production-eu",
      color: "#8b5cf6",
      version: "v1.34.1+k3s1",
      server: "https://api.prod-eu.example.com:6443",
      tags: ["kubeconfig", "current context"],
      cap: [["CPU requested", "4.82 / 36", 13], ["Memory requested", "9.6Gi / 26.4Gi", 36], ["CPU used", "1.94 / 36", 5], ["Memory used", "7.1Gi / 26.4Gi", 27]],
      nodes: [
        ["prod-control-1", "control-plane", "6%", "21%", 41 * D],
        ["prod-worker-1", "worker", "9%", "34%", 41 * D],
        ["prod-worker-2", "worker", "4%", "29%", 41 * D],
      ],
      pods: [
        pod("checkout-7f9c6d5b8-2xkqp", "shop", "Running", 0, "prod-worker-1", "42m", 2 * D, "checkout:2.4.1"),
        pod("checkout-7f9c6d5b8-h7vns", "shop", "Running", 0, "prod-worker-2", "38m", 2 * D, "checkout:2.4.1"),
        pod("cart-5d8b9c7f4-q9wzl", "shop", "Running", 0, "prod-worker-1", "21m", 5 * D, "cart:1.12.0"),
        pod("api-6b7f8d9c5-mx2rt", "shop", "Running", 1, "prod-worker-2", "64m", 5 * D, "api:1.9.0"),
        pod("ledger-sync-644d96db59-pj7lp", "payments", "CrashLoopBackOff", 6, "prod-worker-1", "0m", 18 * 60, "ledger-sync:3.2.0"),
        pod("ledger-api-58c9f7d6b-wq4ns", "payments", "Running", 0, "prod-worker-2", "55m", 12 * D, "ledger-api:3.2.0"),
        pod("ingress-nginx-controller-6d8f9-kz8p2", "ingress-nginx", "Running", 0, "prod-control-1", "12m", 41 * D, "ingress-nginx/controller:v1.13.1"),
        pod("prometheus-0", "monitoring", "Running", 0, "prod-worker-1", "180m", 41 * D, "prometheus:v3.5.0"),
      ],
      deploys: [
        ["checkout", "shop", "checkout:2.4.1", 2 * D],
        ["cart", "shop", "cart:1.12.0", 5 * D],
        ["api", "shop", "api:1.9.0", 5 * D],
        ["ledger-sync", "payments", "ledger-sync:3.2.0", 18 * 60],
        ["ledger-api", "payments", "ledger-api:3.2.0", 12 * D],
        ["ingress-nginx-controller", "ingress-nginx", "controller:v1.13.1", 41 * D],
      ],
      services: [
        ["checkout", "shop", "ClusterIP", "10.43.12.7", "80/TCP"],
        ["cart", "shop", "ClusterIP", "10.43.88.140", "80/TCP"],
        ["api", "shop", "ClusterIP", "10.43.40.3", "8080/TCP"],
        ["ledger-api", "payments", "ClusterIP", "10.43.201.9", "8443/TCP"],
        ["ingress-nginx-controller", "ingress-nginx", "LoadBalancer", "10.43.0.21", "80/TCP, 443/TCP"],
      ],
      events: [
        ["Warning", "BackOff", "pod/ledger-sync-644d96db59-pj7lp", "payments", "Back-off restarting failed container ledger-sync in pod ledger-sync-644d96db59-pj7lp", 137, 26],
        ["Warning", "FailedGetResourceMetric", "horizontalpodautoscaler/api", "shop", "failed to get cpu utilization: unable to get metrics for resource cpu", 477, 4],
        ["Normal", "ScalingReplicaSet", "deployment/checkout", "shop", "Scaled up replica set checkout-7f9c6d5b8 to 2", 2 * D, 1],
        ["Normal", "Pulled", "pod/api-6b7f8d9c5-mx2rt", "shop", 'Container image "registry.example.com/api:1.9.0" already present on machine', 5 * D, 1],
      ],
    },
    {
      id: "st",
      name: "staging",
      color: "#6366f1",
      version: "v1.34.1+k3s1",
      server: "https://api.staging.example.com:6443",
      tags: ["kubeconfig"],
      cap: [["CPU requested", "2.10 / 24", 9], ["Memory requested", "4.4Gi / 17.6Gi", 25], ["CPU used", "0.71 / 24", 3], ["Memory used", "3.0Gi / 17.6Gi", 17]],
      nodes: [
        ["stg-1", "control-plane", "3%", "18%", 63 * D],
        ["stg-2", "worker", "4%", "22%", 63 * D],
        ["stg-3", "worker", "2%", "12%", 9 * D],
      ],
      pods: [
        pod("checkout-86b5d7c9f-tq2ml", "shop", "Running", 0, "stg-2", "19m", 3 * H, "checkout:2.5.0"),
        pod("cart-6f4c8d9b7-wbx5n", "shop", "Running", 0, "stg-3", "11m", 1 * D, "cart:1.13.0-rc.1"),
        pod("api-7c9f6b8d4-r2kvz", "shop", "Running", 0, "stg-2", "33m", 3 * H, "api:1.10.0"),
        pod("ledger-sync-5b8d7f6c9-hn4pd", "payments", "Running", 0, "stg-3", "8m", 6 * H, "ledger-sync:3.3.0"),
        pod("ledger-api-7d6c5b9f8-m8zqs", "payments", "Running", 0, "stg-2", "24m", 6 * H, "ledger-api:3.3.0"),
        pod("web-preview-pr-482-6c7d8-xq9fd", "previews", "Running", 0, "stg-3", "6m", 47 * 60, "web:pr-482"),
      ],
      deploys: [
        ["checkout", "shop", "checkout:2.5.0", 3 * H],
        ["cart", "shop", "cart:1.13.0-rc.1", 1 * D],
        ["api", "shop", "api:1.10.0", 3 * H],
        ["ledger-sync", "payments", "ledger-sync:3.3.0", 6 * H],
        ["ledger-api", "payments", "ledger-api:3.3.0", 6 * H],
        ["web-preview-pr-482", "previews", "web:pr-482", 47 * 60],
      ],
      services: [
        ["checkout", "shop", "ClusterIP", "10.43.9.214", "80/TCP"],
        ["cart", "shop", "ClusterIP", "10.43.77.18", "80/TCP"],
        ["api", "shop", "ClusterIP", "10.43.31.90", "8080/TCP"],
        ["web-preview-pr-482", "previews", "ClusterIP", "10.43.150.6", "3000/TCP"],
      ],
      events: [
        ["Normal", "ScalingReplicaSet", "deployment/checkout", "shop", "Scaled up replica set checkout-86b5d7c9f to 1", 3 * H, 1],
        ["Normal", "Created", "pod/web-preview-pr-482-6c7d8-xq9fd", "previews", "Created container web", 47 * 60, 1],
        ["Normal", "Pulled", "pod/api-7c9f6b8d4-r2kvz", "shop", 'Successfully pulled image "registry.example.com/api:1.10.0" in 2.31s', 3 * H, 1],
      ],
    },
    {
      id: "qa",
      name: "qa",
      color: "#f97316",
      version: "v1.33.4+k3s1",
      server: "https://api.qa.example.com:6443",
      tags: ["kubeconfig"],
      cap: [["CPU requested", "1.35 / 8", 17], ["Memory requested", "2.2Gi / 7.7Gi", 29], ["CPU used", "0.40 / 8", 5], ["Memory used", "1.6Gi / 7.7Gi", 21]],
      nodes: [["qa-1", "control-plane", "5%", "21%", 12 * D]],
      pods: [
        pod("web-5c9d8f7b6-" + sfx(), "qa", "Running", 0, "qa-1", "14m", 4 * H, "web:1.41.0"),
        pod("web-5c9d8f7b6-" + sfx(), "qa", "Running", 0, "qa-1", "12m", 4 * H, "web:1.41.0"),
        pod("api-74d9c8b6f-p5k2w", "qa", "Running", 0, "qa-1", "27m", 1 * D, "api:1.10.0"),
        pod("postgres-0", "qa", "Running", 0, "qa-1", "48m", 12 * D, "postgres:17.6"),
        { ...pod("e2e-runner-29341760-8vx4d", "qa", "Completed", 0, "qa-1", "0m", 52 * 60, "e2e:main"), job: true },
      ],
      deploys: [
        ["web", "qa", "web:1.41.0", 4 * H],
        ["api", "qa", "api:1.10.0", 1 * D],
      ],
      services: [
        ["web", "qa", "ClusterIP", "10.43.5.61", "80/TCP"],
        ["api", "qa", "ClusterIP", "10.43.5.92", "8080/TCP"],
        ["postgres", "qa", "ClusterIP", "None", "5432/TCP"],
      ],
      events: [
        ["Normal", "SuccessfulCreate", "job/e2e-runner-29341760", "qa", "Created pod: e2e-runner-29341760-8vx4d", 52 * 60, 1],
        ["Normal", "Completed", "job/e2e-runner-29341760", "qa", "Job completed", 41 * 60, 1],
      ],
    },
    {
      id: "es",
      name: "edge-singapore",
      color: "#ec4899",
      server: "https://api.edge-sg.example.com:6443",
      tags: ["kubeconfig"],
      error: 'Get "https://api.edge-sg.example.com:6443/version": dial tcp: lookup api.edge-sg.example.com: no such host',
    },
  ];
  const byId = Object.fromEntries(clusters.map((c) => [c.id, c]));

  const pages = [
    { id: "overview", label: "Overview", icon: I.overview },
    { group: "Cluster" },
    { id: "nodes", label: "Nodes", icon: I.nodes },
    { id: "events", label: "Events", icon: I.events },
    { group: "Workloads" },
    { id: "pods", label: "Pods", icon: I.pods },
    { id: "deployments", label: "Deployments", icon: I.deployments },
    { group: "Network" },
    { id: "services", label: "Services", icon: I.services },
  ];

  // What each cluster remembers while you are away: its page and open object.
  const state = { at: "pe", per: Object.fromEntries(clusters.map((c) => [c.id, { page: "overview", open: null }])) };

  /* ---------- formatting ---------- */

  const age = (born) => {
    const s = Math.max(0, Math.floor(now() - born));
    if (s < 120) return s + "s";
    if (s < 3600) return Math.floor(s / 60) + "m" + (s < 600 ? (s % 60) + "s" : "");
    if (s < D) return Math.floor(s / 3600) + "h" + (s < 36000 ? Math.floor((s % 3600) / 60) + "m" : "");
    return Math.floor(s / D) + "d";
  };
  const badge = (status) => {
    const cls = { Running: "", Ready: "", Completed: "idle", Pending: "warn", ContainerCreating: "warn", Terminating: "idle" }[status] ?? "bad";
    return `<span class="badge ${cls}">${esc(status)}</span>`;
  };
  const unhealthy = (p) => !["Running", "Completed", "Terminating"].includes(p.status);
  const readyOf = (c, d) => {
    const ps = c.pods.filter((p) => p.name.startsWith(d[0] + "-") && !p.job);
    const want = d[0] === "checkout" && c.id === "pe" ? 2 : d[0] === "web" && c.id === "qa" ? 2 : 1;
    return `${ps.filter((p) => p.status === "Running").length}/${want}`;
  };

  /* ---------- rendering ---------- */

  const root = document.getElementById("demo");
  if (!root) return;
  root.innerHTML = `
    <div class="app__bar">
      <div class="lights" aria-hidden="true"><i></i><i></i><i></i></div>
      <div class="app__crumbs" data-crumbs></div>
      <div class="app__search" aria-hidden="true">${I.search}<span>Jump to cluster, resource or namespace…</span><kbd>⌘K</kbd></div>
    </div>
    <div class="app__body">
      <nav class="app__rail" aria-label="Clusters" data-rail></nav>
      <nav class="app__side" aria-label="Pages" data-side></nav>
      <div class="app__main" data-main aria-live="polite"></div>
    </div>`;
  const $crumbs = root.querySelector("[data-crumbs]");
  const $rail = root.querySelector("[data-rail]");
  const $side = root.querySelector("[data-side]");
  const $main = root.querySelector("[data-main]");

  const renderCrumbs = () => {
    const c = byId[state.at];
    $crumbs.innerHTML = c
      ? `<b>Kube Desk</b><span>/</span><span class="ctx" style="--cc:${c.color}"><i></i>${esc(c.name)}</span><span class="ns-sep">/</span><span class="ctx ns">All namespaces</span>`
      : `<b>Kube Desk</b>`;
  };

  const renderRail = () => {
    $rail.innerHTML =
      `<button class="rail-btn rail-btn--home" data-go="home" aria-current="${state.at === "home"}" title="All clusters (0)">${I.home}<span class="sr-only">All clusters</span></button><div class="rail-sep"></div>` +
      clusters
        .map(
          (c, i) =>
            `<button class="rail-btn rail-btn--cluster" style="--cc:${c.color}" data-go="${c.id}" aria-current="${state.at === c.id}" title="${c.name} (${i + 1})">${initials(c.name)}<i class="st ${c.error ? "bad" : ""}"></i><span class="sr-only">${c.name}</span></button>`,
        )
        .join("") +
      `<button class="rail-btn" style="margin-top:auto;border:1px dashed var(--a-border)" title="Add cluster" tabindex="-1" aria-hidden="true">${I.plus}</button>`;
  };
  const initials = (n) => {
    const w = n.split(/[^a-zA-Z0-9]+/).filter(Boolean);
    return (w.length >= 2 ? w[0][0] + w[1][0] : w[0].slice(0, 2)).toUpperCase();
  };

  const counts = (c) => ({
    nodes: c.nodes?.length,
    pods: c.pods?.length,
    deployments: c.deploys?.length,
    services: c.services?.length,
    events: c.events?.length,
  });

  const renderSide = () => {
    const c = byId[state.at];
    if (!c) {
      $side.innerHTML = `<div class="side-h">Clusters</div>` + clusters.map((k) => `<button class="side-btn" data-go="${k.id}"><span class="chip-c" style="--cc:${k.color}">${esc(k.name)}</span></button>`).join("");
      return;
    }
    const page = state.per[c.id].page;
    const n = c.error ? {} : counts(c);
    $side.innerHTML = pages
      .map((p) =>
        p.group
          ? `<div class="side-h">${p.group}</div>`
          : `<button class="side-btn" data-page="${p.id}" aria-current="${page === p.id ? "page" : "false"}">${p.icon}${p.label}${n[p.id] != null ? `<span class="count">${n[p.id]}</span>` : ""}</button>`,
      )
      .join("");
  };

  const pagePills = (page) =>
    `<div class="app__pages">${pages
      .filter((p) => p.id)
      .map((p) => `<button data-page="${p.id}" aria-current="${page === p.id ? "page" : "false"}">${p.label}</button>`)
      .join("")}</div>`;

  const heading = (c, title) =>
    `<h4>${esc(title)}</h4><p class="app__sub">${c.version ? `Kubernetes ${c.version} · ` : ""}<code>${esc(c.server)}</code></p>`;

  const views = {
    overview(c) {
      const run = c.pods.filter((p) => p.status === "Running").length;
      const bad = c.pods.filter(unhealthy);
      const warns = c.events.filter((e) => e[0] === "Warning").length;
      const dReady = c.deploys.filter((d) => {
        const [a, b] = readyOf(c, d).split("/");
        return a === b;
      }).length;
      return (
        heading(c, c.name) +
        `<div class="stats">
          <div class="stat" style="--sc:#22c55e"><span>Nodes</span><b>${c.nodes.length}/${c.nodes.length}</b><small>ready</small></div>
          <div class="stat" style="--sc:${bad.length ? "#ef4444" : "#22c55e"}"><span>Pods</span><b>${c.pods.length}</b><small>${run} running${bad.length ? ` · ${bad.length} not ready` : ""}</small></div>
          <div class="stat" style="--sc:${dReady === c.deploys.length ? "#22c55e" : "#f59e0b"}"><span>Deployments</span><b>${dReady}/${c.deploys.length}</b><small>available</small></div>
          <div class="stat" style="--sc:${warns ? "#f59e0b" : "#94a3b8"}"><span>Warnings</span><b>${warns}</b><small>recent events</small></div>
        </div>
        <div class="grid-2">
          <div class="panel"><div class="panel__h">Capacity</div><div class="bars">${c.cap
            .map(([k, v, w]) => `<div><p><span>${k}</span><span>${v} (${w}%)</span></p><i style="--w:${w}%"></i></div>`)
            .join("")}</div></div>
          <div class="panel"><div class="panel__h">Nodes</div><table class="t"><tbody>${c.nodes
            .map(([n, , cpu, mem]) => `<tr><td class="name">${n}</td><td>${badge("Ready")}</td><td class="dim">CPU ${cpu}</td><td class="dim hide-sm">Mem ${mem}</td></tr>`)
            .join("")}</tbody></table></div>
        </div>
        <div class="panel"><div class="panel__h">Unhealthy pods (${bad.length})</div>${
          bad.length
            ? `<table class="t"><tbody>${bad
                .map((p) => `<tr class="clickable" data-pod="${p.name}"><td class="name">${p.name}</td><td class="dim hide-sm">${p.ns}</td><td>${badge(p.status)}</td><td class="dim">${p.restarts} restarts</td></tr>`)
                .join("")}</tbody></table>`
            : `<p style="padding:10px 12px;color:var(--a-muted)">Every pod is running.</p>`
        }</div>
        <div class="panel"><div class="panel__h">Warning events (${warns})</div>${
          warns
            ? c.events
                .filter((e) => e[0] === "Warning")
                .map(([, reason, obj, ns, msg, a, n]) => `<div class="ev"><p><span class="reason">${reason}</span><code>${obj}</code><span class="dim">${ns} · ${age(now() - a)} ago${n > 1 ? ` · ×${n}` : ""}</span></p><p>${esc(msg)}</p></div>`)
                .join("")
            : `<p style="padding:10px 12px;color:var(--a-muted)">No warnings.</p>`
        }</div>`
      );
    },
    nodes(c) {
      return (
        heading(c, "Nodes") +
        `<div class="panel"><table class="t"><thead><tr><th>Name</th><th>Status</th><th class="hide-sm">Roles</th><th>CPU</th><th>Memory</th><th class="hide-sm">Version</th><th>Age</th></tr></thead><tbody>${c.nodes
          .map(([n, r, cpu, mem, a]) => `<tr><td class="name">${n}</td><td>${badge("Ready")}</td><td class="dim hide-sm">${r}</td><td>${cpu}</td><td>${mem}</td><td class="dim hide-sm">${c.version}</td><td class="dim">${age(now() - a)}</td></tr>`)
          .join("")}</tbody></table></div>`
      );
    },
    pods(c) {
      return (
        heading(c, "Pods") +
        `<div class="panel"><table class="t"><thead><tr><th>Name</th><th class="hide-sm">Namespace</th><th>Status</th><th>Restarts</th><th class="hide-sm">CPU</th><th class="hide-sm">Node</th><th>Age</th></tr></thead><tbody>${c.pods
          .map(
            (p) =>
              `<tr class="clickable${p.flash ? " flash" : ""}" data-pod="${p.name}" aria-selected="${state.per[c.id].open === p.name}"><td class="name">${p.name}</td><td class="dim hide-sm">${p.ns}</td><td>${badge(p.status)}</td><td>${p.restarts}</td><td class="dim hide-sm">${p.cpu}</td><td class="dim hide-sm">${p.node}</td><td class="dim" data-born="${p.born}">${age(p.born)}</td></tr>`,
          )
          .join("")}</tbody></table></div>`
      );
    },
    deployments(c) {
      return (
        heading(c, "Deployments") +
        `<div class="panel"><table class="t"><thead><tr><th>Name</th><th class="hide-sm">Namespace</th><th>Ready</th><th class="hide-sm">Image</th><th>Age</th></tr></thead><tbody>${c.deploys
          .map((d) => {
            const r = readyOf(c, d);
            const [a, b] = r.split("/");
            return `<tr><td class="name">${d[0]}</td><td class="dim hide-sm">${d[1]}</td><td>${a === b ? r : `<span class="badge warn">${r}</span>`}</td><td class="dim hide-sm"><code>${d[2]}</code></td><td class="dim">${age(now() - d[3])}</td></tr>`;
          })
          .join("")}</tbody></table></div>`
      );
    },
    services(c) {
      return (
        heading(c, "Services") +
        `<div class="panel"><table class="t"><thead><tr><th>Name</th><th class="hide-sm">Namespace</th><th>Type</th><th class="hide-sm">Cluster IP</th><th>Ports</th></tr></thead><tbody>${c.services
          .map((s) => `<tr><td class="name">${s[0]}</td><td class="dim hide-sm">${s[1]}</td><td>${s[2]}</td><td class="dim hide-sm"><code>${s[3]}</code></td><td class="dim">${s[4]}</td></tr>`)
          .join("")}</tbody></table></div>`
      );
    },
    events(c) {
      return (
        heading(c, "Events") +
        `<div class="panel">${c.events
          .map(
            ([type, reason, obj, ns, msg, a, n]) =>
              `<div class="ev${type === "Normal" ? " normal" : ""}"><p><span class="reason">${reason}</span><code>${obj}</code><span class="dim">${ns} · ${age(now() - a)} ago${n > 1 ? ` · ×${n}` : ""}</span></p><p>${esc(msg)}</p></div>`,
          )
          .join("")}</div>`
      );
    },
  };

  const drawer = (c, still) => {
    const p = c.pods.find((x) => x.name === state.per[c.id].open);
    if (!p) return "";
    return `<aside class="drawer${still ? " still" : ""}" aria-label="Pod details">
      <header><b>${p.name}</b><button data-close title="Close (Esc)">${I.x}<span class="sr-only">Close</span></button></header>
      <div class="tabs-mini"><span>Overview</span><span>Logs</span><span>Shell</span><span>YAML</span><span>History</span></div>
      <dl>
        <dt>Status</dt><dd>${badge(p.status)}</dd>
        <dt>Namespace</dt><dd>${p.ns}</dd>
        <dt>Node</dt><dd>${p.node}</dd>
        <dt>Image</dt><dd><code>${p.image}</code></dd>
        <dt>Restarts</dt><dd>${p.restarts}</dd>
        <dt>Age</dt><dd data-born="${p.born}">${age(p.born)}</dd>
      </dl>
    </aside>`;
  };

  const renderMain = (still = false) => {
    const c = byId[state.at];
    if (!c) {
      $main.innerHTML =
        `<h4>Clusters</h4><p class="app__sub">${clusters.length} clusters · switch with <kbd>⌘1</kbd>…<kbd>⌘9</kbd> or <kbd>⌘K</kbd></p><div class="cards">` +
        clusters
          .map(
            (k) =>
              `<button class="ccard" style="--cc:${k.color}" data-go="${k.id}"><span class="tile">${initials(k.name)}</span><b>${esc(k.name)}</b><code>${esc(k.server)}</code><span class="tags">${
                k.error ? `<span class="badge bad">Unreachable</span>` : `<span class="badge">${k.version}</span>`
              }${k.tags.map((t) => `<span class="tag ${t === "current context" ? "cur" : ""}">${t}</span>`).join("")}</span>${k.error ? `<span class="err-box">${esc(k.error)}</span>` : ""}</button>`,
          )
          .join("") +
        `</div>`;
      return;
    }
    const page = state.per[c.id].page;
    if (c.error) {
      $main.innerHTML = `<div class="unreach">${I.unplug}<h4>Can’t reach ${esc(c.name)}</h4><p class="app__sub" style="margin:0">Other clusters keep working. Kube Desk retries when you come back.</p><div class="err-box">${esc(c.error)}</div><button class="a-btn" data-retry>Retry</button></div>`;
      return;
    }
    $main.innerHTML = pagePills(page) + views[page](c) + drawer(c, still);
  };

  const render = () => {
    renderCrumbs();
    renderRail();
    renderSide();
    renderMain();
  };

  /* ---------- interaction ---------- */

  const hint = document.querySelector(".demo-hint");
  const hit = (k) => {
    const kb = hint?.querySelector(`kbd[data-k="${k}"]`);
    if (!kb) return;
    kb.classList.add("is-hit");
    setTimeout(() => kb.classList.remove("is-hit"), 260);
  };
  const go = (id) => {
    if (state.at === id) return;
    state.at = id;
    render();
  };

  root.addEventListener("click", (e) => {
    const t = e.target.closest("[data-go],[data-page],[data-pod],[data-close],[data-retry]");
    if (!t) return;
    if (t.dataset.go) {
      go(t.dataset.go);
      hit(t.dataset.go === "home" ? "0" : String(clusters.indexOf(byId[t.dataset.go]) + 1));
    } else if (t.dataset.page) {
      state.per[state.at].page = t.dataset.page;
      renderSide();
      renderMain();
    } else if (t.dataset.pod) {
      const per = state.per[state.at];
      per.open = per.open === t.dataset.pod ? null : t.dataset.pod;
      if (per.page === "overview") per.page = "pods";
      renderSide();
      renderMain();
    } else if (t.hasAttribute("data-close")) {
      state.per[state.at].open = null;
      renderMain();
    } else if (t.hasAttribute("data-retry")) {
      t.disabled = true;
      t.textContent = "Checking…";
      setTimeout(renderMain, 900);
    }
  });

  let visible = false;
  if ("IntersectionObserver" in window) {
    new IntersectionObserver(([en]) => (visible = en.isIntersecting), { threshold: 0.25 }).observe(root);
  }

  document.addEventListener("keydown", (e) => {
    if (!visible || e.metaKey || e.ctrlKey || e.altKey || window.kdPalette?.isOpen()) return;
    if (e.target.closest?.("input, textarea, select, [contenteditable]")) return;
    if (/^[0-4]$/.test(e.key)) {
      go(e.key === "0" ? "home" : clusters[+e.key - 1].id);
      hit(e.key);
    } else if (e.key === "Escape" && byId[state.at] && state.per[state.at].open) {
      state.per[state.at].open = null;
      renderMain();
    }
  });

  /* ---------- live updates ---------- */

  // production-eu: the crash looping pod keeps restarting.
  // qa: a rolling update replaces web's two pods, one at a time, forever.
  const pe = byId.pe;
  const qa = byId.qa;
  let rollout = { hash: "8d6f5c7b9", minor: 42, step: 0 };
  const webPods = () => qa.pods.filter((p) => p.name.startsWith("web-"));

  const steps = [
    () => qa.pods.unshift({ ...pod(`web-${rollout.hash}-${sfx()}`, "qa", "Pending", 0, "qa-1", "0m", 0, `web:1.${rollout.minor}.0`), fresh: true }),
    () => (webPods().find((p) => p.fresh && p.status === "Pending").status = "ContainerCreating"),
    () => {
      webPods().find((p) => p.fresh && p.status === "ContainerCreating").status = "Running";
      webPods().filter((p) => !p.fresh).pop().status = "Terminating";
    },
    () => {
      const old = webPods().find((p) => p.status === "Terminating");
      qa.pods.splice(qa.pods.indexOf(old), 1);
      if (state.per.qa.open === old.name) state.per.qa.open = null;
    },
  ];

  const tick = () => {
    const changed = new Set();
    // rollout: two rounds of the four steps, then a pause, then a new version
    if (rollout.step < 8) {
      const before = qa.pods.map((p) => p.name + p.status).join();
      steps[rollout.step % 4]();
      qa.pods.forEach((p) => {
        if (!before.includes(p.name + p.status)) p.flash = true;
      });
      changed.add("qa");
    }
    rollout.step++;
    if (rollout.step === 8) qa.deploys[0][2] = `web:1.${rollout.minor}.0`;
    if (rollout.step === 11) {
      webPods().forEach((p) => delete p.fresh);
      rollout = { hash: sfx(9), minor: rollout.minor + 1, step: 0 };
    }
    // a restart every few ticks
    if (Math.random() < 0.3) {
      const p = pe.pods.find((x) => x.status === "CrashLoopBackOff");
      p.restarts++;
      p.flash = true;
      pe.events[0][6]++;
      pe.events[0][5] = 0;
      changed.add("pe");
    }
    if (changed.has(state.at)) {
      renderSide();
      renderMain(true);
    } else {
      $main.querySelectorAll("[data-born]").forEach((el) => (el.textContent = age(+el.dataset.born)));
    }
    clusters.forEach((c) => c.pods?.forEach((p) => delete p.flash));
  };

  render();
  if (!reduce) setInterval(() => visible && !document.hidden && tick(), 2600);

  /* ---------- guardrail confirm ---------- */

  const guard = document.getElementById("guard");
  if (guard) {
    const input = guard.querySelector("input");
    const btn = guard.querySelector("[data-delete]");
    const note = guard.querySelector(".note");
    const want = input.dataset.want;
    input.addEventListener("input", () => {
      btn.disabled = input.value.trim() !== want;
      note.textContent = btn.disabled ? "Type the cluster’s name to continue" : "Ready";
    });
    guard.addEventListener("reset", () => {
      btn.disabled = true;
      note.textContent = "Type the cluster’s name to continue";
    });
    guard.addEventListener("submit", (e) => {
      e.preventDefault();
      note.textContent = "This is a demo — nothing was deleted.";
      btn.disabled = true;
      input.value = "";
    });
  }

  /* ---------- agent permissions ---------- */

  const perm = document.getElementById("perm");
  if (perm) {
    const say = {
      Off: (n) => `Agents can’t see ${n}.`,
      Read: (n) => `Agents can read ${n}: resources, logs, events, changes. Secret values stay hidden.`,
      Propose: (n) => `Agents can read ${n} and propose changes. Each one waits for your approval.`,
    };
    const note = perm.querySelector(".perm__note");
    perm.addEventListener("click", (e) => {
      const b = e.target.closest(".seg button");
      if (!b) return;
      b.parentElement.querySelectorAll("button").forEach((x) => x.setAttribute("aria-pressed", String(x === b)));
      note.textContent = say[b.dataset.level](b.closest(".perm__row").dataset.name);
    });
  }

  /* ---------- screenshot tabs ---------- */

  const gal = document.getElementById("gallery");
  if (gal) {
    const tabs = [...gal.querySelectorAll("[role=tab]")];
    const img = gal.querySelector(".gallery__frame img");
    const cap = gal.querySelector(".gallery__cap");
    const select = (t, focus) => {
      tabs.forEach((x) => {
        x.setAttribute("aria-selected", String(x === t));
        x.tabIndex = x === t ? 0 : -1;
      });
      img.src = t.dataset.src;
      img.alt = t.dataset.alt;
      cap.innerHTML = t.dataset.cap;
      if (focus) t.focus();
    };
    tabs.forEach((t, i) => {
      t.addEventListener("click", () => select(t));
      t.addEventListener("keydown", (e) => {
        const d = { ArrowRight: 1, ArrowLeft: -1 }[e.key];
        if (d) select(tabs[(i + d + tabs.length) % tabs.length], true);
      });
      // warm the cache so switching tabs is instant, like the app
      const pre = new Image();
      pre.src = t.dataset.src;
    });
  }
})();
