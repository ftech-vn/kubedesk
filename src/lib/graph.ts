import { matchesSelector, podStatus, type KObj, type Tone } from "./k8s";

/** The kinds the resource map draws, in the lists it is built from. */
export type MapKind =
  | "Ingress"
  | "Service"
  | "HorizontalPodAutoscaler"
  | "CronJob"
  | "Deployment"
  | "StatefulSet"
  | "DaemonSet"
  | "ReplicaSet"
  | "Job"
  | "Pod"
  | "ConfigMap"
  | "Secret"
  | "PersistentVolumeClaim";

export type MapLists = Partial<Record<MapKind, KObj[]>>;

/** MapNode is an object of the map, or the pods of a workload together. */
export interface MapNode {
  id: string;
  kind: MapKind;
  name: string;
  namespace: string;
  tone: Tone;
  status: string;
  column: number;
  /** pods, for the pods of a workload shown together. */
  pods?: string[];
  /** missing marks an object that others name but that does not exist. */
  missing?: boolean;
}

export interface MapEdge {
  from: string;
  to: string;
}

/** MapGroup is a part of the map: objects that belong together, like an app. */
export interface MapGroup {
  id: string;
  title: string;
  nodes: MapNode[];
  edges: MapEdge[];
  /** worst is the worst tone in the group. */
  worst: Tone;
}

// Traffic comes in on the left; what pods use is on the right.
const columnOf: Record<MapKind, number> = {
  Ingress: 0,
  Service: 1,
  HorizontalPodAutoscaler: 1,
  CronJob: 2,
  Deployment: 2,
  StatefulSet: 2,
  DaemonSet: 2,
  ReplicaSet: 3,
  Job: 3,
  Pod: 4,
  ConfigMap: 5,
  Secret: 5,
  PersistentVolumeClaim: 5,
};

export const mapKindList = Object.keys(columnOf) as MapKind[];

/** Pods beyond this many, of one workload, show as one node. */
const GROUP_PODS = 4;
/** Jobs kept for each cron job, the newest. */
const JOBS_PER_CRONJOB = 3;

const toneRank: Record<Tone, number> = { bad: 0, warn: 1, info: 2, ok: 3, muted: 4 };
export const worstTone = (tones: Tone[]): Tone => tones.reduce<Tone>((w, t) => (toneRank[t] < toneRank[w] ? t : w), "muted");

export const nodeId = (kind: string, namespace: string, name: string) => `${kind}/${namespace}/${name}`;

function replicasStatus(ready: number, desired: number, what = "ready"): { tone: Tone; status: string } {
  if (desired === 0) return { tone: "muted", status: "scaled to 0" };
  const tone: Tone = ready >= desired ? "ok" : ready === 0 ? "bad" : "warn";
  return { tone, status: `${ready}/${desired} ${what}` };
}

