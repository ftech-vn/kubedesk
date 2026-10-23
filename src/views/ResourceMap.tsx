import { useEffect, useMemo, useRef, useState } from "react";
import { AlertTriangle, Maximize, Minus, Network, Plus, Route, Router, Search, X } from "lucide-react";
import { buildMap, layoutGroup, mapKindList, NODE_H, NODE_W, type GroupLayout, type MapKind, type MapLists, type MapNode } from "../lib/graph";
import { byKind } from "../lib/resources";
import { useActiveView, useApp } from "../lib/store";
import { useWatch } from "../lib/watch";
import { useApiResources } from "../lib/discovery";
import type { GVR } from "../mygo";
import { Empty, Spinner } from "../components/ui";
import { ContextMenu } from "../components/Menu";

/** Kinds whose details have a map of what they belong to. */
export const mapKinds = new Set<string>(["Gateway", "HTTPRoute", "Ingress", "Service", "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job", "CronJob", "Pod", "HorizontalPodAutoscaler"]);

const MAX_NODES = 700;
const GROUP_GAP = 34;
const GROUP_PAD = 14;
const LABEL_H = 22;
const CONFIG_KEY = "map-config";

function savedConfig() {
  try {
    return localStorage.getItem(CONFIG_KEY) !== "0";
  } catch {
    return true;
  }
}

// Gateway API kinds, watched where the cluster serves them.
const gatewayKinds: Partial<Record<MapKind, GVR>> = {
  Gateway: { group: "gateway.networking.k8s.io", version: "v1", resource: "gateways" },
  HTTPRoute: { group: "gateway.networking.k8s.io", version: "v1", resource: "httproutes" },
};

export const gvrOfMapKind = (kind: string): GVR | undefined => byKind.get(kind)?.gvr ?? gatewayKinds[kind as MapKind];

/** useMapLists watches what the map is drawn from. */
function useMapLists(cluster: string, namespace: string, config: boolean): { lists: MapLists; loading: boolean; error?: string } {
  const served = useApiResources(cluster);
  const out: MapLists = {};
  let loading = false;
  let error: string | undefined;
  for (const kind of mapKindList) {
    const gw = gatewayKinds[kind];
    const unserved = !!gw && !served?.some((r) => r.group === gw.group && r.resource === gw.resource);
    const off = unserved || (!config && (kind === "ConfigMap" || kind === "Secret" || kind === "PersistentVolumeClaim"));
    // The list of kinds never changes, so the hooks keep their order.
    // eslint-disable-next-line react-hooks/rules-of-hooks
    const w = useWatch(cluster, off ? null : gvrOfMapKind(kind)!, namespace);
    if (off) continue;
    // A kind the user may not list is left out, rather than taken as empty.
    if (w.error && !w.items.length) {
      if (kind === "Pod" || kind === "Deployment") error = w.error;
      continue;
    }
    if (w.loading && !w.items.length) loading = true;
    out[kind] = w.items;
  }
  return { lists: out, loading, error };
}

