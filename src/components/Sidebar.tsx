import { useMemo, useState } from "react";
import { BookOpen, ChevronDown, ChevronRight, Gauge, History, Network, Package, Puzzle } from "lucide-react";
import { customResourceDef, resourceGroups, resources } from "../lib/resources";
import { useActiveView, useApp } from "../lib/store";
import { useWatch } from "../lib/watch";

const COLLAPSED_KEY = "sidebar-collapsed";

function loadCollapsed(): Record<string, boolean> {
  try {
    return JSON.parse(localStorage.getItem(COLLAPSED_KEY) ?? "") ?? {};
  } catch {
    return { "Access Control": true };
  }
}

export function Sidebar({ cluster }: { cluster: string }) {
  const view = useActiveView()!;
  const setSection = useApp((s) => s.setSection);
  const [collapsed, setCollapsed] = useState(loadCollapsed);
  const crds = useWatch(cluster, resources.find((r) => r.key === "crds")!.gvr);

  const crdGroups = useMemo(() => {
    const groups = new Map<string, { key: string; title: string }[]>();
    for (const crd of crds.items) {
      const d = customResourceDef(crd);
      const list = groups.get(d.group) ?? [];
      list.push({ key: d.key, title: d.title });
      groups.set(d.group, list);
    }
    return [...groups.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([g, items]) => [g, items.sort((a, b) => a.title.localeCompare(b.title))] as const);
  }, [crds.items]);

  const toggle = (g: string) => {
    const next = { ...collapsed, [g]: !collapsed[g] };
    setCollapsed(next);
    try {
      localStorage.setItem(COLLAPSED_KEY, JSON.stringify(next));
    } catch {
      /* storage unavailable */
    }
  };

  const item = (key: string, title: string, Icon: React.ComponentType<{ size?: number }>, indent = false) => (
    <button key={key} className={`side-item ${view.section === key ? "active" : ""} ${indent ? "indent" : ""}`} onClick={() => setSection(key)}>
      <Icon size={15} />
      <span>{title}</span>
    </button>
  );

  return (
    <aside className="sidebar">
      <div className="side-scroll">
        {item("overview", "Overview", Gauge)}
        {item("changes", "Changes", History)}
        {item("map", "Map", Network)}
        {item("helm", "Helm", Package)}
        {item("runbooks", "Runbooks", BookOpen)}
        {resourceGroups.map((g) => (
          <div key={g} className="side-group">
            <button className="side-group-title" onClick={() => toggle(g)}>
              {collapsed[g] ? <ChevronRight size={12} /> : <ChevronDown size={12} />}
              {g}
            </button>
            {!collapsed[g] && (
              <>
                {resources.filter((r) => r.group === g).map((r) => item(r.key, r.title, r.icon))}
                {g === "Custom Resources" &&
                  crdGroups.map(([group, items]) => (
                    <div key={group} className="side-subgroup">
                      <button className="side-subgroup-title" onClick={() => toggle(`crd:${group}`)} title={group}>
                        {collapsed[`crd:${group}`] === false ? <ChevronDown size={11} /> : <ChevronRight size={11} />}
                        <span>{group}</span>
                      </button>
                      {collapsed[`crd:${group}`] === false && items.map((i) => item(i.key, i.title, Puzzle, true))}
                    </div>
                  ))}
              </>
            )}
          </div>
        ))}
      </div>
    </aside>
  );
}
