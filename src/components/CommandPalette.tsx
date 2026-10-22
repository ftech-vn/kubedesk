import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Bell, Bot, Box, Download, History, Globe, Gauge, Home, Layers, LayoutGrid, Monitor, Moon, Plus, Search, Settings2, Sun, Waypoints } from "lucide-react";
import { AppInfo } from "../mygo";
import { isWatched, toggleWatch, useAlerts } from "../lib/alerts";
import { byKey, resources } from "../lib/resources";
import { useApp } from "../lib/store";
import { useWatch } from "../lib/watch";
import { ClusterTile } from "./ClusterRail";
import { Kbd, mod } from "./ui";

interface Item {
  id: string;
  group: string;
  label: string;
  hint?: string;
  icon: ReactNode;
  keywords?: string;
  run(): void;
}

/** score is how well q matches text as a subsequence; -1 when it does not. */
function score(text: string, q: string): number {
  if (!q) return 0;
  const t = text.toLowerCase();
  const i = t.indexOf(q);
  if (i >= 0) return 1000 - i * 2 - t.length * 0.1 + (i === 0 ? 200 : 0);
  let ti = 0;
  let gaps = 0;
  for (const ch of q) {
    const found = t.indexOf(ch, ti);
    if (found < 0) return -1;
    gaps += found - ti;
    ti = found + 1;
  }
  return 500 - gaps * 5 - t.length * 0.1;
}