/** ResourceMapView draws how a namespace's objects connect; with focus, only the part an object is in. */
export function ResourceMapView({ cluster, namespace, focus }: { cluster: string; namespace: string; focus?: { kind: string; namespace: string; name: string } }) {
  const openDetail = useApp((s) => s.openDetail);
  const [config, setConfig] = useState(savedConfig);
  const [problems, setProblems] = useState(false);
  const [filter, setFilter] = useState("");
  const [zoom, setZoom] = useState(1);
  const [hover, setHover] = useState<string | null>(null);
  const [podMenu, setPodMenu] = useState<{ node: MapNode; x: number; y: number } | null>(null);
  const canvas = useRef<HTMLDivElement>(null);
  const { lists, loading, error } = useMapLists(cluster, namespace, config);

  // Lists change often; the map is rebuilt from them as they settle.
  const [stable, setStable] = useState(lists);
  const listsKey = mapKindList.map((k) => (lists[k] ? lists[k]!.length + ":" + lists[k]!.map((o) => o.metadata.resourceVersion).join(",") : "-")).join("|");
  useEffect(() => {
    const t = setTimeout(() => setStable(lists), 250);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [listsKey]);

  const groups = useMemo(() => buildMap(stable, { config }), [stable, config]);
  const focusId = focus ? `${focus.kind}/${focus.namespace}/${focus.name}` : null;
  const shown = useMemo(() => {
    let gs = groups;
    if (focusId) {
      gs = gs.filter((g) => g.nodes.some((n) => n.id === focusId || (focus?.kind === "Pod" && n.pods?.includes(focus.name))));
    }
    if (problems) gs = gs.filter((g) => g.worst === "bad" || g.worst === "warn");
    const f = filter.trim().toLowerCase();
    if (f) gs = gs.filter((g) => g.nodes.some((n) => n.name.toLowerCase().includes(f) || n.pods?.some((p) => p.includes(f))));
    return gs;
  }, [groups, focusId, focus, problems, filter]);

  const { layouts, cut } = useMemo(() => {
    const out: (GroupLayout & { top: number })[] = [];
    let total = 0;
    let top = 0;
    for (const g of shown) {
      if (total + g.nodes.length > MAX_NODES && out.length) break;
      total += g.nodes.length;
      const l = layoutGroup(g);
      out.push({ ...l, top });
      top += l.height + GROUP_PAD * 2 + LABEL_H + GROUP_GAP;
    }
    return { layouts: out, cut: shown.length - out.length };
  }, [shown]);

  const width = Math.max(0, ...layouts.map((l) => l.width)) + GROUP_PAD * 2;
  const height = layouts.length ? layouts[layouts.length - 1]!.top + layouts[layouts.length - 1]!.height + GROUP_PAD * 2 + LABEL_H : 0;

  const near = useMemo(() => {
    if (!hover) return null;
    const s = new Set([hover]);
    for (const l of layouts) for (const e of l.group.edges) if (e.from === hover || e.to === hover) s.add(e.from).add(e.to);
    return s;
  }, [hover, layouts]);

  // Zoom with ⌘ and the wheel, or a pinch.
  useEffect(() => {
    const el = canvas.current;
    if (!el) return;
    const onWheel = (e: WheelEvent) => {
      if (!e.ctrlKey && !e.metaKey) return;
      e.preventDefault();
      setZoom((z) => Math.min(2, Math.max(0.3, z * (e.deltaY < 0 ? 1.08 : 0.92))));
    };
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => el.removeEventListener("wheel", onWheel);
  }, []);

  const fit = () => {
    const el = canvas.current;
    if (!el || !width) return;
    setZoom(Math.min(1, Math.max(0.3, Math.min((el.clientWidth - 24) / width, (el.clientHeight - 24) / height))));
  };

  const open = (n: MapNode, e: React.MouseEvent) => {
    if (n.missing) return;
    if (n.pods) {
      setPodMenu({ node: n, x: e.clientX, y: e.clientY });
      return;
    }
    const gvr = gvrOfMapKind(n.kind);
    if (gvr) openDetail({ gvr, kind: n.kind, namespace: n.namespace, name: n.name }, !focus);
  };

  const toggleConfig = () => {
    setConfig(!config);
    try {
      localStorage.setItem(CONFIG_KEY, config ? "0" : "1");
    } catch {
      /* storage unavailable */
    }
  };

  const toolbar = (
    <div className="map-tools">
      {!focus && (
        <div className="search-input">
          <Search size={14} />
          <input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="Find an app or object" />
          {filter && (
            <button className="clear" onClick={() => setFilter("")}>
              <X size={13} />
            </button>
          )}
        </div>
      )}
      {!focus && (
        <label className="check small">
          <input type="checkbox" checked={problems} onChange={(e) => setProblems(e.target.checked)} /> Problems only
        </label>
      )}
      <label className="check small">
        <input type="checkbox" checked={config} onChange={toggleConfig} /> Config and storage
      </label>
      <div className="map-zoom">
        <button title="Zoom out" onClick={() => setZoom((z) => Math.max(0.3, z / 1.2))}>
          <Minus size={13} />
        </button>
        <span className="small faint">{Math.round(zoom * 100)}%</span>
        <button title="Zoom in" onClick={() => setZoom((z) => Math.min(2, z * 1.2))}>
          <Plus size={13} />
        </button>
        <button title="Fit" onClick={fit}>
          <Maximize size={13} />
        </button>
      </div>
    </div>
  );

  let body: React.ReactNode;
  if (error) body = <Empty icon={<AlertTriangle size={26} />} title="Could not read the workloads">{error}</Empty>;
  else if (loading && !layouts.length)
    body = (
      <div className="center-pad">
        <Spinner />
      </div>
    );
  else if (!layouts.length)
    body = (
      <Empty icon={<Network size={26} />} title={problems ? "No problems" : filter ? "Nothing matches" : "Nothing to map"}>
        {problems ? "Every part of the map is healthy." : filter ? "Try another name." : focus ? "This object is not connected to anything." : namespace ? `Namespace ${namespace} has no workloads.` : "There are no workloads."}
      </Empty>
    );
  else
    body = (
      <div className="map-inner" style={{ width: width * zoom + 32, height: height * zoom + 32 }}>
        <div className="map-scale" style={{ transform: `scale(${zoom})`, width, height }}>
          {layouts.map((l) => (
            <div key={l.group.id} className={`map-group tone-${l.group.worst}`} style={{ top: l.top, width: l.width + GROUP_PAD * 2, height: l.height + GROUP_PAD * 2 + LABEL_H }}>
              <div className="map-group-title">
                {l.group.title}
                {!namespace && <span className="faint"> · {l.group.nodes[0]!.namespace}</span>}
              </div>
            </div>
          ))}
          <svg className="map-edges" width={width} height={height}>
            {layouts.map((l) => {
              const at = new Map(l.placed.map((p) => [p.node.id, p]));
              const ox = GROUP_PAD;
              const oy = l.top + GROUP_PAD + LABEL_H;
              return l.group.edges.map((e) => {
                const a = at.get(e.from);
                const b = at.get(e.to);
                if (!a || !b) return null;
                const x1 = ox + a.x + NODE_W;
                const y1 = oy + a.y + NODE_H / 2;
                const x2 = ox + b.x;
                const y2 = oy + b.y + NODE_H / 2;
                const mid = (x1 + x2) / 2;
                const lit = near ? near.has(e.from) && near.has(e.to) && (e.from === hover || e.to === hover) : false;
                return <path key={`${e.from}>${e.to}`} d={`M${x1},${y1} C${mid},${y1} ${mid},${y2} ${x2},${y2}`} className={`map-edge ${lit ? "lit" : ""} ${near && !lit ? "dim" : ""} ${b.node.missing ? "broken" : ""}`} />;
              });
            })}
          </svg>
          {layouts.map((l) =>
            l.placed.map((p) => {
              const n = p.node;
              const Icon = byKind.get(n.kind)?.icon ?? (n.kind === "Gateway" ? Router : n.kind === "HTTPRoute" ? Route : Network);
              const focused = n.id === focusId || (focus?.kind === "Pod" && n.pods?.includes(focus.name));
              return (
                <button
                  key={n.id}
                  className={`map-node tone-${n.tone} ${n.missing ? "missing" : ""} ${focused ? "focused" : ""} ${near && !near.has(n.id) ? "dim" : ""}`}
                  style={{ left: GROUP_PAD + p.x, top: l.top + GROUP_PAD + LABEL_H + p.y, width: NODE_W, height: NODE_H }}
                  onMouseEnter={() => setHover(n.id)}
                  onMouseLeave={() => setHover(null)}
                  onClick={(e) => open(n, e)}
                  title={n.missing ? `${n.kind} ${n.name} does not exist` : `${n.kind} ${n.namespace}/${n.name}`}
                >
                  <span className="map-node-icon">
                    <Icon size={14} />
                  </span>
                  <span className="map-node-text">
                    <span className="map-node-name">{n.name}</span>
                    <span className="map-node-status">
                      <span className="map-dot" />
                      {kindLabel(n.kind)} · {n.status}
                    </span>
                  </span>
                </button>
              );
            }),
          )}
        </div>
      </div>
    );

  return (
    <div className={`map-view ${focus ? "in-details" : ""}`}>
      {toolbar}
      {cut > 0 && (
        <div className="list-warning">
          {cut} more parts are not drawn: {namespace ? "find an app above" : "choose a namespace"} to see them.
        </div>
      )}
      <div className="map-canvas" ref={canvas}>
        {body}
      </div>
      {podMenu && (
        <ContextMenu
          x={podMenu.x}
          y={podMenu.y}
          items={podMenu.node.pods!.slice(0, 40).map((name) => ({
            label: name,
            run: () => openDetail({ gvr: byKind.get("Pod")!.gvr, kind: "Pod", namespace: podMenu.node.namespace, name }, !focus),
          }))}
          onClose={() => setPodMenu(null)}
        />
      )}
    </div>
  );
}

const shortKinds: Partial<Record<MapKind, string>> = { HorizontalPodAutoscaler: "HPA", PersistentVolumeClaim: "PVC", HTTPRoute: "Route" };
const kindLabel = (k: MapKind) => shortKinds[k] ?? k;

/** ResourceMapPage is the Map page of a cluster. */
export function ResourceMapPage({ cluster }: { cluster: string }) {
  const view = useActiveView()!;
  return (
    <div className="list-view">
      <header className="list-header">
        <div className="list-title">
          <Network size={18} className="muted" />
          <h1>Map</h1>
          <span className="scope">{view.namespace ? `in ${view.namespace}` : "all namespaces"}</span>
        </div>
        <span className="faint small">Traffic flows left to right: ingresses, services, workloads, pods, and what they use. Click an object to open it.</span>
      </header>
      <ResourceMapView cluster={cluster} namespace={view.namespace} />
    </div>
  );
}
