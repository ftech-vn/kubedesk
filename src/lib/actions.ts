import { create } from "zustand";
import { Kube, type GVR, type ImpactRequest, type PortForward } from "../mygo";
import { ask, run, toast, useApp } from "./store";
import type { KObj } from "./k8s";

const kinds: Record<string, string> = { deployments: "Deployment", statefulsets: "StatefulSet", daemonsets: "DaemonSet", replicasets: "ReplicaSet" };
const kindOf = (gvr: GVR) => kinds[gvr.resource] ?? gvr.resource;

// ---- Guards of production and read-only clusters ----

function clusterOf(id: string) {
  return useApp.getState().clusters.find((c) => c.id === id);
}

/** readOnly reports, and says, when a cluster refuses changes in the app. */
export function readOnly(cluster: string): boolean {
  const c = clusterOf(cluster);
  if (!c?.readOnly) return false;
  toast.error(`${c.name} is read-only`, "Turn off read-only in the cluster's settings to make changes.");
  return true;
}

/** typeToConfirm is the name to type before changing a production cluster. */
export function typeToConfirm(cluster: string): string | undefined {
  const c = clusterOf(cluster);
  return c?.production ? c.name : undefined;
}

/**
 * confirmChange lets a change through: never on a read-only cluster, after
 * typing the cluster's name on a production one, at once otherwise.
 */
export async function confirmChange(cluster: string, title: string, message?: string): Promise<boolean> {
  if (readOnly(cluster)) return false;
  const name = typeToConfirm(cluster);
  if (!name) return true;
  return (await ask({ title, message, confirmLabel: "Apply to production", danger: true, typeToConfirm: name })).ok;
}

/** impactOf previews a change of the objects, merged when there are several. */
function impactOf(cluster: string, req: (o: KObj) => ImpactRequest, objs: KObj[]) {
  return async () => {
    const lists = await Promise.all(objs.slice(0, 5).map((o) => Kube.impact(cluster, req(o))));
    if (objs.length === 1) return lists[0] ?? [];
    return lists.flatMap((l, i) => l.map((it) => ({ ...it, text: `${objs[i]!.metadata.name}: ${it.text}` })));
  };
}

const ref = (kind: string, o: KObj) => ({ kind, namespace: o.metadata.namespace ?? "", name: o.metadata.name });

export async function deleteObjects(cluster: string, gvr: GVR, kind: string, objs: KObj[]): Promise<boolean> {
  if (!objs.length || readOnly(cluster)) return false;
  const names = objs.map((o) => (o.metadata.namespace ? `${o.metadata.namespace}/${o.metadata.name}` : o.metadata.name));
  const r = await ask({
    title: objs.length === 1 ? `Delete ${kind} ${objs[0]!.metadata.name}?` : `Delete ${objs.length} ${kind}s?`,
    message: objs.length === 1 ? `${names[0]} will be deleted from the cluster.` : names.slice(0, 12).join("\n") + (names.length > 12 ? `\n…and ${names.length - 12} more` : ""),
    confirmLabel: "Delete",
    danger: true,
    checkbox: kind === "Pod" ? "Force (no grace period)" : undefined,
    typeToConfirm: typeToConfirm(cluster),
    impact: kind === "Event" ? undefined : impactOf(cluster, (o) => ({ action: "delete", ...ref(kind, o) }), objs),
  });
  if (!r.ok) return false;
  const failures: string[] = [];
  await Promise.all(
    objs.map((o) =>
      Kube.delete(cluster, gvr, o.metadata.namespace ?? "", o.metadata.name, r.checked).catch((e) => failures.push(`${o.metadata.name}: ${e.message ?? e}`)),
    ),
  );
  if (failures.length) toast.error(`Could not delete ${failures.length} of ${objs.length}`, failures.join("\n"));
  else toast.ok(objs.length === 1 ? `Deleted ${names[0]}` : `Deleted ${objs.length} ${kind}s`);
  return failures.length === 0;
}

export async function scale(cluster: string, gvr: GVR, o: KObj) {
  if (readOnly(cluster)) return;
  const r = await ask({
    title: `Scale ${o.metadata.name}`,
    input: { label: "Replicas", value: String(o.spec?.replicas ?? 1), type: "number" },
    confirmLabel: "Scale",
    typeToConfirm: typeToConfirm(cluster),
    impact: (v) => Kube.impact(cluster, { action: "scale", ...ref(kindOf(gvr), o), replicas: Math.max(0, Number(v) || 0) }),
  });
  if (!r.ok) return;
  const n = Number(r.value);
  if (!Number.isInteger(n) || n < 0) return toast.error("Replicas must be a whole number, 0 or more");
  await run("Could not scale", () => Kube.scale(cluster, gvr, o.metadata.namespace ?? "", o.metadata.name, n), `Scaled ${o.metadata.name} to ${n}`);
}

