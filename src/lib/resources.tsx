import type { ReactNode } from "react";
import {
  Activity,
  Box,
  Boxes,
  CalendarClock,
  Container,
  Copy,
  Database,
  FileCode2,
  FileKey,
  Gauge,
  HardDrive,
  KeyRound,
  Layers,
  LayoutGrid,
  ListChecks,
  Network,
  Puzzle,
  Route,
  Server,
  ServerCog,
  Shield,
  ShieldCheck,
  Split,
  UserCog,
  Users,
  Waypoints,
  type LucideIcon,
} from "lucide-react";
import type { GVR } from "../mygo";
import { Badge } from "../components/ui";
import { age, formatBytes, formatCores, nodeRoles, nodeStatus, parseQuantity, podStatus, timestamp, type KObj, type Tone } from "./k8s";

export interface Column {
  id: string;
  title: string;
  /** value is what the column sorts and filters by. */
  value(o: KObj, now: number): string | number;
  render?(o: KObj, now: number): ReactNode;
  width?: number | string;
  align?: "right";
  mono?: boolean;
  /** hideable columns are dropped first in narrow windows. */
  minor?: boolean;
}

export interface ResourceDef {
  key: string;
  title: string;
  kind: string;
  gvr: GVR;
  namespaced: boolean;
  group: string;
  icon: LucideIcon;
  columns: Column[];
  /** defaultSort is the column a list sorts by first, descending with "-". */
  defaultSort?: string;
}

const v1 = (resource: string): GVR => ({ group: "", version: "v1", resource });
const g = (group: string, version: string, resource: string): GVR => ({ group, version, resource });

export const nameCol: Column = {
  id: "name",
  title: "Name",
  value: (o) => o.metadata.name,
  render: (o) => <span className="cell-name">{o.metadata.name}</span>,
  width: "minmax(220px, 2.2fr)",
};
export const nsCol: Column = { id: "namespace", title: "Namespace", value: (o) => o.metadata.namespace ?? "", width: "minmax(120px, 1fr)" };
export const ageCol: Column = {
  id: "age",
  title: "Age",
  value: (o) => -timestamp(o),
  render: (o, now) => <span title={o.metadata.creationTimestamp}>{age(o.metadata.creationTimestamp, now)}</span>,
  width: "72px",
  align: "right",
};

const col = (id: string, title: string, value: (o: KObj, now: number) => string | number, extra: Partial<Column> = {}): Column => ({ id, title, value, ...extra });
const tone = (t: Tone, text: string) => <Badge tone={t} dot>{text}</Badge>;

const ready = (r: number | undefined, d: number | undefined) => `${r ?? 0}/${d ?? 0}`;
const readyTone = (r = 0, d = 0): Tone => (d === 0 ? "muted" : r >= d ? "ok" : r === 0 ? "bad" : "warn");

function jobStatus(o: KObj): { text: string; tone: Tone } {
  const conds: any[] = o.status?.conditions ?? [];
  if (conds.some((c) => c.type === "Complete" && c.status === "True")) return { text: "Complete", tone: "ok" };
  if (conds.some((c) => c.type === "Failed" && c.status === "True")) return { text: "Failed", tone: "bad" };
  if (o.spec?.suspend) return { text: "Suspended", tone: "muted" };
  if (o.status?.active) return { text: "Running", tone: "info" };
  return { text: "Pending", tone: "muted" };
}

function duration(o: KObj, now: number) {
  const start = o.status?.startTime ? Date.parse(o.status.startTime) : 0;
  if (!start) return "—";
  const end = o.status?.completionTime ? Date.parse(o.status.completionTime) : now;
  return age(new Date(start).toISOString(), end);
}

function servicePorts(o: KObj) {
  return (o.spec?.ports ?? [])
    .map((p: any) => `${p.port}${p.nodePort ? `:${p.nodePort}` : ""}/${p.protocol ?? "TCP"}`)
    .join(", ");
}

function externalIP(o: KObj) {
  const lb = (o.status?.loadBalancer?.ingress ?? []).map((i: any) => i.ip || i.hostname);
  const ext = o.spec?.externalIPs ?? [];
  const all = [...lb, ...ext];
  if (all.length) return all.join(", ");
  return o.spec?.type === "LoadBalancer" ? "<pending>" : "—";
}

