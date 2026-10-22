import type { GVR } from "../mygo";

/** A Kubernetes object as the API returns it. */
export interface KObj {
  apiVersion?: string;
  kind?: string;
  metadata: {
    name: string;
    namespace?: string;
    uid: string;
    creationTimestamp?: string;
    deletionTimestamp?: string;
    labels?: Record<string, string>;
    annotations?: Record<string, string>;
    ownerReferences?: { apiVersion: string; kind: string; name: string; uid: string; controller?: boolean }[];
    resourceVersion?: string;
    generation?: number;
  };
  // Resources differ in everything else; views read it through helpers.
  spec?: any;
  status?: any;
  data?: Record<string, string>;
  [key: string]: any;
}

export const gvrKey = (g: GVR) => `${g.group}/${g.version}/${g.resource}`;

export function apiVersionOf(g: GVR) {
  return g.group ? `${g.group}/${g.version}` : g.version;
}

/** Age formats the time since a timestamp the way kubectl does. */
export function age(ts?: string, now = Date.now()): string {
  if (!ts) return "—";
  const s = Math.max(0, Math.floor((now - Date.parse(ts)) / 1000));
  if (s < 120) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 10) return `${m}m${s % 60 ? `${s % 60}s` : ""}`;
  if (m < 180) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 8) return `${h}h${m % 60 ? `${m % 60}m` : ""}`;
  if (h < 48) return `${h}h`;
  const d = Math.floor(h / 24);
  if (d < 8) return `${d}d${h % 24 ? `${h % 24}h` : ""}`;
  if (d < 365 * 2) return `${d}d`;
  return `${Math.floor(d / 365)}y`;
}

export const timestamp = (o: KObj) => (o.metadata.creationTimestamp ? Date.parse(o.metadata.creationTimestamp) : 0);

/** Tone is the color a status shows in. */
export type Tone = "ok" | "warn" | "bad" | "info" | "muted";

export interface PodStatus {
  reason: string;
  tone: Tone;
  ready: number;
  total: number;
  restarts: number;
}

/** podStatus computes the status column of kubectl get pods. */
export function podStatus(p: KObj): PodStatus {
  const st = p.status ?? {};
  const containers: any[] = st.containerStatuses ?? [];
  const total = (p.spec?.containers ?? []).length;
  let ready = 0;
  let restarts = 0;
  let reason: string = st.reason || st.phase || "Unknown";
  let initializing = false;
  (st.initContainerStatuses ?? []).forEach((c: any, i: number) => {
    restarts += c.restartCount ?? 0;
    if (c.state?.terminated?.exitCode === 0) return;
    if (c.state?.terminated) {
      reason = c.state.terminated.reason ? `Init:${c.state.terminated.reason}` : `Init:ExitCode:${c.state.terminated.exitCode}`;
      initializing = true;
    } else if (c.state?.waiting?.reason && c.state.waiting.reason !== "PodInitializing") {
      reason = `Init:${c.state.waiting.reason}`;
      initializing = true;
    } else if (!c.started || !c.ready) {
      if (c.restartPolicy === "Always" && c.started) return; // sidecar
      reason = `Init:${i}/${(p.spec?.initContainers ?? []).length}`;
      initializing = true;
    }
  });
  if (!initializing) {
    let hasRunning = false;
    for (const c of [...containers].reverse()) {
      restarts += c.restartCount ?? 0;
      if (c.state?.waiting?.reason) reason = c.state.waiting.reason;
      else if (c.state?.terminated?.reason) reason = c.state.terminated.reason;
      else if (c.state?.terminated) reason = c.state.terminated.signal ? `Signal:${c.state.terminated.signal}` : `ExitCode:${c.state.terminated.exitCode}`;
      else if (c.ready && c.state?.running) {
        hasRunning = true;
        ready++;
      }
    }
    if (reason === "Completed" && hasRunning) reason = "Running";
  } else {
    for (const c of containers) restarts += c.restartCount ?? 0;
  }
  if (p.metadata.deletionTimestamp) reason = st.reason === "NodeLost" ? "Unknown" : "Terminating";
  return { reason, tone: toneOf(reason, ready, total), ready, total, restarts };
}