function statusOf(kind: MapKind, o: KObj): { tone: Tone; status: string } {
  const st = o.status ?? {};
  switch (kind) {
    case "Pod": {
      const p = podStatus(o);
      return { tone: p.tone, status: `${p.reason} · ${p.ready}/${p.total}${p.restarts ? ` · ${p.restarts} restarts` : ""}` };
    }
    case "Deployment":
    case "StatefulSet":
    case "ReplicaSet":
      return replicasStatus(st.readyReplicas ?? 0, o.spec?.replicas ?? 1);
    case "DaemonSet":
      return replicasStatus(st.numberReady ?? 0, st.desiredNumberScheduled ?? 0);
    case "Job": {
      if ((st.failed ?? 0) > 0 && !st.succeeded) return { tone: "bad", status: `${st.failed} failed` };
      if (st.active) return { tone: "info", status: `${st.active} running` };
      if (st.succeeded) return { tone: "muted", status: "complete" };
      return { tone: "info", status: "pending" };
    }
    case "CronJob":
      return o.spec?.suspend ? { tone: "muted", status: "suspended" } : { tone: "ok", status: o.spec?.schedule ?? "" };
    case "HorizontalPodAutoscaler":
      return { tone: "ok", status: `${st.currentReplicas ?? "?"} of ${o.spec?.minReplicas ?? 1}–${o.spec?.maxReplicas ?? "?"}` };
    case "PersistentVolumeClaim": {
      const phase = st.phase ?? "Pending";
      return { tone: phase === "Bound" ? "ok" : phase === "Lost" ? "bad" : "warn", status: `${phase}${o.spec?.resources?.requests?.storage ? ` · ${o.spec.resources.requests.storage}` : ""}` };
    }
    case "Service":
      return { tone: "ok", status: o.spec?.type ?? "ClusterIP" };
    case "Ingress": {
      const hosts = (o.spec?.rules ?? []).map((r: any) => r.host).filter(Boolean);
      return { tone: "ok", status: hosts.length ? hosts.slice(0, 2).join(", ") + (hosts.length > 2 ? ` +${hosts.length - 2}` : "") : "any host" };
    }
  }
  return { tone: "ok", status: "" };
}

/** ingressServices are the services an ingress sends traffic to. */
function ingressServices(o: KObj): string[] {
  const out = new Set<string>();
  const add = (b: any) => {
    const name = b?.service?.name ?? b?.serviceName;
    if (name) out.add(name);
  };
  add(o.spec?.defaultBackend ?? o.spec?.backend);
  for (const r of o.spec?.rules ?? []) for (const p of r.http?.paths ?? []) add(p.backend);
  return [...out];
}

/** podUses are the config maps, secrets and claims a pod needs. */
function podUses(p: KObj): { kind: MapKind; name: string }[] {
  const out = new Map<string, { kind: MapKind; name: string }>();
  const add = (kind: MapKind, name?: string) => {
    if (name && name !== "kube-root-ca.crt") out.set(`${kind}/${name}`, { kind, name });
  };
  for (const v of p.spec?.volumes ?? []) {
    add("ConfigMap", v.configMap?.name);
    add("Secret", v.secret?.secretName);
    add("PersistentVolumeClaim", v.persistentVolumeClaim?.claimName);
    for (const s of v.projected?.sources ?? []) {
      add("ConfigMap", s.configMap?.name);
      add("Secret", s.secret?.name);
    }
  }
  const containers = [...(p.spec?.initContainers ?? []), ...(p.spec?.containers ?? [])];
  for (const c of containers) {
    for (const e of c.envFrom ?? []) {
      add("ConfigMap", e.configMapRef?.name);
      add("Secret", e.secretRef?.name);
    }
    for (const e of c.env ?? []) {
      add("ConfigMap", e.valueFrom?.configMapKeyRef?.name);
      add("Secret", e.valueFrom?.secretKeyRef?.name);
    }
  }
  for (const s of p.spec?.imagePullSecrets ?? []) add("Secret", s.name);
  return [...out.values()];
}

const controllerOf = (o: KObj) => (o.metadata.ownerReferences ?? []).find((r) => r.controller) ?? o.metadata.ownerReferences?.[0];

/**
 * buildMap works out the map of a namespace's objects: what routes to what,
 * what owns what, and what pods use, in groups of objects that belong
 * together. config adds the config maps, secrets and claims pods use.
 */