export function CommandPalette({ onClose }: { onClose(): void }) {
  const s = useApp();
  const active = s.active;
  const view = active ? s.views[active] : undefined;
  const ns = view?.namespace ?? "";
  const namespaces = useWatch(active, byKey.get("namespaces")!.gvr);
  const pods = useWatch(active, byKey.get("pods")!.gvr, ns);
  const deployments = useWatch(active, byKey.get("deployments")!.gvr, ns);
  const services = useWatch(active, byKey.get("services")!.gvr, ns);
  const [q, setQ] = useState("");
  const [sel, setSel] = useState(0);
  const list = useRef<HTMLDivElement>(null);

  const items = useMemo<Item[]>(() => {
    const out: Item[] = [];
    const visible = s.clusters.filter((c) => !c.hidden);
    visible.forEach((c, i) =>
      out.push({
        id: `cluster:${c.id}`,
        group: "Clusters",
        label: c.name,
        hint: i < 9 ? `${mod}${i + 1}` : c.server,
        keywords: `${c.context} ${c.server}`,
        icon: <ClusterTile c={c} size={20} />,
        run: () => s.activate(c.id),
      }),
    );
    out.push({ id: "home", group: "Clusters", label: "All clusters", icon: <Home size={16} />, run: () => s.activate(null) });
    out.push({ id: "add", group: "Clusters", label: "Add a cluster…", icon: <Plus size={16} />, run: () => s.openDialog({ kind: "add" }) });
    out.push({ id: "agents", group: "App", label: "AI agents (MCP)…", keywords: "mcp claude codex cursor ai agent", icon: <Bot size={16} />, run: () => s.openDialog({ kind: "agents" }) });
    out.push({ id: "updates", group: "App", label: "Check for updates…", keywords: "update version upgrade", icon: <Download size={16} />, run: () => AppInfo.checkForUpdates() });
    out.push({ id: "theme:light", group: "Appearance", label: "Light mode", keywords: "theme", hint: s.theme === "light" ? "current" : undefined, icon: <Sun size={16} />, run: () => s.setTheme("light") });
    out.push({ id: "theme:dark", group: "Appearance", label: "Dark mode", keywords: "theme", hint: s.theme === "dark" ? "current" : undefined, icon: <Moon size={16} />, run: () => s.setTheme("dark") });
    out.push({ id: "theme:system", group: "Appearance", label: "Match system appearance", keywords: "theme auto", hint: s.theme === "system" ? "current" : undefined, icon: <Monitor size={16} />, run: () => s.setTheme("system") });
    if (active) {
      out.push({ id: "settings", group: "Clusters", label: "Settings of this cluster…", icon: <Settings2 size={16} />, run: () => s.openDialog({ kind: "settings", cluster: active }) });
      out.push({
        id: "watch",
        group: "App",
        label: isWatched(useAlerts.getState().state, active, ns) ? `Stop watching ${ns || "this cluster"}` : `Watch ${ns || "this cluster"} for problems`,
        keywords: "watchlist notifications alerts monitor",
        icon: <Bell size={16} />,
        run: () => toggleWatch(active, ns),
      });
      out.push({ id: "sec:overview", group: "Go to", label: "Overview", icon: <Gauge size={16} />, run: () => s.setSection("overview") });
      out.push({ id: "sec:changes", group: "Go to", label: "Changes", keywords: "timeline history what changed diff", icon: <History size={16} />, run: () => s.setSection("changes") });
      for (const r of resources) {
        const Icon = r.icon;
        out.push({ id: `sec:${r.key}`, group: "Go to", label: r.title, hint: r.group, keywords: r.kind, icon: <Icon size={16} />, run: () => s.setSection(r.key) });
      }
      out.push({ id: "ns:all", group: "Namespaces", label: "All namespaces", icon: <LayoutGrid size={16} />, run: () => s.setNamespace("") });
      for (const n of namespaces.items) {
        out.push({ id: `ns:${n.metadata.name}`, group: "Namespaces", label: n.metadata.name, hint: "namespace", icon: <LayoutGrid size={16} />, run: () => s.setNamespace(n.metadata.name) });
      }
      const objs = (items: typeof pods.items, key: string, kind: string, icon: ReactNode) => {
        const def = byKey.get(key)!;
        for (const o of items) {
          out.push({
            id: `${kind}:${o.metadata.uid}`,
            group: "Objects",
            label: o.metadata.name,
            hint: `${kind} · ${o.metadata.namespace}`,
            icon,
            run: () => s.openDetail({ gvr: def.gvr, kind, namespace: o.metadata.namespace ?? "", name: o.metadata.name }, true),
          });
        }
      };
      objs(deployments.items, "deployments", "Deployment", <Layers size={16} />);
      objs(services.items, "services", "Service", <Waypoints size={16} />);
      objs(pods.items, "pods", "Pod", <Box size={16} />);
    }
    return out;
  }, [s, active, namespaces.items, pods.items, deployments.items, services.items]);

  const query = q.trim().toLowerCase();
  const results = useMemo(() => {
    const everywhere: Item = {
      id: "search-all",
      group: "Search",
      label: query ? `Search every cluster for “${q.trim()}”` : "Search every cluster…",
      hint: "⇧⌘F",
      icon: <Globe size={16} />,
      run: () => s.openDialog({ kind: "search", query: q.trim() }),
    };
    if (!query) return [...items.filter((i) => i.group !== "Objects" && i.group !== "Namespaces" && i.group !== "Appearance" && i.group !== "App").slice(0, 60), everywhere];
    return items
      .map((i) => {
        const label = score(i.label, query);
        const extra = score(`${i.label} ${i.keywords ?? ""} ${i.hint ?? ""}`, query);
        const s = Math.max(label, extra < 0 ? -1 : extra - 100);
        // Clusters and pages come first among matches of the same quality.
        return { i, s: s < 0 ? -1 : s + (i.group === "Clusters" ? 50 : i.group === "Go to" ? 30 : 0) };
      })
      .filter((x) => x.s >= 0)
      .sort((a, b) => b.s - a.s)
      .slice(0, 60)
      .map((x) => x.i)
      .concat(everywhere);
  }, [items, query, q, s]);

  useEffect(() => setSel(0), [query]);
  useEffect(() => {
    list.current?.querySelector(".palette-item.sel")?.scrollIntoView({ block: "nearest" });
  }, [sel]);

  const choose = (i: Item | undefined) => {
    if (!i) return;
    onClose();
    i.run();
  };

  let lastGroup = "";
  return (
    <div className="modal-backdrop palette-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="palette">
        <div className="palette-input">
          <Search size={16} className="muted" />
          <input
            autoFocus
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="Switch cluster, go to a resource, pick a namespace, find a pod…"
            onKeyDown={(e) => {
              if (e.key === "ArrowDown") {
                e.preventDefault();
                setSel((x) => Math.min(results.length - 1, x + 1));
              } else if (e.key === "ArrowUp") {
                e.preventDefault();
                setSel((x) => Math.max(0, x - 1));
              } else if (e.key === "Enter") choose(results[sel]);
              else if (e.key === "Escape") onClose();
            }}
          />
        </div>
        <div className="palette-list" ref={list}>
          {results.length === 0 && <div className="faint pad">No matches</div>}
          {results.map((r, idx) => {
            const header = r.group !== lastGroup ? r.group : null;
            lastGroup = r.group;
            return (
              <div key={r.id}>
                {header && <div className="palette-group">{header}</div>}
                <button className={`palette-item ${idx === sel ? "sel" : ""}`} onMouseMove={() => setSel(idx)} onClick={() => choose(r)}>
                  <span className="palette-icon">{r.icon}</span>
                  <span className="palette-label">{r.label}</span>
                  {r.hint && <span className="palette-hint">{r.hint}</span>}
                </button>
              </div>
            );
          })}
        </div>
        <div className="palette-foot faint small">
          <Kbd>↑↓</Kbd> move <Kbd>↵</Kbd> open <Kbd>esc</Kbd> close
        </div>
      </div>
    </div>
  );
}