function toneOf(reason: string, ready: number, total: number): Tone {
  if (reason === "Running") return ready === total ? "ok" : "warn";
  if (reason === "Succeeded" || reason === "Completed") return "muted";
  if (reason === "Pending" || reason === "ContainerCreating" || reason === "PodInitializing" || reason === "Terminating" || reason.startsWith("Init:") && /^Init:\d/.test(reason)) return "info";
  return "bad";
}

export interface NodeStatus {
  ready: boolean;
  schedulable: boolean;
  label: string;
  tone: Tone;
}

export function nodeStatus(n: KObj): NodeStatus {
  const cond = (n.status?.conditions ?? []).find((c: any) => c.type === "Ready");
  const ready = cond?.status === "True";
  const schedulable = !n.spec?.unschedulable;
  const label = (ready ? "Ready" : cond?.status === "Unknown" ? "Unknown" : "NotReady") + (schedulable ? "" : ", SchedulingDisabled");
  return { ready, schedulable, label, tone: !ready ? "bad" : schedulable ? "ok" : "warn" };
}

export function nodeRoles(n: KObj): string {
  const roles = Object.keys(n.metadata.labels ?? {})
    .filter((l) => l.startsWith("node-role.kubernetes.io/"))
    .map((l) => l.slice("node-role.kubernetes.io/".length))
    .filter(Boolean);
  return roles.length ? roles.join(", ") : "<none>";
}

/** parseQuantity turns a Kubernetes quantity into a number (cores, bytes). */
export function parseQuantity(q: string | number | undefined): number {
  if (q === undefined || q === null || q === "") return 0;
  if (typeof q === "number") return q;
  const m = /^([+-]?[0-9.]+(?:[eE][+-]?[0-9]+)?)([a-zA-Z]*)$/.exec(q.trim());
  if (!m) return 0;
  const n = parseFloat(m[1]!);
  const suffix = m[2] ?? "";
  const bin: Record<string, number> = { Ki: 2 ** 10, Mi: 2 ** 20, Gi: 2 ** 30, Ti: 2 ** 40, Pi: 2 ** 50, Ei: 2 ** 60 };
  const dec: Record<string, number> = { n: 1e-9, u: 1e-6, m: 1e-3, "": 1, k: 1e3, K: 1e3, M: 1e6, G: 1e9, T: 1e12, P: 1e15, E: 1e18 };
  if (suffix in bin) return n * bin[suffix]!;
  if (suffix in dec) return n * dec[suffix]!;
  return n;
}

export function formatBytes(n: number): string {
  if (!n) return "0";
  const units = ["B", "Ki", "Mi", "Gi", "Ti", "Pi"];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return `${n >= 100 || i === 0 ? n.toFixed(0) : n.toFixed(1)}${units[i]}`;
}

export function formatCores(n: number): string {
  if (!n) return "0";
  if (n < 1) return `${Math.round(n * 1000)}m`;
  return n.toFixed(n >= 10 ? 0 : 2).replace(/\.?0+$/, "");
}

/** podRequests sums the requests of a pod's containers. */
export function podRequests(p: KObj): { cpu: number; memory: number } {
  let cpu = 0;
  let memory = 0;
  for (const c of p.spec?.containers ?? []) {
    cpu += parseQuantity(c.resources?.requests?.cpu);
    memory += parseQuantity(c.resources?.requests?.memory);
  }
  return { cpu, memory };
}

export function matchesSelector(labels: Record<string, string> | undefined, selector: Record<string, string> | undefined): boolean {
  if (!selector || Object.keys(selector).length === 0) return false;
  const l = labels ?? {};
  return Object.entries(selector).every(([k, v]) => l[k] === v);
}

/** labelSelector reads the matchLabels of a selector, of either form. */
export function selectorOf(o: KObj): Record<string, string> | undefined {
  const s = o.spec?.selector;
  if (!s) return undefined;
  if (s.matchLabels || s.matchExpressions) return s.matchLabels;
  return s;
}

export function decodeBase64(s: string): string {
  try {
    const bin = atob(s);
    const bytes = Uint8Array.from(bin, (c) => c.charCodeAt(0));
    return new TextDecoder().decode(bytes);
  } catch {
    return s;
  }
}

export function base64ToBytes(s: string): Uint8Array {
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

export function errorMessage(e: unknown): string {
  if (e instanceof Error) return e.message;
  if (typeof e === "string") return e;
  try {
    return JSON.stringify(e);
  } catch {
    return String(e);
  }
}