export function buildMap(lists: MapLists, opts: { config: boolean } = { config: true }): MapGroup[] {
  const nodes = new Map<string, MapNode>();
  const edges = new Map<string, MapEdge>();
  const byUid = new Map<string, MapNode>();
  const objOf = new Map<string, KObj>();

  const add = (kind: MapKind, o: KObj) => {
    const ns = o.metadata.namespace ?? "";
    const id = nodeId(kind, ns, o.metadata.name);
    const n: MapNode = { id, kind, name: o.metadata.name, namespace: ns, column: columnOf[kind], ...statusOf(kind, o) };
    nodes.set(id, n);
    byUid.set(o.metadata.uid, n);
    objOf.set(id, o);
    return n;
  };
  const link = (from: string, to: string) => {
    if (from !== to) edges.set(`${from}>${to}`, { from, to });
  };
  // An object others name: the real one, or a missing one.
  const ref = (kind: MapKind, ns: string, name: string, known: boolean) => {
    const id = nodeId(kind, ns, name);
    if (!nodes.has(id)) {
      if (!known) return id; // its kind is not listed: cannot tell
      nodes.set(id, { id, kind, name, namespace: ns, column: columnOf[kind], tone: "bad", status: "missing", missing: true });
    }
    return id;
  };

  for (const kind of ["Ingress", "Service", "HorizontalPodAutoscaler", "CronJob", "Deployment", "StatefulSet", "DaemonSet"] as MapKind[]) {
    for (const o of lists[kind] ?? []) add(kind, o);
  }
  // ReplicaSets left behind by rollouts, at zero, are history: leave them out.
  for (const o of lists.ReplicaSet ?? []) {
    if ((o.spec?.replicas ?? 0) === 0 && (o.status?.replicas ?? 0) === 0 && controllerOf(o)) continue;
    add("ReplicaSet", o);
  }
  // The newest jobs of each cron job.
  const jobsByOwner = new Map<string, KObj[]>();
  for (const o of lists.Job ?? []) {
    const owner = controllerOf(o)?.uid ?? `self:${o.metadata.uid}`;
    jobsByOwner.set(owner, [...(jobsByOwner.get(owner) ?? []), o]);
  }
  for (const [owner, jobs] of jobsByOwner) {
    const keep = owner.startsWith("self:") ? jobs : [...jobs].sort((a, b) => (b.metadata.creationTimestamp ?? "").localeCompare(a.metadata.creationTimestamp ?? "")).slice(0, JOBS_PER_CRONJOB);
    for (const o of keep) add("Job", o);
  }

  // Pods, together when a workload has many.
  const pods = lists.Pod ?? [];
  const podsByOwner = new Map<string, KObj[]>();
  for (const p of pods) {
    const owner = controllerOf(p);
    const key = owner && byUid.has(owner.uid) ? owner.uid : `self:${p.metadata.uid}`;
    podsByOwner.set(key, [...(podsByOwner.get(key) ?? []), p]);
  }
  // podNode is the node that stands for a pod: its own, or its group's.
  const podNode = new Map<string, string>();
  for (const [owner, list] of podsByOwner) {
    const parent = byUid.get(owner);
    if (parent && list.length > GROUP_PODS) {
      const ns = parent.namespace;
      const id = nodeId("Pod", ns, `${parent.name}*`);
      const statuses = list.map((p) => statusOf("Pod", p));
      const problems = statuses.filter((s) => s.tone === "bad" || s.tone === "warn").length;
      nodes.set(id, {
        id,
        kind: "Pod",
        name: `${list.length} pods`,
        namespace: ns,
        column: columnOf.Pod,
        tone: worstTone(statuses.map((s) => s.tone)),
        status: problems ? `${problems} not ready` : "all ready",
        pods: list.map((p) => p.metadata.name).sort(),
      });
      for (const p of list) {
        podNode.set(p.metadata.uid, id);
        objOf.set(id, objOf.get(id) ?? p);
      }
      link(parent.id, id);
      continue;
    }
    for (const p of list) {
      const n = add("Pod", p);
      podNode.set(p.metadata.uid, n.id);
      if (parent) link(parent.id, n.id);
    }
  }

  // Owners of the workloads: cron jobs own jobs, deployments replica sets.
  for (const n of nodes.values()) {
    const o = objOf.get(n.id);
    if (!o || n.kind === "Pod") continue;
    const owner = controllerOf(o);
    const parent = owner && byUid.get(owner.uid);
    if (parent) link(parent.id, n.id);
  }

  // The top workload of a pod, which services point at.
  const topOf = (uid: string): MapNode | undefined => {
    let n = byUid.get(uid);
    for (let i = 0; n && i < 4; i++) {
      const o = objOf.get(n.id);
      const owner = o && controllerOf(o);
      const up = owner && byUid.get(owner.uid);
      if (!up) return n;
      n = up;
    }
    return n;
  };

  const listed = (k: MapKind) => lists[k] !== undefined;
  for (const svc of lists.Service ?? []) {
    const ns = svc.metadata.namespace ?? "";
    const id = nodeId("Service", ns, svc.metadata.name);
    const sel = svc.spec?.selector as Record<string, string> | undefined;
    if (svc.spec?.type === "ExternalName") {
      const n = nodes.get(id)!;
      n.tone = "muted";
      n.status = `→ ${svc.spec.externalName}`;
      continue;
    }
    if (!sel || !Object.keys(sel).length) continue; // endpoints are set by hand
    const selected = pods.filter((p) => (p.metadata.namespace ?? "") === ns && matchesSelector(p.metadata.labels, sel));
    const n = nodes.get(id)!;
    if (!selected.length) {
      n.tone = "warn";
      n.status = `${n.status} · no pods`;
      continue;
    }
    const ready = selected.filter((p) => podStatus(p).tone === "ok").length;
    n.status = `${n.status} · ${ready}/${selected.length} pods ready`;
    if (ready === 0) n.tone = "bad";
    for (const p of selected) {
      const owner = controllerOf(p);
      const top = owner ? topOf(owner.uid) : undefined;
      link(id, top?.id ?? podNode.get(p.metadata.uid) ?? nodeId("Pod", ns, p.metadata.name));
    }
  }

  for (const ing of lists.Ingress ?? []) {
    const ns = ing.metadata.namespace ?? "";
    const from = nodeId("Ingress", ns, ing.metadata.name);
    for (const svc of ingressServices(ing)) {
      const to = ref("Service", ns, svc, listed("Service"));
      if (nodes.get(to)?.missing) nodes.get(from)!.tone = "bad";
      link(from, to);
    }
  }

  for (const hpa of lists.HorizontalPodAutoscaler ?? []) {
    const ns = hpa.metadata.namespace ?? "";
    const t = hpa.spec?.scaleTargetRef;
    if (t?.kind && t?.name && t.kind in columnOf) link(nodeId("HorizontalPodAutoscaler", ns, hpa.metadata.name), ref(t.kind as MapKind, ns, t.name, listed(t.kind as MapKind)));
  }

  if (opts.config) {
    // What pods use, from the node that stands for them.
    const configOf = new Map<string, KObj>();
    for (const k of ["ConfigMap", "Secret", "PersistentVolumeClaim"] as MapKind[]) for (const o of lists[k] ?? []) configOf.set(nodeId(k, o.metadata.namespace ?? "", o.metadata.name), o);
    for (const p of pods) {
      const from = podNode.get(p.metadata.uid);
      if (!from) continue;
      const ns = p.metadata.namespace ?? "";
      for (const u of podUses(p)) {
        const id = nodeId(u.kind, ns, u.name);
        const o = configOf.get(id);
        if (o && !nodes.has(id)) add(u.kind, o);
        // Secrets of image pulls and optional ones may be missing on purpose.
        const to = o ? id : ref(u.kind, ns, u.name, listed(u.kind) && u.kind !== "Secret");
        if (nodes.has(to)) link(from, to);
      }
    }
  }

  return groupsOf(nodes, [...edges.values()]);
}