export const resources: ResourceDef[] = [
  // ---- Cluster ----
  {
    key: "nodes",
    title: "Nodes",
    kind: "Node",
    gvr: v1("nodes"),
    namespaced: false,
    group: "Cluster",
    icon: Server,
    columns: [
      nameCol,
      col("status", "Status", (o) => nodeStatus(o).label, { render: (o) => tone(nodeStatus(o).tone, nodeStatus(o).label), width: "minmax(110px, 1fr)" }),
      col("roles", "Roles", (o) => nodeRoles(o), { width: "minmax(100px, 1fr)" }),
      col("version", "Version", (o) => o.status?.nodeInfo?.kubeletVersion ?? "", { width: "minmax(110px, 0.8fr)", minor: true }),
      col("ip", "Internal IP", (o) => (o.status?.addresses ?? []).find((a: any) => a.type === "InternalIP")?.address ?? "", { mono: true, width: "minmax(110px, 0.8fr)" }),
      col("cpu", "CPU", (o) => parseQuantity(o.status?.capacity?.cpu), { render: (o) => formatCores(parseQuantity(o.status?.capacity?.cpu)), align: "right", width: "64px" }),
      col("memory", "Memory", (o) => parseQuantity(o.status?.capacity?.memory), { render: (o) => formatBytes(parseQuantity(o.status?.capacity?.memory)), align: "right", width: "80px" }),
      col("os", "OS", (o) => `${o.status?.nodeInfo?.osImage ?? ""}`, { width: "minmax(120px, 1fr)", minor: true }),
      ageCol,
    ],
  },
  {
    key: "namespaces",
    title: "Namespaces",
    kind: "Namespace",
    gvr: v1("namespaces"),
    namespaced: false,
    group: "Cluster",
    icon: LayoutGrid,
    columns: [
      nameCol,
      col("status", "Status", (o) => o.status?.phase ?? "", { render: (o) => tone(o.status?.phase === "Active" ? "ok" : "warn", o.status?.phase ?? "Unknown"), width: "120px" }),
      col("labels", "Labels", (o) => Object.keys(o.metadata.labels ?? {}).length, { align: "right", width: "80px", minor: true }),
      ageCol,
    ],
  },
  {
    key: "events",
    title: "Events",
    kind: "Event",
    gvr: v1("events"),
    namespaced: true,
    group: "Cluster",
    icon: Activity,
    defaultSort: "lastSeen",
    columns: [
      col("type", "Type", (o) => o.type ?? "", { render: (o) => tone(o.type === "Warning" ? "warn" : "muted", o.type ?? ""), width: "100px" }),
      col("reason", "Reason", (o) => o.reason ?? "", { width: "minmax(110px, 0.8fr)" }),
      col("object", "Object", (o) => `${o.involvedObject?.kind}/${o.involvedObject?.name}`, { render: (o) => <span className="cell-name">{`${o.involvedObject?.kind?.toLowerCase()}/${o.involvedObject?.name}`}</span>, width: "minmax(180px, 1.4fr)" }),
      nsCol,
      col("message", "Message", (o) => o.message ?? "", { width: "minmax(240px, 3fr)", render: (o) => <span className="cell-wrap">{o.message}</span> }),
      col("count", "Count", (o) => o.count ?? o.series?.count ?? 1, { align: "right", width: "60px" }),
      col("lastSeen", "Last seen", (o) => -eventTime(o), { render: (o, now) => age(new Date(eventTime(o) || Date.now()).toISOString(), now), align: "right", width: "84px" }),
    ],
  },
  // ---- Workloads ----
  {
    key: "pods",
    title: "Pods",
    kind: "Pod",
    gvr: v1("pods"),
    namespaced: true,
    group: "Workloads",
    icon: Box,
    columns: [
      nameCol,
      nsCol,
      col("ready", "Ready", (o) => podStatus(o).ready / Math.max(1, podStatus(o).total), {
        render: (o) => {
          const s = podStatus(o);
          const done = s.reason === "Completed" || s.reason === "Succeeded" || o.status?.phase === "Succeeded";
          return <span className={done ? "muted" : s.ready < s.total ? "text-warn" : ""}>{`${s.ready}/${s.total}`}</span>;
        },
        width: "64px",
      }),
      col("status", "Status", (o) => podStatus(o).reason, { render: (o) => tone(podStatus(o).tone, podStatus(o).reason), width: "minmax(120px, 1fr)" }),
      col("restarts", "Restarts", (o) => podStatus(o).restarts, {
        render: (o) => {
          const r = podStatus(o).restarts;
          return <span className={r > 0 ? "text-warn" : "muted"}>{r}</span>;
        },
        align: "right",
        width: "76px",
      }),
      col("node", "Node", (o) => o.spec?.nodeName ?? "", { width: "minmax(120px, 1fr)", minor: true }),
      col("ip", "IP", (o) => o.status?.podIP ?? "", { mono: true, width: "minmax(100px, 0.7fr)", minor: true }),
      ageCol,
    ],
  },
  {
    key: "deployments",
    title: "Deployments",
    kind: "Deployment",
    gvr: g("apps", "v1", "deployments"),
    namespaced: true,
    group: "Workloads",
    icon: Layers,
    columns: [
      nameCol,
      nsCol,
      col("ready", "Ready", (o) => ready(o.status?.readyReplicas, o.spec?.replicas), { render: (o) => tone(readyTone(o.status?.readyReplicas, o.spec?.replicas), ready(o.status?.readyReplicas, o.spec?.replicas)), width: "90px" }),
      col("updated", "Up-to-date", (o) => o.status?.updatedReplicas ?? 0, { align: "right", width: "90px", minor: true }),
      col("available", "Available", (o) => o.status?.availableReplicas ?? 0, { align: "right", width: "80px" }),
      col("images", "Images", (o) => images(o), { width: "minmax(160px, 1.5fr)", render: (o) => <span className="cell-dim mono">{images(o)}</span>, minor: true }),
      ageCol,
    ],
  },
  {
    key: "statefulsets",
    title: "StatefulSets",
    kind: "StatefulSet",
    gvr: g("apps", "v1", "statefulsets"),
    namespaced: true,
    group: "Workloads",
    icon: Database,
    columns: [
      nameCol,
      nsCol,
      col("ready", "Ready", (o) => ready(o.status?.readyReplicas, o.spec?.replicas), { render: (o) => tone(readyTone(o.status?.readyReplicas, o.spec?.replicas), ready(o.status?.readyReplicas, o.spec?.replicas)), width: "90px" }),
      col("service", "Service", (o) => o.spec?.serviceName ?? "", { width: "minmax(120px, 1fr)", minor: true }),
      col("images", "Images", (o) => images(o), { width: "minmax(160px, 1.5fr)", render: (o) => <span className="cell-dim mono">{images(o)}</span>, minor: true }),
      ageCol,
    ],
  },
  {
    key: "daemonsets",
    title: "DaemonSets",
    kind: "DaemonSet",
    gvr: g("apps", "v1", "daemonsets"),
    namespaced: true,
    group: "Workloads",
    icon: Boxes,
    columns: [
      nameCol,
      nsCol,
      col("desired", "Desired", (o) => o.status?.desiredNumberScheduled ?? 0, { align: "right", width: "72px" }),
      col("ready", "Ready", (o) => o.status?.numberReady ?? 0, { render: (o) => tone(readyTone(o.status?.numberReady, o.status?.desiredNumberScheduled), String(o.status?.numberReady ?? 0)), width: "80px" }),
      col("updated", "Up-to-date", (o) => o.status?.updatedNumberScheduled ?? 0, { align: "right", width: "90px", minor: true }),
      col("available", "Available", (o) => o.status?.numberAvailable ?? 0, { align: "right", width: "80px", minor: true }),
      col("selector", "Node selector", (o) => Object.entries(o.spec?.template?.spec?.nodeSelector ?? {}).map(([k, v]) => `${k}=${v}`).join(", "), { width: "minmax(120px, 1fr)", minor: true }),
      ageCol,
    ],
  },
  {
    key: "replicasets",
    title: "ReplicaSets",
    kind: "ReplicaSet",
    gvr: g("apps", "v1", "replicasets"),
    namespaced: true,
    group: "Workloads",
    icon: Copy,
    columns: [
      nameCol,
      nsCol,
      col("desired", "Desired", (o) => o.spec?.replicas ?? 0, { align: "right", width: "72px" }),
      col("current", "Current", (o) => o.status?.replicas ?? 0, { align: "right", width: "72px" }),
      col("ready", "Ready", (o) => o.status?.readyReplicas ?? 0, { render: (o) => tone(readyTone(o.status?.readyReplicas, o.spec?.replicas), String(o.status?.readyReplicas ?? 0)), width: "72px" }),
      col("owner", "Owner", (o) => o.metadata.ownerReferences?.[0]?.name ?? "", { width: "minmax(120px, 1fr)", minor: true }),
      ageCol,
    ],
  },
  {
    key: "jobs",
    title: "Jobs",
    kind: "Job",
    gvr: g("batch", "v1", "jobs"),
    namespaced: true,
    group: "Workloads",
    icon: ListChecks,
    columns: [
      nameCol,
      nsCol,
      col("status", "Status", (o) => jobStatus(o).text, { render: (o) => tone(jobStatus(o).tone, jobStatus(o).text), width: "110px" }),
      col("completions", "Completions", (o) => `${o.status?.succeeded ?? 0}/${o.spec?.completions ?? 1}`, { width: "100px" }),
      col("duration", "Duration", (o, now) => duration(o, now), { width: "84px", align: "right" }),
      ageCol,
    ],
  },
  {
    key: "cronjobs",
    title: "CronJobs",
    kind: "CronJob",
    gvr: g("batch", "v1", "cronjobs"),
    namespaced: true,
    group: "Workloads",
    icon: CalendarClock,
    columns: [
      nameCol,
      nsCol,
      col("schedule", "Schedule", (o) => o.spec?.schedule ?? "", { mono: true, width: "minmax(110px, 0.8fr)" }),
      col("suspend", "Suspend", (o) => (o.spec?.suspend ? "Yes" : "No"), { render: (o) => (o.spec?.suspend ? tone("warn", "Suspended") : <span className="muted">No</span>), width: "100px" }),
      col("active", "Active", (o) => (o.status?.active ?? []).length, { align: "right", width: "64px" }),
      col("last", "Last schedule", (o) => -(o.status?.lastScheduleTime ? Date.parse(o.status.lastScheduleTime) : 0), { render: (o, now) => (o.status?.lastScheduleTime ? age(o.status.lastScheduleTime, now) : "—"), align: "right", width: "110px" }),
      ageCol,
    ],
  },
  // ---- Network ----
  {
    key: "services",
    title: "Services",
    kind: "Service",
    gvr: v1("services"),
    namespaced: true,
    group: "Network",
    icon: Waypoints,
    columns: [
      nameCol,
      nsCol,
      col("type", "Type", (o) => o.spec?.type ?? "", { width: "110px" }),
      col("clusterIP", "Cluster IP", (o) => o.spec?.clusterIP ?? "", { mono: true, width: "minmax(110px, 0.8fr)" }),
      col("external", "External IP", (o) => externalIP(o), { mono: true, width: "minmax(110px, 0.8fr)", minor: true }),
      col("ports", "Ports", (o) => servicePorts(o), { mono: true, width: "minmax(120px, 1.2fr)" }),
      ageCol,
    ],
  },
  {
    key: "ingresses",
    title: "Ingresses",
    kind: "Ingress",
    gvr: g("networking.k8s.io", "v1", "ingresses"),
    namespaced: true,
    group: "Network",
    icon: Route,
    columns: [
      nameCol,
      nsCol,
      col("class", "Class", (o) => o.spec?.ingressClassName ?? "", { width: "100px" }),
      col("hosts", "Hosts", (o) => (o.spec?.rules ?? []).map((r: any) => r.host ?? "*").join(", "), { width: "minmax(160px, 1.5fr)" }),
      col("address", "Address", (o) => (o.status?.loadBalancer?.ingress ?? []).map((i: any) => i.ip || i.hostname).join(", "), { mono: true, width: "minmax(110px, 1fr)", minor: true }),
      ageCol,
    ],
  },
  {
    key: "endpointslices",
    title: "EndpointSlices",
    kind: "EndpointSlice",
    gvr: g("discovery.k8s.io", "v1", "endpointslices"),
    namespaced: true,
    group: "Network",
    icon: Split,
    columns: [
      nameCol,
      nsCol,
      col("service", "Service", (o) => o.metadata.labels?.["kubernetes.io/service-name"] ?? "", { width: "minmax(120px, 1fr)" }),
      col("endpoints", "Endpoints", (o) => (o.endpoints ?? []).flatMap((e: any) => e.addresses ?? []).slice(0, 4).join(", "), { mono: true, width: "minmax(160px, 1.4fr)" }),
      col("ports", "Ports", (o) => (o.ports ?? []).map((p: any) => p.port).join(", "), { mono: true, width: "100px", minor: true }),
      ageCol,
    ],
  },
  {
    key: "networkpolicies",
    title: "Network Policies",
    kind: "NetworkPolicy",
    gvr: g("networking.k8s.io", "v1", "networkpolicies"),
    namespaced: true,
    group: "Network",
    icon: Network,
    columns: [
      nameCol,
      nsCol,
      col("selector", "Pod selector", (o) => Object.entries(o.spec?.podSelector?.matchLabels ?? {}).map(([k, v]) => `${k}=${v}`).join(", ") || "<all>", { width: "minmax(160px, 1.5fr)" }),
      col("types", "Policy types", (o) => (o.spec?.policyTypes ?? []).join(", "), { width: "120px" }),
      ageCol,
    ],
  },
  // ---- Config ----
  {
    key: "configmaps",
    title: "ConfigMaps",
    kind: "ConfigMap",
    gvr: v1("configmaps"),
    namespaced: true,
    group: "Config",
    icon: FileCode2,
    columns: [nameCol, nsCol, col("data", "Data", (o) => Object.keys(o.data ?? {}).length + Object.keys(o.binaryData ?? {}).length, { align: "right", width: "64px" }), ageCol],
  },
  {
    key: "secrets",
    title: "Secrets",
    kind: "Secret",
    gvr: v1("secrets"),
    namespaced: true,
    group: "Config",
    icon: KeyRound,
    columns: [nameCol, nsCol, col("type", "Type", (o) => o.type ?? "", { width: "minmax(160px, 1.2fr)", render: (o) => <span className="cell-dim">{o.type}</span> }), col("data", "Data", (o) => Object.keys(o.data ?? {}).length, { align: "right", width: "64px" }), ageCol],
  },
  {
    key: "hpas",
    title: "HPAs",
    kind: "HorizontalPodAutoscaler",
    gvr: g("autoscaling", "v2", "horizontalpodautoscalers"),
    namespaced: true,
    group: "Config",
    icon: Gauge,
    columns: [
      nameCol,
      nsCol,
      col("ref", "Reference", (o) => `${o.spec?.scaleTargetRef?.kind}/${o.spec?.scaleTargetRef?.name}`, { width: "minmax(140px, 1.2fr)" }),
      col("minmax", "Min/Max", (o) => `${o.spec?.minReplicas ?? 1}/${o.spec?.maxReplicas}`, { width: "80px" }),
      col("replicas", "Replicas", (o) => o.status?.currentReplicas ?? 0, { align: "right", width: "80px" }),
      ageCol,
    ],
  },
  {
    key: "pdbs",
    title: "Pod Disruption Budgets",
    kind: "PodDisruptionBudget",
    gvr: g("policy", "v1", "poddisruptionbudgets"),
    namespaced: true,
    group: "Config",
    icon: ShieldCheck,
    columns: [
      nameCol,
      nsCol,
      col("min", "Min available", (o) => String(o.spec?.minAvailable ?? "—"), { width: "100px" }),
      col("max", "Max unavailable", (o) => String(o.spec?.maxUnavailable ?? "—"), { width: "120px" }),
      col("allowed", "Allowed disruptions", (o) => o.status?.disruptionsAllowed ?? 0, { align: "right", width: "140px" }),
      ageCol,
    ],
  },
  // ---- Storage ----
  {
    key: "pvcs",
    title: "Persistent Volume Claims",
    kind: "PersistentVolumeClaim",
    gvr: v1("persistentvolumeclaims"),
    namespaced: true,
    group: "Storage",
    icon: HardDrive,
    columns: [
      nameCol,
      nsCol,
      col("status", "Status", (o) => o.status?.phase ?? "", { render: (o) => tone(o.status?.phase === "Bound" ? "ok" : o.status?.phase === "Pending" ? "info" : "bad", o.status?.phase ?? ""), width: "96px" }),
      col("volume", "Volume", (o) => o.spec?.volumeName ?? "", { width: "minmax(140px, 1.2fr)", minor: true }),
      col("capacity", "Capacity", (o) => parseQuantity(o.status?.capacity?.storage), { render: (o) => o.status?.capacity?.storage ?? "—", width: "80px", align: "right" }),
      col("class", "Storage class", (o) => o.spec?.storageClassName ?? "", { width: "120px" }),
      ageCol,
    ],
  },
  {
    key: "pvs",
    title: "Persistent Volumes",
    kind: "PersistentVolume",
    gvr: v1("persistentvolumes"),
    namespaced: false,
    group: "Storage",
    icon: Container,
    columns: [
      nameCol,
      col("capacity", "Capacity", (o) => parseQuantity(o.spec?.capacity?.storage), { render: (o) => o.spec?.capacity?.storage ?? "—", width: "80px", align: "right" }),
      col("access", "Access modes", (o) => (o.spec?.accessModes ?? []).join(", "), { width: "120px", minor: true }),
      col("reclaim", "Reclaim", (o) => o.spec?.persistentVolumeReclaimPolicy ?? "", { width: "84px", minor: true }),
      col("status", "Status", (o) => o.status?.phase ?? "", { render: (o) => tone(o.status?.phase === "Bound" ? "ok" : o.status?.phase === "Available" ? "info" : "warn", o.status?.phase ?? ""), width: "96px" }),
      col("claim", "Claim", (o) => (o.spec?.claimRef ? `${o.spec.claimRef.namespace}/${o.spec.claimRef.name}` : ""), { width: "minmax(140px, 1.2fr)" }),
      col("class", "Storage class", (o) => o.spec?.storageClassName ?? "", { width: "120px" }),
      ageCol,
    ],
  },
  {
    key: "storageclasses",
    title: "Storage Classes",
    kind: "StorageClass",
    gvr: g("storage.k8s.io", "v1", "storageclasses"),
    namespaced: false,
    group: "Storage",
    icon: ServerCog,
    columns: [
      { ...nameCol, render: (o) => <span className="cell-name">{o.metadata.name}{o.metadata.annotations?.["storageclass.kubernetes.io/is-default-class"] === "true" && <Badge tone="info">default</Badge>}</span> },
      col("provisioner", "Provisioner", (o) => o.provisioner ?? "", { width: "minmax(160px, 1.4fr)" }),
      col("reclaim", "Reclaim", (o) => o.reclaimPolicy ?? "", { width: "84px" }),
      col("binding", "Binding mode", (o) => o.volumeBindingMode ?? "", { width: "150px", minor: true }),
      ageCol,
    ],
  },
  // ---- Access control ----
  {
    key: "serviceaccounts",
    title: "Service Accounts",
    kind: "ServiceAccount",
    gvr: v1("serviceaccounts"),
    namespaced: true,
    group: "Access Control",
    icon: UserCog,
    columns: [nameCol, nsCol, ageCol],
  },
  {
    key: "roles",
    title: "Roles",
    kind: "Role",
    gvr: g("rbac.authorization.k8s.io", "v1", "roles"),
    namespaced: true,
    group: "Access Control",
    icon: Shield,
    columns: [nameCol, nsCol, col("rules", "Rules", (o) => (o.rules ?? []).length, { align: "right", width: "64px" }), ageCol],
  },
  {
    key: "rolebindings",
    title: "Role Bindings",
    kind: "RoleBinding",
    gvr: g("rbac.authorization.k8s.io", "v1", "rolebindings"),
    namespaced: true,
    group: "Access Control",
    icon: Users,
    columns: [nameCol, nsCol, col("role", "Role", (o) => `${o.roleRef?.kind}/${o.roleRef?.name}`, { width: "minmax(140px, 1.2fr)" }), col("subjects", "Subjects", (o) => (o.subjects ?? []).map((s: any) => s.name).join(", "), { width: "minmax(140px, 1.2fr)", minor: true }), ageCol],
  },
  {
    key: "clusterroles",
    title: "Cluster Roles",
    kind: "ClusterRole",
    gvr: g("rbac.authorization.k8s.io", "v1", "clusterroles"),
    namespaced: false,
    group: "Access Control",
    icon: FileKey,
    columns: [nameCol, col("rules", "Rules", (o) => (o.rules ?? []).length, { align: "right", width: "64px" }), ageCol],
  },
  {
    key: "clusterrolebindings",
    title: "Cluster Role Bindings",
    kind: "ClusterRoleBinding",
    gvr: g("rbac.authorization.k8s.io", "v1", "clusterrolebindings"),
    namespaced: false,
    group: "Access Control",
    icon: Users,
    columns: [nameCol, col("role", "Role", (o) => `${o.roleRef?.kind}/${o.roleRef?.name}`, { width: "minmax(140px, 1.2fr)" }), col("subjects", "Subjects", (o) => (o.subjects ?? []).map((s: any) => `${s.kind}:${s.name}`).join(", "), { width: "minmax(160px, 1.5fr)", minor: true }), ageCol],
  },
  // ---- Custom resources ----
  {
    key: "crds",
    title: "Definitions",
    kind: "CustomResourceDefinition",
    gvr: g("apiextensions.k8s.io", "v1", "customresourcedefinitions"),
    namespaced: false,
    group: "Custom Resources",
    icon: Puzzle,
    columns: [
      nameCol,
      col("group", "Group", (o) => o.spec?.group ?? "", { width: "minmax(140px, 1.2fr)" }),
      col("kind", "Kind", (o) => o.spec?.names?.kind ?? "", { width: "minmax(110px, 1fr)" }),
      col("version", "Version", (o) => (o.spec?.versions ?? []).filter((v: any) => v.served).map((v: any) => v.name).join(", "), { width: "100px" }),
      col("scope", "Scope", (o) => o.spec?.scope ?? "", { width: "100px" }),
      ageCol,
    ],
  },
];