export async function restart(cluster: string, gvr: GVR, o: KObj) {
  if (readOnly(cluster)) return;
  const r = await ask({
    title: `Restart ${o.metadata.name}?`,
    message: "Its pods are replaced, as with kubectl rollout restart.",
    confirmLabel: "Restart",
    typeToConfirm: typeToConfirm(cluster),
    impact: () => Kube.impact(cluster, { action: "restart", ...ref(kindOf(gvr), o) }),
  });
  if (!r.ok) return;
  await run("Could not restart", () => Kube.restart(cluster, gvr, o.metadata.namespace ?? "", o.metadata.name), `Restarting ${o.metadata.name}`);
}

export async function cordon(cluster: string, node: KObj, unschedulable: boolean) {
  if (!(await confirmChange(cluster, `${unschedulable ? "Cordon" : "Uncordon"} ${node.metadata.name}?`))) return;
  await run(
    unschedulable ? "Could not cordon" : "Could not uncordon",
    () => Kube.cordon(cluster, node.metadata.name, unschedulable),
    unschedulable ? `Cordoned ${node.metadata.name}` : `Uncordoned ${node.metadata.name}`,
  );
}

export async function drain(cluster: string, node: KObj) {
  if (readOnly(cluster)) return;
  const r = await ask({
    title: `Drain ${node.metadata.name}?`,
    message: "The node is cordoned and its pods evicted, except those of daemon sets. Disruption budgets are respected.",
    confirmLabel: "Drain",
    danger: true,
    typeToConfirm: typeToConfirm(cluster),
    impact: () => Kube.impact(cluster, { action: "drain", kind: "Node", namespace: "", name: node.metadata.name }),
  });
  if (!r.ok) return;
  toast.info(`Draining ${node.metadata.name}…`);
  const evicted = await run("Drain incomplete", () => Kube.drain(cluster, node.metadata.name));
  if (evicted) toast.ok(`Drained ${node.metadata.name}`, `${evicted.length} pods evicted`);
}

export async function triggerCronJob(cluster: string, o: KObj) {
  if (!(await confirmChange(cluster, `Run ${o.metadata.name} now?`, "A job is created from the cron job's template."))) return;
  const job = await run("Could not start the job", () => Kube.triggerCronJob(cluster, o.metadata.namespace ?? "", o.metadata.name));
  if (job) toast.ok(`Started job ${job}`);
}

export async function suspendCronJob(cluster: string, o: KObj, suspend: boolean) {
  if (!(await confirmChange(cluster, `${suspend ? "Suspend" : "Resume"} ${o.metadata.name}?`))) return;
  await run("Could not update", () => Kube.setSuspended(cluster, o.metadata.namespace ?? "", o.metadata.name, suspend), suspend ? `Suspended ${o.metadata.name}` : `Resumed ${o.metadata.name}`);
}

export async function portForward(cluster: string, namespace: string, pod: string, port: number) {
  const r = await ask({
    title: `Forward port ${port} of ${pod}`,
    input: { label: "Local port (empty for any free port)", value: String(port >= 1024 ? port : ""), type: "number", placeholder: "auto" },
    confirmLabel: "Start",
  });
  if (!r.ok) return;
  const pf = await run("Could not forward the port", () => Kube.startPortForward(cluster, namespace, pod, port, Number(r.value) || 0));
  if (pf) {
    usePortForwards.getState().refresh();
    toast.ok(`Forwarding localhost:${pf.localPort} → ${pod}:${port}`);
  }
}

// The list of port forwards, shown in the title bar.
export const usePortForwards = create<{ list: PortForward[]; refresh(): Promise<void>; stop(id: string): Promise<void> }>((set, get) => ({
  list: [],
  async refresh() {
    set({ list: await Kube.portForwards() });
  },
  async stop(id) {
    await Kube.stopPortForward(id);
    await get().refresh();
  },
}));

/** openPodTab opens the logs or a shell of a pod in the details panel. */
export function openPodTab(o: KObj, tab: "logs" | "terminal") {
  useApp.getState().openDetail({ gvr: { group: "", version: "v1", resource: "pods" }, kind: "Pod", namespace: o.metadata.namespace ?? "", name: o.metadata.name, tab }, true);
}