/** groupsOf splits the map into its connected parts. */
function groupsOf(nodes: Map<string, MapNode>, edges: MapEdge[]): MapGroup[] {
  const parent = new Map<string, string>();
  const find = (x: string): string => {
    let r = x;
    while (parent.get(r) !== r) r = parent.get(r)!;
    parent.set(x, r);
    return r;
  };
  for (const id of nodes.keys()) parent.set(id, id);
  const kept = edges.filter((e) => nodes.has(e.from) && nodes.has(e.to));
  for (const e of kept) parent.set(find(e.from), find(e.to));
  const groups = new Map<string, MapGroup>();
  for (const n of nodes.values()) {
    const root = find(n.id);
    const g = groups.get(root) ?? { id: root, title: "", nodes: [], edges: [], worst: "muted" as Tone };
    g.nodes.push(n);
    groups.set(root, g);
  }
  for (const e of kept) groups.get(find(e.from))!.edges.push(e);
  const out = [...groups.values()];
  for (const g of out) {
    g.worst = worstTone(g.nodes.map((n) => n.tone));
    // Named after its leftmost workload, or its first object.
    const lead = [...g.nodes].sort((a, b) => (a.column === 2 ? -1 : 0) - (b.column === 2 ? -1 : 0) || a.column - b.column || a.name.localeCompare(b.name))[0]!;
    g.title = lead.name;
    g.id = lead.id;
  }
  return out.sort((a, b) => toneRank[a.worst] - toneRank[b.worst] || b.nodes.length - a.nodes.length || a.title.localeCompare(b.title));
}

