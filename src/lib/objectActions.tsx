import { FileText, GitCompareArrows, CirclePause, CirclePlay, FileCode2, Maximize2, Play, RotateCw, ScrollText, ShieldOff, ShieldCheck, SquareTerminal, Trash2, Wind } from "lucide-react";
import type { GVR } from "../mygo";
import type { MenuItem } from "../components/Menu";
import { cordon, deleteObjects, drain, openPodTab, restart, scale, suspendCronJob, triggerCronJob } from "./actions";
import type { KObj } from "./k8s";
import { useApp } from "./store";

const scalable = new Set(["Deployment", "StatefulSet", "ReplicaSet"]);
const restartable = new Set(["Deployment", "StatefulSet", "DaemonSet"]);
const comparable = new Set(["Deployment", "StatefulSet", "DaemonSet", "Service", "ConfigMap", "Secret", "Ingress", "CronJob", "HorizontalPodAutoscaler"]);
const reportable = new Set(["Pod", "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job"]);

/** objectActions are what can be done to an object, for menus and toolbars. */
export function objectActions(cluster: string, gvr: GVR, kind: string, o: KObj, opts: { inDetails?: boolean } = {}): MenuItem[] {
  const items: MenuItem[] = [];
  const s = 15;
  const ro = !!useApp.getState().clusters.find((c) => c.id === cluster)?.readOnly;
  const report: MenuItem | null = reportable.has(kind)
    ? {
        label: "Incident report",
        icon: <FileText size={s} />,
        run: () => useApp.getState().openDialog({ kind: "report", cluster, object: kind, namespace: o.metadata.namespace ?? "", name: o.metadata.name }),
      }
    : null;
  if (kind === "Pod") {
    items.push({ label: "Logs", icon: <ScrollText size={s} />, run: () => openPodTab(o, "logs") });
    if (!ro) items.push({ label: "Shell", icon: <SquareTerminal size={s} />, run: () => openPodTab(o, "terminal") });
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
  if (scalable.has(kind)) items.push({ label: "Scale…", icon: <Maximize2 size={s} />, run: () => scale(cluster, gvr, o) });
  if (restartable.has(kind)) items.push({ label: "Restart", icon: <RotateCw size={s} />, run: () => restart(cluster, gvr, o) });
  if (kind === "CronJob") {
    items.push({ label: "Run now", icon: <Play size={s} />, run: () => triggerCronJob(cluster, o) });
    items.push(
      o.spec?.suspend
        ? { label: "Resume", icon: <CirclePlay size={s} />, run: () => suspendCronJob(cluster, o, false) }
        : { label: "Suspend", icon: <CirclePause size={s} />, run: () => suspendCronJob(cluster, o, true) },
    );
  }
  if (kind === "Node") {
    items.push(
      o.spec?.unschedulable
        ? { label: "Uncordon", icon: <ShieldCheck size={s} />, run: () => cordon(cluster, o, false) }
        : { label: "Cordon", icon: <ShieldOff size={s} />, run: () => cordon(cluster, o, true) },
    );
    items.push({ label: "Drain…", icon: <Wind size={s} />, run: () => drain(cluster, o) });
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
    separatorBefore: opts.inDetails && items.length > 0,
    run: async () => {
      const deleted = await deleteObjects(cluster, gvr, kind, [o]);
      if (deleted && opts.inDetails) useApp.getState().backDetail();
    },
  });
  return items;
}
