import { useState } from "react";
import { EyeOff, Home, Plus, RefreshCw, Settings2, Trash2 } from "lucide-react";
import { Clusters, type Cluster } from "../mygo";
import { clusterColor, initials, shortName } from "../lib/clusterLook";
import { ask, run, useApp } from "../lib/store";
import { dropCluster } from "../lib/watch";
import { forgetResources } from "../lib/discovery";
import { ContextMenu } from "./Menu";
import { mod } from "./ui";

export function ClusterTile({ c, size = 36 }: { c: Cluster; size?: number }) {
  const color = clusterColor(c);
  return (
    <span className="cluster-tile" style={{ width: size, height: size, background: `color-mix(in srgb, ${color} 22%, var(--panel))`, color, borderColor: `color-mix(in srgb, ${color} 45%, transparent)`, fontSize: size * 0.36 }}>
      {initials(shortName(c.name))}
    </span>
  );
}

export async function reconnect(id: string) {
  await Clusters.reconnect(id);
  forgetResources(id);
  dropCluster(id);
  useApp.getState().checkStatus(id);
}

export function clusterMenu(c: Cluster) {
  const s = useApp.getState();
  return [
    { label: "Settings…", icon: <Settings2 size={15} />, run: () => s.openDialog({ kind: "settings", cluster: c.id }) },
    { label: "Reconnect", icon: <RefreshCw size={15} />, run: () => reconnect(c.id) },
    {
      label: "Hide",
      icon: <EyeOff size={15} />,
      separatorBefore: true,
      run: async () => {
        await run("Could not hide", () => Clusters.setHidden(c.id, true));
        if (useApp.getState().active === c.id) s.activate(null);
        await s.refresh();
      },
    },
    {
      label: c.sourceKind === "stored" ? "Remove…" : "Remove from app…",
      icon: <Trash2 size={15} />,
      danger: true,
      run: () => removeCluster(c),
    },
  ];
}

export async function removeCluster(c: Cluster) {
  const s = useApp.getState();
  const r = await ask({
    title: `Remove ${c.name}?`,
    message:
      c.sourceKind === "stored"
        ? "Its kubeconfig, which was added in the app, is deleted."
        : `The app stops showing it. ${c.sourceKind === "default" ? "Your kubeconfig" : c.source} is not changed.`,
    confirmLabel: "Remove",
    danger: true,
  });
  if (!r.ok) return;
  await run("Could not remove", () => Clusters.remove(c.id));
  if (s.active === c.id) s.activate(null);
  await s.refresh();
}

export function ClusterRail() {
  const { clusters, active, status, activate, openDialog, refresh } = useApp();
  const visible = clusters.filter((c) => !c.hidden);
  const [menu, setMenu] = useState<{ c: Cluster; x: number; y: number } | null>(null);
  const [drag, setDrag] = useState<string | null>(null);

  const onDrop = async (target: string) => {
    if (!drag || drag === target) return;
    const ids = visible.map((c) => c.id).filter((id) => id !== drag);
    ids.splice(ids.indexOf(target), 0, drag);
    const hidden = clusters.filter((c) => c.hidden).map((c) => c.id);
    useApp.setState({ clusters: [...ids.map((id) => clusters.find((c) => c.id === id)!), ...clusters.filter((c) => c.hidden)] });
    await Clusters.reorder([...ids, ...hidden]);
    await refresh();
  };

  return (
    <nav className="rail">
      <button className={`rail-btn ${active === null ? "active" : ""}`} title="All clusters" onClick={() => activate(null)}>
        <Home size={18} />
      </button>
      <div className="rail-sep" />
      <div className="rail-list">
        {visible.map((c, i) => {
          const st = status[c.id];
          const tone = st?.checking ? "checking" : st?.ok ? "ok" : st?.error ? "bad" : "unknown";
          return (
            <button
              key={c.id}
              className={`rail-cluster ${active === c.id ? "active" : ""} ${drag === c.id ? "dragging" : ""}`}
              title={`${c.name}${i < 9 ? `  (${mod}${i + 1})` : ""}\n${c.server}${st?.error ? `\n⚠ ${st.error}` : st?.version ? `\n${st.version}` : ""}`}
              onClick={() => activate(c.id)}
              onContextMenu={(e) => {
                e.preventDefault();
                setMenu({ c, x: e.clientX, y: e.clientY });
              }}
              draggable
              onDragStart={() => setDrag(c.id)}
              onDragEnd={() => setDrag(null)}
              onDragOver={(e) => e.preventDefault()}
              onDrop={() => onDrop(c.id)}
            >
              <span className="rail-indicator" />
              <ClusterTile c={c} />
              {c.production && <span className="rail-prod" title="Production" />}
              <span className={`rail-status ${tone}`} />
            </button>
          );
        })}
      </div>
      <button className="rail-btn add" title="Add a cluster" onClick={() => openDialog({ kind: "add" })}>
        <Plus size={18} />
      </button>
      {menu && <ContextMenu x={menu.x} y={menu.y} items={clusterMenu(menu.c)} onClose={() => setMenu(null)} />}
    </nav>
  );
}
