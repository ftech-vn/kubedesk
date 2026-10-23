import { useEffect, useMemo, useState } from "react";
import { AlertTriangle, Box, Camera, Layers, LayoutGrid, Server } from "lucide-react";
import { Kube, type ClusterInfo } from "../mygo";
import { age, errorMessage, formatBytes, formatCores, nodeStatus, parseQuantity, podRequests, podStatus, type KObj } from "../lib/k8s";
import { byKey, eventTime } from "../lib/resources";
import { useActiveCluster, useActiveView, useApp } from "../lib/store";
import { useNow, usePoll, useWatch } from "../lib/watch";
import { Badge, Button, Card, ErrorBox, Spinner } from "../components/ui";
import { ChangeList, useChanges } from "./ChangesView";
import { useAlerts } from "../lib/alerts";
import { useRunbooksFor } from "../lib/runbooks";
import { RunbooksFor } from "../components/Runbooks";
import { ResourceBar } from "./details/Overview";

const def = (k: string) => byKey.get(k)!;

export function ClusterOverview({ cluster }: { cluster: string }) {
  const cl = useActiveCluster();
  const view = useActiveView()!;
  const { setSection, openDetail } = useApp();
  const now = useNow(10000);
  const ns = view.namespace;
  const nodes = useWatch(cluster, def("nodes").gvr);
  const pods = useWatch(cluster, def("pods").gvr, ns);
  const deployments = useWatch(cluster, def("deployments").gvr, ns);
  const namespaces = useWatch(cluster, def("namespaces").gvr);
  const events = useWatch(cluster, def("events").gvr, ns);
  const nodeMetrics = usePoll(cluster, { group: "metrics.k8s.io", version: "v1beta1", resource: "nodes" }, "", 20000);
  const [info, setInfo] = useState<ClusterInfo | null>(null);
  const [infoError, setInfoError] = useState("");

  useEffect(() => {
    Kube.info(cluster).then(setInfo, (e) => setInfoError(errorMessage(e)));
  }, [cluster]);

  if (infoError && nodes.error && !nodes.items.length) {
    return (
      <div className="page">
        <ErrorBox title={`Cannot connect to ${cl?.name}`} message={infoError} />
      </div>
    );
  }

  const readyNodes = nodes.items.filter((n) => nodeStatus(n).ready).length;
  const podTones = pods.items.reduce<Record<string, number>>((acc, p) => {
    const t = podStatus(p).tone;
    acc[t] = (acc[t] ?? 0) + 1;
    return acc;
  }, {});
  const healthyDeploys = deployments.items.filter((d) => (d.status?.readyReplicas ?? 0) >= (d.spec?.replicas ?? 0)).length;

  const alloc = nodes.items.reduce((a, n) => ({ cpu: a.cpu + parseQuantity(n.status?.allocatable?.cpu), memory: a.memory + parseQuantity(n.status?.allocatable?.memory), pods: a.pods + parseQuantity(n.status?.allocatable?.pods) }), { cpu: 0, memory: 0, pods: 0 });
  const active = pods.items.filter((p) => p.status?.phase !== "Succeeded" && p.status?.phase !== "Failed");
  const requests = active.reduce((a, p) => {
    const r = podRequests(p);
    return { cpu: a.cpu + r.cpu, memory: a.memory + r.memory };
  }, { cpu: 0, memory: 0 });
  const usage = nodeMetrics.items.reduce((a, m) => ({ cpu: a.cpu + parseQuantity(m.usage?.cpu), memory: a.memory + parseQuantity(m.usage?.memory) }), { cpu: 0, memory: 0 });
  const hasMetrics = nodeMetrics.items.length > 0;
  const warnings = events.items.filter((e) => e.type === "Warning").sort((a, b) => eventTime(b) - eventTime(a)).slice(0, 30);
  const failing = pods.items.filter((p) => podStatus(p).tone === "bad").slice(0, 12);

  return (
    <div className="page overview-page">
      <header className="page-header">
        <div>
          <h1>{cl?.name}</h1>
          <div className="muted small">
            {info ? `Kubernetes ${info.version}` : <Spinner size={12} />} · <span className="mono">{info?.server ?? cl?.server}</span>
            {ns && <> · namespace <b>{ns}</b></>}
          </div>
        </div>
        {cl && cl.sourceKind !== "snapshot" && (
          <Button icon={<Camera size={14} />} onClick={() => useApp.getState().openDialog({ kind: "snapshot", cluster, namespace: ns })} title="Save a namespace to a file others open without access to the cluster">
            Snapshot
          </Button>
        )}
      </header>

      <div className="stat-grid">
        <Stat icon={<Server size={18} />} label="Nodes" value={nodes.loading ? "…" : `${readyNodes}/${nodes.items.length}`} sub="ready" tone={readyNodes < nodes.items.length ? "warn" : "ok"} onClick={() => setSection("nodes")} />
        <Stat icon={<Box size={18} />} label="Pods" value={pods.loading ? "…" : String(pods.items.length)} sub={`${podTones.ok ?? 0} running · ${podTones.bad ?? 0} failing`} tone={(podTones.bad ?? 0) > 0 ? "bad" : "ok"} onClick={() => setSection("pods")} />
        <Stat icon={<Layers size={18} />} label="Deployments" value={deployments.loading ? "…" : `${healthyDeploys}/${deployments.items.length}`} sub="available" tone={healthyDeploys < deployments.items.length ? "warn" : "ok"} onClick={() => setSection("deployments")} />
        <Stat icon={<LayoutGrid size={18} />} label="Namespaces" value={namespaces.loading ? "…" : String(namespaces.items.length)} sub="in the cluster" onClick={() => setSection("namespaces")} />
        <Stat icon={<AlertTriangle size={18} />} label="Warnings" value={events.loading ? "…" : String(warnings.length)} sub="recent events" tone={warnings.length ? "warn" : "ok"} onClick={() => setSection("events")} />
      </div>

      <OverviewRunbooks cluster={cluster} namespace={ns} />

      <div className="overview-grid">
        <Card title="Capacity">
          <div className="resource-bars">
            <ResourceBar label="CPU requested" value={requests.cpu} max={alloc.cpu} fmt={formatCores} />
            <ResourceBar label="Memory requested" value={requests.memory} max={alloc.memory} fmt={formatBytes} />
            {hasMetrics && <ResourceBar label="CPU used" value={usage.cpu} max={alloc.cpu} fmt={formatCores} />}
            {hasMetrics && <ResourceBar label="Memory used" value={usage.memory} max={alloc.memory} fmt={formatBytes} />}
            <ResourceBar label="Pods scheduled" value={active.length} max={alloc.pods} fmt={(n) => String(Math.round(n))} />
          </div>
          <div className="faint small" style={{ marginTop: 10 }}>
            {ns ? `Requests of namespace ${ns} against the whole cluster. ` : ""}
            {!hasMetrics && !nodeMetrics.loading && "Usage needs metrics-server."}
          </div>
        </Card>

        <Card title="Nodes">
          <div className="mini-table">
            {nodes.items.map((n) => {
              const st = nodeStatus(n);
              const m = nodeMetrics.items.find((x) => x.metadata.name === n.metadata.name);
              const cpuPct = m ? (parseQuantity(m.usage?.cpu) / parseQuantity(n.status?.allocatable?.cpu)) * 100 : null;
              const memPct = m ? (parseQuantity(m.usage?.memory) / parseQuantity(n.status?.allocatable?.memory)) * 100 : null;
              return (
                <button key={n.metadata.uid} className="mini-row clickable" style={{ gridTemplateColumns: "1.6fr 1fr 70px 70px" }} onClick={() => openDetail({ gvr: def("nodes").gvr, kind: "Node", namespace: "", name: n.metadata.name }, true)}>
                  <span className="cell-name">{n.metadata.name}</span>
                  <span>
                    <Badge tone={st.tone} dot>
                      {st.label}
                    </Badge>
                  </span>
                  <span className="right muted small">{cpuPct !== null ? `CPU ${cpuPct.toFixed(0)}%` : formatCores(parseQuantity(n.status?.capacity?.cpu)) + " CPU"}</span>
                  <span className="right muted small">{memPct !== null ? `Mem ${memPct.toFixed(0)}%` : formatBytes(parseQuantity(n.status?.capacity?.memory))}</span>
                </button>
              );
            })}
          </div>
        </Card>

        {failing.length > 0 && (
          <Card title={`Unhealthy pods (${pods.items.filter((p) => podStatus(p).tone === "bad").length})`}>
            <div className="mini-table">
              {failing.map((p: KObj) => {
                const s = podStatus(p);
                return (
                  <button key={p.metadata.uid} className="mini-row clickable" style={{ gridTemplateColumns: "minmax(0, 2fr) minmax(0, 1fr) auto 44px" }} onClick={() => openDetail({ gvr: def("pods").gvr, kind: "Pod", namespace: p.metadata.namespace ?? "", name: p.metadata.name }, true)}>
                    <span className="cell-name">{p.metadata.name}</span>
                    <span className="muted">{p.metadata.namespace}</span>
                    <span>
                      <Badge tone={s.tone} dot>
                        {s.reason}
                      </Badge>
                    </span>
                    <span className="right muted">{s.restarts}↻</span>
                  </button>
                );
              })}
            </div>
          </Card>
        )}

        <RecentChanges cluster={cluster} namespace={ns} />

        <Card title="Warning events" className="span-2">
          {warnings.length === 0 ? (
            <div className="muted small">{events.loading ? "Loading…" : "No warnings. All quiet."}</div>
          ) : (
            <div className="event-list compact">
              {warnings.map((e) => (
                <button key={e.metadata.uid} className="event warning clickable" onClick={() => openDetail({ gvr: def("events").gvr, kind: "Event", namespace: e.metadata.namespace ?? "", name: e.metadata.name }, true)}>
                  <div className="event-head">
                    <Badge tone="warn">{e.reason}</Badge>
                    <span className="mono small">{`${e.involvedObject?.kind?.toLowerCase()}/${e.involvedObject?.name}`}</span>
                    <span className="faint small">
                      {e.metadata.namespace} · {age(new Date(eventTime(e)).toISOString(), now)} ago{(e.count ?? 1) > 1 ? ` · ×${e.count}` : ""}
                    </span>
                  </div>
                  <div className="event-message">{e.message}</div>
                </button>
              ))}
            </div>
          )}
        </Card>
      </div>
    </div>
  );
}