export interface Placed {
  node: MapNode;
  x: number;
  y: number;
}

export interface GroupLayout {
  group: MapGroup;
  placed: Placed[];
  width: number;
  height: number;
}

export const NODE_W = 210;
export const NODE_H = 50;
const COL_GAP = 64;
const ROW_GAP = 12;

/**
 * layoutGroup places a group's nodes in columns, ordering each column by
 * where its neighbors are, to keep lines from crossing.
 */
export function layoutGroup(g: MapGroup): GroupLayout {
  const cols = [...new Set(g.nodes.map((n) => n.column))].sort((a, b) => a - b);
  const columns = cols.map((c) => g.nodes.filter((n) => n.column === c).sort((a, b) => a.name.localeCompare(b.name)));
  const neighbors = new Map<string, string[]>();
  for (const e of g.edges) {
    neighbors.set(e.from, [...(neighbors.get(e.from) ?? []), e.to]);
    neighbors.set(e.to, [...(neighbors.get(e.to) ?? []), e.from]);
  }
  const pos = new Map<string, number>();
  const index = () => columns.forEach((col) => col.forEach((n, i) => pos.set(n.id, i / Math.max(1, col.length - 1 || 1))));
  index();
  const order = (col: MapNode[]) => {
    const score = (n: MapNode) => {
      const ps = (neighbors.get(n.id) ?? []).map((id) => pos.get(id)).filter((p): p is number => p !== undefined);
      return ps.length ? ps.reduce((a, b) => a + b, 0) / ps.length : (pos.get(n.id) ?? 0);
    };
    col.sort((a, b) => score(a) - score(b) || a.name.localeCompare(b.name));
  };
  for (let pass = 0; pass < 4; pass++) {
    for (let i = 1; i < columns.length; i++) {
      order(columns[i]!);
      index();
    }
    for (let i = columns.length - 2; i >= 0; i--) {
      order(columns[i]!);
      index();
    }
  }
  const tallest = Math.max(...columns.map((c) => c.length));
  const height = tallest * NODE_H + (tallest - 1) * ROW_GAP;
  const placed: Placed[] = [];
  columns.forEach((col, ci) => {
    const colH = col.length * NODE_H + (col.length - 1) * ROW_GAP;
    const top = (height - colH) / 2;
    col.forEach((n, i) => placed.push({ node: n, x: ci * (NODE_W + COL_GAP), y: top + i * (NODE_H + ROW_GAP) }));
  });
  return { group: g, placed, width: columns.length * NODE_W + (columns.length - 1) * COL_GAP, height };
}
