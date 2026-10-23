import { FileText, GitCompareArrows, CirclePause, CirclePlay, FileCode2, Maximize2, Play, RotateCw, ScrollText, ShieldOff, ShieldCheck, SquareTerminal, Trash2, Wind } from "lucide-react";
import type { GVR } from "../mygo";
import type { MenuItem } from "../components/Menu";
import { cordon, deleteObjects, drain, openPodTab, restart, scale, suspendCronJob, triggerCronJob } from "./actions";
import type { KObj } from "./k8s";
import { useApp } from "./store";
import { canDo, noAccess } from "./access";

const scalable = new Set(["Deployment", "StatefulSet", "ReplicaSet"]);
const restartable = new Set(["Deployment", "StatefulSet", "DaemonSet"]);
const comparable = new Set(["Deployment", "StatefulSet", "DaemonSet", "Service", "ConfigMap", "Secret", "Ingress", "CronJob", "HorizontalPodAutoscaler"]);
const reportable = new Set(["Pod", "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job"]);

/** objectActions are what can be done to an object, for menus and toolbars. */
export function objectActions(cluster: string, gvr: GVR, kind: string, o: KObj, opts: { inDetails?: boolean } = {}): MenuItem[] {
  const items: MenuItem[] = [];
  const s = 15;
  const ro = !!useApp.getState().clusters.find((c) => c.id === cluster)?.readOnly;
  const ns = o.metadata.namespace ?? "";
  // Disables what the user's access does not allow.
  const allow = (verb: string, g: GVR = gvr, sub?: string) => (canDo(cluster, verb, g, ns, sub) ? {} : { disabled: true, hint: noAccess });
  const pods: GVR = { group: "", version: "v1", resource: "pods" };
  const report: MenuItem | null = reportable.has(kind)
    ? {
        label: "Incident report",
        icon: <FileText size={s} />,
        run: () => useApp.getState().openDialog({ kind: "report", cluster, object: kind, namespace: o.metadata.namespace ?? "", name: o.metadata.name }),
      }
    : null;
  if (kind === "Pod") {
    items.push({ label: "Logs", icon: <ScrollText size={s} />, run: () => openPodTab(o, "logs"), ...allow("get", pods, "log") });
    if (!ro) items.push({ label: "Shell", icon: <SquareTerminal size={s} />, run: () => openPodTab(o, "terminal"), ...allow("create", pods, "exec") });
  }
  if (report) items.push(report);
  if (comparable.has(kind) && useApp.getState().clusters.filter((c) => !c.hidden).length > 1) {
    items.push({
      label: "Compare across clusters",
      icon: <GitCompareArrows size={s} />,
      run: () => useApp.getState().openDialog({ kind: "compare", gvr, object: kind, namespace: o.metadata.namespace ?? "", name: o.metadata.name }),
    });
  }
  if (ro) {
    if (!opts.inDetails) {
      items.push({
        label: "View YAML",
        icon: <FileCode2 size={s} />,
        separatorBefore: items.length > 0,
        run: () => useApp.getState().openDetail({ gvr, kind, namespace: o.metadata.namespace ?? "", name: o.metadata.name, tab: "yaml" }, true),
      });
    }
    return items;
  }
  if (scalable.has(kind)) items.push({ label: "Scale…", icon: <Maximize2 size={s} />, run: () => scale(cluster, gvr, o), ...allow("patch") });
  if (restartable.has(kind)) items.push({ label: "Restart", icon: <RotateCw size={s} />, run: () => restart(cluster, gvr, o), ...allow("patch") });
  if (kind === "CronJob") {
    items.push({ label: "Run now", icon: <Play size={s} />, run: () => triggerCronJob(cluster, o), ...allow("create", { group: "batch", version: "v1", resource: "jobs" }) });
    items.push(
      o.spec?.suspend
        ? { label: "Resume", icon: <CirclePlay size={s} />, run: () => suspendCronJob(cluster, o, false), ...allow("patch") }
        : { label: "Suspend", icon: <CirclePause size={s} />, run: () => suspendCronJob(cluster, o, true), ...allow("patch") },
    );
  }
  if (kind === "Node") {
    items.push(
      o.spec?.unschedulable
        ? { label: "Uncordon", icon: <ShieldCheck size={s} />, run: () => cordon(cluster, o, false), ...allow("patch") }
        : { label: "Cordon", icon: <ShieldOff size={s} />, run: () => cordon(cluster, o, true), ...allow("patch") },
    );
    items.push({ label: "Drain…", icon: <Wind size={s} />, run: () => drain(cluster, o), ...allow("patch") });
  }
  if (!opts.inDetails) {
    items.push({
      label: "Edit YAML",
      icon: <FileCode2 size={s} />,
      separatorBefore: items.length > 0,
      run: () => useApp.getState().openDetail({ gvr, kind, namespace: o.metadata.namespace ?? "", name: o.metadata.name, tab: "yaml" }, true),
    });
  }
  items.push({
    label: "Delete…",
    icon: <Trash2 size={s} />,
    danger: true,
    ...allow("delete"),
    separatorBefore: opts.inDetails && items.length > 0,
    run: async () => {
      const deleted = await deleteObjects(cluster, gvr, kind, [o]);
      if (deleted && opts.inDetails) useApp.getState().backDetail();
    },
  });
  return items;
}