export const resourceGroups = ["Cluster", "Workloads", "Network", "Config", "Storage", "Access Control", "Custom Resources"];

export const byKey = new Map(resources.map((r) => [r.key, r]));
export const byKind = new Map(resources.map((r) => [r.kind, r]));

function images(o: KObj) {
  return (o.spec?.template?.spec?.containers ?? []).map((c: any) => c.image).join(", ");
}

export function eventTime(o: KObj): number {
  const t = o.lastTimestamp || o.eventTime || o.series?.lastObservedTime || o.firstTimestamp || o.metadata.creationTimestamp;
  return t ? Date.parse(t) : 0;
}

/**
 * customResourceDef builds the list of a custom resource from its
 * definition, with the printer columns the definition declares.
 */
export function customResourceDef(crd: KObj): ResourceDef {
  const versions: any[] = crd.spec?.versions ?? [];
  const version = versions.find((v) => v.storage) ?? versions.find((v) => v.served) ?? versions[0];
  const namespaced = crd.spec?.scope === "Namespaced";
  const printer: Column[] = (version?.additionalPrinterColumns ?? [])
    .filter((c: any) => c.jsonPath !== ".metadata.creationTimestamp" && (c.priority ?? 0) === 0)
    .slice(0, 5)
    .map((c: any) => ({
      id: `p:${c.name}`,
      title: c.name,
      value: (o: KObj) => {
        const v = jsonPathValue(o, c.jsonPath);
        return c.type === "integer" || c.type === "number" ? Number(v ?? 0) : String(v ?? "");
      },
      render: c.type === "date" ? (o: KObj, now: number) => age(String(jsonPathValue(o, c.jsonPath) ?? ""), now) : undefined,
      width: "minmax(90px, 1fr)",
    }));
  return {
    key: `cr:${crd.spec?.group}/${crd.spec?.names?.plural}`,
    title: crd.spec?.names?.kind ?? crd.metadata.name,
    kind: crd.spec?.names?.kind ?? "",
    gvr: { group: crd.spec?.group ?? "", version: version?.name ?? "v1", resource: crd.spec?.names?.plural ?? "" },
    namespaced,
    group: crd.spec?.group ?? "",
    icon: Puzzle,
    columns: [nameCol, ...(namespaced ? [nsCol] : []), ...printer, ageCol],
  };
}

function jsonPathValue(o: KObj, path: string): unknown {
  const p = path.replace(/^\./, "");
  let cur: any = o;
  for (const part of p.match(/[^.[\]]+|\[\d+\]/g) ?? []) {
    if (cur == null) return undefined;
    const idx = /^\[(\d+)\]$/.exec(part);
    cur = idx ? cur[Number(idx[1])] : cur[part];
  }
  if (Array.isArray(cur)) return cur.join(", ");
  if (cur && typeof cur === "object") return JSON.stringify(cur);
  return cur;
}

/** genericDef is the list of any resource the app has no page for. */
export function genericDef(r: { group: string; version: string; resource: string; kind: string; namespaced: boolean }): ResourceDef {
  return {
    key: `gen:${r.group}/${r.resource}`,
    title: r.kind,
    kind: r.kind,
    gvr: { group: r.group, version: r.version, resource: r.resource },
    namespaced: r.namespaced,
    group: r.group,
    icon: Puzzle,
    columns: [nameCol, ...(r.namespaced ? [nsCol] : []), ageCol],
  };
}