function RecentChanges({ cluster, namespace }: { cluster: string; namespace: string }) {
  const watched = useAlerts((s) => s.state.targets.some((t) => t.cluster === cluster));
  const setSection = useApp((s) => s.setSection);
  const since = useMemo(() => new Date(Date.now() - 24 * 3600e3).toISOString().replace(/\.\d+Z$/, "Z"), []);
  const { changes } = useChanges(watched ? { cluster, namespace, since, limit: 40 } : null);
  if (!watched) return null;
  const edits = (changes ?? []).filter((c) => c.type !== "status").slice(0, 8);
  return (
    <Card
      title="Recent changes"
      className="span-2"
      actions={
        <Button size="sm" variant="ghost" onClick={() => setSection("changes")}>
          All changes
        </Button>
      }
    >
      {!changes ? <Spinner size={14} /> : edits.length === 0 ? <div className="muted small">No changes in the last 24 hours.</div> : <ChangeList cluster={cluster} changes={edits} showNamespace={!namespace} compact />}
    </Card>
  );
}

function Stat({ icon, label, value, sub, tone, onClick }: { icon: React.ReactNode; label: string; value: string; sub: string; tone?: "ok" | "warn" | "bad"; onClick(): void }) {
  return (
    <button className={`stat ${tone ? `stat-${tone}` : ""}`} onClick={onClick}>
      <div className="stat-top">
        <span className="stat-icon">{icon}</span>
        <span className="stat-label">{label}</span>
      </div>
      <div className="stat-value">{value}</div>
      <div className="stat-sub">{sub}</div>
    </button>
  );
}

/** OverviewRunbooks shows the runbooks of the namespace, or of the cluster. */
function OverviewRunbooks({ cluster, namespace }: { cluster: string; namespace: string }) {
  const list = useRunbooksFor(cluster, namespace, "", "");
  if (!list.length) return null;
  return (
    <div className="overview-runbooks">
      <RunbooksFor list={list} ctx={{ cluster, namespace }} />
    </div>
  );
}
