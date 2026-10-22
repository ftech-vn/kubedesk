import { useState, type ReactNode } from "react";
import { ArrowRightLeft, ScrollText, SquareTerminal } from "lucide-react";
import { age, formatBytes, formatCores, matchesSelector, nodeRoles, nodeStatus, parseQuantity, podRequests, podStatus, selectorOf, type KObj } from "../../lib/k8s";
import { portForward } from "../../lib/actions";
import { eventTime } from "../../lib/resources";
import { useApp } from "../../lib/store";
import { useNow, usePoll, useWatch } from "../../lib/watch";
import { Badge, Button, Card, KV, Meter } from "../../components/ui";
import { Conditions, DataEntries, MetaCard, ObjLink, PodMiniTable, Selector, podsGVR } from "./parts";

type Row = [ReactNode, ReactNode];

export function Overview({ cluster, kind, o }: { cluster: string; kind: string; o: KObj }) {
  return (
    <div className="overview">
      <KindOverview cluster={cluster} kind={kind} o={o} />
      <MetaCard cluster={cluster} o={o} />
      {kind !== "Node" && <Conditions conditions={o.status?.conditions} />}
    </div>
  );
}

function KindOverview({ cluster, kind, o }: { cluster: string; kind: string; o: KObj }) {
  switch (kind) {
    case "Pod":
      return <PodOverview cluster={cluster} o={o} />;
    case "Deployment":
    case "StatefulSet":
    case "DaemonSet":
    case "ReplicaSet":
      return <WorkloadOverview cluster={cluster} kind={kind} o={o} />;
    case "Job":
      return <JobOverview cluster={cluster} o={o} />;
    case "CronJob":
      return <CronJobOverview cluster={cluster} o={o} />;
    case "Node":
      return <NodeOverview cluster={cluster} o={o} />;
    case "Service":
      return <ServiceOverview cluster={cluster} o={o} />;
    case "Ingress":
      return <IngressOverview cluster={cluster} o={o} />;
    case "ConfigMap":
      return (
        <Card title="Data">
          <DataEntries data={{ ...(o.data ?? {}), ...Object.fromEntries(Object.keys(o.binaryData ?? {}).map((k) => [k, "<binary>"])) }} />
        </Card>
      );
    case "Secret":
      return (
        <Card title={`Data · ${o.type}`}>
          <DataEntries data={o.data ?? {}} secret />
        </Card>
      );
    case "PersistentVolumeClaim":
      return (
        <Card title="Claim">
          <KV
            items={[
              ["Status", <Badge tone={o.status?.phase === "Bound" ? "ok" : "warn"}>{o.status?.phase}</Badge>],
              ["Capacity", o.status?.capacity?.storage ?? o.spec?.resources?.requests?.storage],
              ["Access modes", (o.spec?.accessModes ?? []).join(", ")],
              ["Storage class", o.spec?.storageClassName],
              ["Volume", o.spec?.volumeName ? <ObjLink cluster={cluster} kind="PersistentVolume" name={o.spec.volumeName} /> : ""],
              ["Volume mode", o.spec?.volumeMode],
            ]}
          />
        </Card>
      );
    case "Namespace":
      return <NamespaceOverview cluster={cluster} o={o} />;
    case "Event":
      return <EventOverview cluster={cluster} o={o} />;
    case "HorizontalPodAutoscaler":
      return (
        <Card title="Autoscaling">
          <KV
            items={[
              ["Target", <ObjLink cluster={cluster} kind={o.spec?.scaleTargetRef?.kind} apiVersion={o.spec?.scaleTargetRef?.apiVersion} name={o.spec?.scaleTargetRef?.name} namespace={o.metadata.namespace} />],
              ["Replicas", `${o.status?.currentReplicas ?? 0} current · ${o.status?.desiredReplicas ?? 0} desired · ${o.spec?.minReplicas ?? 1}–${o.spec?.maxReplicas} allowed`],
              [
                "Metrics",
                <div className="stack">
                  {(o.spec?.metrics ?? []).map((m: any, i: number) => {
                    const cur = o.status?.currentMetrics?.[i];
                    const name = m.resource?.name ?? m.pods?.metric?.name ?? m.external?.metric?.name ?? m.type;
                    const target = m.resource?.target?.averageUtilization ? `${m.resource.target.averageUtilization}%` : (m.resource?.target?.averageValue ?? m.pods?.target?.averageValue ?? "");
                    const current = cur?.resource?.current?.averageUtilization !== undefined ? `${cur.resource.current.averageUtilization}%` : (cur?.resource?.current?.averageValue ?? "?");
                    return <span key={i}>{`${name}: ${current} / ${target}`}</span>;
                  })}
                </div>,
              ],
            ]}
          />
        </Card>
      );
    default:
      return null;
  }
}

function PodOverview({ cluster, o }: { cluster: string; o: KObj }) {
  const s = podStatus(o);
  const openDetail = useApp((st) => st.openDetail);
  const setTab = (tab: string, container: string) =>
    openDetail({ gvr: podsGVR, kind: "Pod", namespace: o.metadata.namespace ?? "", name: o.metadata.name, tab: `${tab}:${container}` }, false);
  const statuses = new Map<string, any>([...(o.status?.initContainerStatuses ?? []), ...(o.status?.containerStatuses ?? [])].map((c: any) => [c.name, c]));
  const containers = [...(o.spec?.initContainers ?? []).map((c: any) => ({ ...c, init: true })), ...(o.spec?.containers ?? [])];
  return (
    <>
      <Card title="Pod">
        <KV
          items={[
            ["Status", <Badge tone={s.tone} dot>{s.reason}</Badge>],
            ["Ready", `${s.ready}/${s.total} containers · ${s.restarts} restarts`],
            ["Node", o.spec?.nodeName ? <ObjLink cluster={cluster} kind="Node" name={o.spec.nodeName} /> : <span className="muted">not scheduled</span>],
            ["Pod IP", <span className="mono selectable">{(o.status?.podIPs ?? []).map((p: any) => p.ip).join(", ") || o.status?.podIP}</span>],
            ["Host IP", <span className="mono">{o.status?.hostIP}</span>],
            ["QoS class", o.status?.qosClass],
            ["Service account", o.spec?.serviceAccountName ? <ObjLink cluster={cluster} kind="ServiceAccount" name={o.spec.serviceAccountName} namespace={o.metadata.namespace} /> : ""],
            ["Priority class", o.spec?.priorityClassName],
            ...(o.status?.message ? ([["Message", <span className="text-warn">{o.status.message}</span>]] as Row[]) : []),
          ]}
        />
      </Card>
      {containers.map((c: any) => {
        const st = statuses.get(c.name);
        const state = st?.state ?? {};
        const stateLabel = state.running ? "Running" : state.waiting ? state.waiting.reason ?? "Waiting" : state.terminated ? state.terminated.reason ?? "Terminated" : "Unknown";
        const tone = state.running ? (st?.ready ? "ok" : "warn") : state.terminated?.exitCode === 0 ? "muted" : state.waiting?.reason === "ContainerCreating" ? "info" : "bad";
        return (
          <Card
            key={c.name}
            title={
              <span className="container-title">
                {c.init && <Badge tone="muted">init</Badge>}
                {c.name}
                <Badge tone={tone} dot>
                  {stateLabel}
                </Badge>
              </span>
            }
            actions={
              <>
                <Button size="sm" variant="ghost" icon={<ScrollText size={14} />} onClick={() => setTab("logs", c.name)}>
                  Logs
                </Button>
                {state.running && !c.init && (
                  <Button size="sm" variant="ghost" icon={<SquareTerminal size={14} />} onClick={() => setTab("terminal", c.name)}>
                    Shell
                  </Button>
                )}
              </>
            }
          >
            <KV
              items={[
                ["Image", <span className="mono selectable">{c.image}</span>],
                ["Restarts", st?.restartCount ?? 0],
                ...(state.waiting?.message ? ([["Waiting", <span className="text-warn">{state.waiting.message}</span>]] as Row[]) : []),
                ...(st?.lastState?.terminated
                  ? ([["Last exit", `${st.lastState.terminated.reason ?? ""} (code ${st.lastState.terminated.exitCode}) ${st.lastState.terminated.finishedAt ? `· ${age(st.lastState.terminated.finishedAt)} ago` : ""}`]] as Row[])
                  : []),
                ...(c.command || c.args ? ([["Command", <LongText text={[...(c.command ?? []), ...(c.args ?? [])].join(" ")} />]] as Row[]) : []),
                [
                  "Ports",
                  (c.ports ?? []).length ? (
                    <div className="ports">
                      {(c.ports ?? []).map((p: any) => (
                        <span key={`${p.containerPort}/${p.protocol}`} className="port">
                          <span className="mono">
                            {p.name ? `${p.name}: ` : ""}
                            {p.containerPort}/{p.protocol ?? "TCP"}
                          </span>
                          {state.running && (p.protocol ?? "TCP") === "TCP" && (
                            <button className="link small" onClick={() => portForward(cluster, o.metadata.namespace ?? "", o.metadata.name, p.containerPort)}>
                              <ArrowRightLeft size={12} /> Forward
                            </button>
                          )}
                        </span>
                      ))}
                    </div>
                  ) : (
                    ""
                  ),
                ],
                ["Requests", resourcesText(c.resources?.requests)],
                ["Limits", resourcesText(c.resources?.limits)],
                ...((c.volumeMounts ?? []).length
                  ? ([
                      [
                        "Mounts",
                        <div className="stack small">
                          {c.volumeMounts.map((m: any) => (
                            <span key={m.mountPath} className="mono">
                              {m.mountPath} <span className="muted">← {m.name}{m.readOnly ? " (ro)" : ""}</span>
                            </span>
                          ))}
                        </div>,
                      ],
                    ] as Row[])
                  : []),
                ...((c.env ?? []).length ? ([["Env", `${c.env.length} variables`]] as Row[]) : []),
              ]}
            />
          </Card>
        );
      })}
    </>
  );
}

/** LongText shows the first lines of a long text, and all of it on demand. */
function LongText({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  const long = text.length > 160 || text.includes("\n");
  return (
    <span className="stack">
      <span className={`mono small selectable ${long && !open ? "clamp" : ""}`} style={{ whiteSpace: "pre-wrap" }}>
        {text}
      </span>
      {long && (
        <button className="link small" onClick={() => setOpen(!open)}>
          {open ? "Show less" : "Show all"}
        </button>
      )}
    </span>
  );
}

function resourcesText(r?: Record<string, string>) {
  if (!r || !Object.keys(r).length) return "";
  return Object.entries(r)
    .map(([k, v]) => `${k}: ${v}`)
    .join(" · ");
}

function WorkloadOverview({ cluster, kind, o }: { cluster: string; kind: string; o: KObj }) {
  const ns = o.metadata.namespace ?? "";
  const { items } = useWatch(cluster, podsGVR, ns);
  const selector = selectorOf(o);
  const pods = items.filter((p) => matchesSelector(p.metadata.labels, selector) && (kind !== "ReplicaSet" || p.metadata.ownerReferences?.some((r) => r.uid === o.metadata.uid)));
  const st = o.status ?? {};
  const rows: Row[] =
    kind === "DaemonSet"
      ? [
          ["Scheduled", `${st.currentNumberScheduled ?? 0} of ${st.desiredNumberScheduled ?? 0} nodes`],
          ["Ready", `${st.numberReady ?? 0} ready · ${st.numberAvailable ?? 0} available · ${st.updatedNumberScheduled ?? 0} up to date`],
          ["Update strategy", o.spec?.updateStrategy?.type],
        ]
      : [
          ["Replicas", `${st.readyReplicas ?? 0} ready · ${st.availableReplicas ?? 0} available · ${st.updatedReplicas ?? 0} up to date · ${o.spec?.replicas ?? 0} desired`],
          ["Strategy", o.spec?.strategy?.type ?? o.spec?.updateStrategy?.type ?? ""],
        ];
  return (
    <>
      <Card title={kind}>
        <KV
          items={[
            ...rows,
            ["Selector", <Selector selector={selector} />],
            [
              "Images",
              <div className="stack">
                {(o.spec?.template?.spec?.containers ?? []).map((c: any) => (
                  <span key={c.name} className="mono small selectable">
                    <span className="muted">{c.name}: </span>
                    {c.image}
                  </span>
                ))}
              </div>,
            ],
          ]}
        />
      </Card>
      <PodMiniTable pods={pods} />
    </>
  );
}

function JobOverview({ cluster, o }: { cluster: string; o: KObj }) {
  const { items } = useWatch(cluster, podsGVR, o.metadata.namespace ?? "");
  const pods = items.filter((p) => p.metadata.labels?.["job-name"] === o.metadata.name || p.metadata.ownerReferences?.some((r) => r.uid === o.metadata.uid));
  return (
    <>
      <Card title="Job">
        <KV
          items={[
            ["Completions", `${o.status?.succeeded ?? 0} of ${o.spec?.completions ?? 1} succeeded · ${o.status?.failed ?? 0} failed · ${o.status?.active ?? 0} active`],
            ["Parallelism", o.spec?.parallelism],
            ["Backoff limit", o.spec?.backoffLimit],
            ["Started", o.status?.startTime ? new Date(o.status.startTime).toLocaleString() : ""],
            ["Completed", o.status?.completionTime ? new Date(o.status.completionTime).toLocaleString() : ""],
          ]}
        />
      </Card>
      <PodMiniTable pods={pods} />
    </>
  );
}

function CronJobOverview({ cluster, o }: { cluster: string; o: KObj }) {
  const now = useNow(10000);
  const open = useApp((s) => s.openDetail);
  const { items } = useWatch(cluster, { group: "batch", version: "v1", resource: "jobs" }, o.metadata.namespace ?? "");
  const jobs = items.filter((j) => j.metadata.ownerReferences?.some((r) => r.uid === o.metadata.uid)).sort((a, b) => (b.metadata.creationTimestamp ?? "").localeCompare(a.metadata.creationTimestamp ?? ""));
  return (
    <>
      <Card title="CronJob">
        <KV
          items={[
            ["Schedule", <span className="mono">{o.spec?.schedule}</span>],
            ["Time zone", o.spec?.timeZone ?? "controller's local time"],
            ["Suspended", o.spec?.suspend ? <Badge tone="warn">Yes</Badge> : "No"],
            ["Last schedule", o.status?.lastScheduleTime ? `${age(o.status.lastScheduleTime, now)} ago` : "never"],
            ["Last success", o.status?.lastSuccessfulTime ? `${age(o.status.lastSuccessfulTime, now)} ago` : ""],
            ["Concurrency", o.spec?.concurrencyPolicy],
          ]}
        />
      </Card>
      <Card title={`Jobs (${jobs.length})`}>
        {jobs.length === 0 ? (
          <div className="muted small">None</div>
        ) : (
          <div className="mini-table">
            {jobs.map((j) => {
              const done = j.status?.succeeded >= (j.spec?.completions ?? 1);
              const failed = (j.status?.conditions ?? []).some((c: any) => c.type === "Failed" && c.status === "True");
              return (
                <button
                  key={j.metadata.uid}
                  className="mini-row clickable"
                  style={{ gridTemplateColumns: "2fr 110px 70px" }}
                  onClick={() => open({ gvr: { group: "batch", version: "v1", resource: "jobs" }, kind: "Job", namespace: j.metadata.namespace ?? "", name: j.metadata.name })}
                >
                  <span className="cell-name">{j.metadata.name}</span>
                  <span>
                    <Badge tone={failed ? "bad" : done ? "ok" : "info"} dot>
                      {failed ? "Failed" : done ? "Complete" : "Running"}
                    </Badge>
                  </span>
                  <span className="right muted">{age(j.metadata.creationTimestamp, now)}</span>
                </button>
              );
            })}
          </div>
        )}
      </Card>
    </>
  );
}

function NodeOverview({ cluster, o }: { cluster: string; o: KObj }) {
  const { items } = useWatch(cluster, podsGVR, "");
  const metrics = usePoll(cluster, { group: "metrics.k8s.io", version: "v1beta1", resource: "nodes" }, "", 20000);
  const pods = items.filter((p) => p.spec?.nodeName === o.metadata.name && p.status?.phase !== "Succeeded" && p.status?.phase !== "Failed");
  const usage = metrics.items.find((m) => m.metadata.name === o.metadata.name)?.usage;
  const st = nodeStatus(o);
  const alloc = o.status?.allocatable ?? {};
  const cap = o.status?.capacity ?? {};
  const req = pods.reduce((acc, p) => {
    const r = podRequests(p);
    return { cpu: acc.cpu + r.cpu, memory: acc.memory + r.memory };
  }, { cpu: 0, memory: 0 });
  const cpuAlloc = parseQuantity(alloc.cpu);
  const memAlloc = parseQuantity(alloc.memory);
  const info = o.status?.nodeInfo ?? {};
  return (
    <>
      <Card title="Node">
        <KV
          items={[
            ["Status", <Badge tone={st.tone} dot>{st.label}</Badge>],
            ["Roles", nodeRoles(o)],
            ["Addresses", <div className="stack mono small">{(o.status?.addresses ?? []).map((a: any) => <span key={a.type + a.address}><span className="muted">{a.type}: </span>{a.address}</span>)}</div>],
            ["Kubelet", info.kubeletVersion],
            ["OS", `${info.osImage ?? ""} · ${info.operatingSystem ?? ""}/${info.architecture ?? ""}`],
            ["Kernel", info.kernelVersion],
            ["Runtime", info.containerRuntimeVersion],
            ["Pod CIDR", o.spec?.podCIDR],
            ["Taints", (o.spec?.taints ?? []).length ? <div className="stack mono small">{o.spec.taints.map((t: any, i: number) => <span key={i}>{`${t.key}${t.value ? `=${t.value}` : ""}:${t.effect}`}</span>)}</div> : ""],
          ]}
        />
      </Card>
      <Card title="Resources">
        <div className="resource-bars">
          <ResourceBar label="CPU requests" value={req.cpu} max={cpuAlloc} fmt={formatCores} />
          <ResourceBar label="Memory requests" value={req.memory} max={memAlloc} fmt={formatBytes} />
          {usage && <ResourceBar label="CPU usage" value={parseQuantity(usage.cpu)} max={cpuAlloc} fmt={formatCores} />}
          {usage && <ResourceBar label="Memory usage" value={parseQuantity(usage.memory)} max={memAlloc} fmt={formatBytes} />}
          <ResourceBar label="Pods" value={pods.length} max={parseQuantity(alloc.pods)} fmt={(n) => String(Math.round(n))} />
        </div>
        <div className="faint small" style={{ marginTop: 8 }}>
          Capacity {formatCores(parseQuantity(cap.cpu))} CPU · {formatBytes(parseQuantity(cap.memory))} memory · {cap["ephemeral-storage"] ? formatBytes(parseQuantity(cap["ephemeral-storage"])) : "?"} storage
          {!usage && !metrics.loading && " · install metrics-server for usage"}
        </div>
      </Card>
      <Conditions conditions={o.status?.conditions} />
      <PodMiniTable pods={pods} showNode={false} showNamespace />
    </>
  );
}

export function ResourceBar({ label, value, max, fmt }: { label: string; value: number; max: number; fmt(n: number): string }) {
  return (
    <div className="resource-bar">
      <div className="resource-bar-label">
        <span>{label}</span>
        <span className="muted">
          {fmt(value)} / {fmt(max)} <span className="faint">({max ? Math.round((value / max) * 100) : 0}%)</span>
        </span>
      </div>
      <Meter value={value} max={max} />
    </div>
  );
}

function ServiceOverview({ cluster, o }: { cluster: string; o: KObj }) {
  const ns = o.metadata.namespace ?? "";
  const { items } = useWatch(cluster, podsGVR, ns);
  const selector = o.spec?.selector as Record<string, string> | undefined;
  const pods = items.filter((p) => matchesSelector(p.metadata.labels, selector));
  const forward = (port: any) => {
    const pod = pods.find((p) => podStatus(p).reason === "Running");
    if (!pod) return;
    let target = port.targetPort ?? port.port;
    if (typeof target === "string") {
      for (const c of pod.spec?.containers ?? []) for (const p of c.ports ?? []) if (p.name === target) target = p.containerPort;
    }
    portForward(cluster, ns, pod.metadata.name, Number(target));
  };
  const hasRunning = pods.some((p) => podStatus(p).reason === "Running");
  return (
    <>
      <Card title="Service">
        <KV
          items={[
            ["Type", o.spec?.type],
            ["Cluster IP", <span className="mono selectable">{(o.spec?.clusterIPs ?? [o.spec?.clusterIP]).join(", ")}</span>],
            ["External", <span className="mono">{[...(o.status?.loadBalancer?.ingress ?? []).map((i: any) => i.ip || i.hostname), ...(o.spec?.externalIPs ?? []), o.spec?.externalName].filter(Boolean).join(", ")}</span>],
            ["DNS", <span className="mono small selectable">{`${o.metadata.name}.${ns}.svc.cluster.local`}</span>],
            ["Session affinity", o.spec?.sessionAffinity],
            ["Selector", <Selector selector={selector} />],
          ]}
        />
      </Card>
      <Card title="Ports">
        <div className="mini-table">
          <div className="mini-row mini-head" style={{ gridTemplateColumns: "1fr 80px 100px 90px 80px 90px" }}>
            <span>Name</span>
            <span>Port</span>
            <span>Target</span>
            <span>Node port</span>
            <span>Protocol</span>
            <span />
          </div>
          {(o.spec?.ports ?? []).map((p: any, i: number) => (
            <div key={i} className="mini-row" style={{ gridTemplateColumns: "1fr 80px 100px 90px 80px 90px" }}>
              <span>{p.name ?? "—"}</span>
              <span className="mono">{p.port}</span>
              <span className="mono">{p.targetPort}</span>
              <span className="mono">{p.nodePort ?? "—"}</span>
              <span>{p.protocol ?? "TCP"}</span>
              <span>
                {hasRunning && (p.protocol ?? "TCP") === "TCP" && (
                  <button className="link small" onClick={() => forward(p)}>
                    <ArrowRightLeft size={12} /> Forward
                  </button>
                )}
              </span>
            </div>
          ))}
        </div>
      </Card>
      {selector && <PodMiniTable pods={pods} title="Endpoints" />}
    </>
  );
}

function IngressOverview({ cluster, o }: { cluster: string; o: KObj }) {
  const ns = o.metadata.namespace;
  const rows: { host: string; path: string; service: string; port: string }[] = [];
  for (const r of o.spec?.rules ?? []) {
    for (const p of r.http?.paths ?? []) {
      rows.push({ host: r.host ?? "*", path: p.path ?? "/", service: p.backend?.service?.name ?? p.backend?.resource?.name ?? "", port: String(p.backend?.service?.port?.number ?? p.backend?.service?.port?.name ?? "") });
    }
  }
  return (
    <>
      <Card title="Ingress">
        <KV
          items={[
            ["Class", o.spec?.ingressClassName],
            ["Address", <span className="mono">{(o.status?.loadBalancer?.ingress ?? []).map((i: any) => i.ip || i.hostname).join(", ")}</span>],
            ["TLS", (o.spec?.tls ?? []).map((t: any) => `${(t.hosts ?? []).join(", ")} → ${t.secretName}`).join("; ")],
            ["Default backend", o.spec?.defaultBackend?.service?.name],
          ]}
        />
      </Card>
      <Card title="Rules">
        <div className="mini-table">
          <div className="mini-row mini-head" style={{ gridTemplateColumns: "1.5fr 1fr 1.2fr 70px" }}>
            <span>Host</span>
            <span>Path</span>
            <span>Service</span>
            <span>Port</span>
          </div>
          {rows.map((r, i) => {
            const tls = (o.spec?.tls ?? []).some((t: any) => (t.hosts ?? []).includes(r.host));
            const url = r.host !== "*" ? `${tls ? "https" : "http"}://${r.host}${r.path}` : "";
            return (
              <div key={i} className="mini-row" style={{ gridTemplateColumns: "1.5fr 1fr 1.2fr 70px" }}>
                <span>{url ? <a className="link" href={url} target="_blank" rel="noreferrer">{r.host}</a> : r.host}</span>
                <span className="mono">{r.path}</span>
                <span>{r.service && <ObjLink cluster={cluster} kind="Service" name={r.service} namespace={ns} />}</span>
                <span className="mono">{r.port}</span>
              </div>
            );
          })}
        </div>
      </Card>
    </>
  );
}

function NamespaceOverview({ cluster, o }: { cluster: string; o: KObj }) {
  const { items } = useWatch(cluster, podsGVR, o.metadata.name);
  const setNamespace = useApp((s) => s.setNamespace);
  const setSection = useApp((s) => s.setSection);
  const counts = items.reduce<Record<string, number>>((acc, p) => {
    const r = podStatus(p).tone;
    acc[r] = (acc[r] ?? 0) + 1;
    return acc;
  }, {});
  return (
    <Card
      title="Namespace"
      actions={
        <Button
          size="sm"
          onClick={() => {
            setNamespace(o.metadata.name);
            setSection("pods");
          }}
        >
          Show its pods
        </Button>
      }
    >
      <KV
        items={[
          ["Status", <Badge tone={o.status?.phase === "Active" ? "ok" : "warn"}>{o.status?.phase}</Badge>],
          ["Pods", `${items.length} · ${counts.ok ?? 0} healthy · ${counts.bad ?? 0} failing · ${(counts.info ?? 0) + (counts.warn ?? 0)} starting`],
        ]}
      />
    </Card>
  );
}

function EventOverview({ cluster, o }: { cluster: string; o: KObj }) {
  const io = o.involvedObject ?? o.regarding ?? {};
  return (
    <Card title="Event">
      <KV
        items={[
          ["Type", <Badge tone={o.type === "Warning" ? "warn" : "muted"}>{o.type}</Badge>],
          ["Reason", o.reason],
          ["Message", <span className="selectable">{o.message ?? o.note}</span>],
          ["Object", io.name ? <ObjLink cluster={cluster} kind={io.kind} apiVersion={io.apiVersion} name={io.name} namespace={io.namespace}>{`${io.kind}/${io.name}`}</ObjLink> : ""],
          ["Source", [o.source?.component, o.source?.host, o.reportingComponent].filter(Boolean).join(" · ")],
          ["Count", o.count ?? o.series?.count ?? 1],
          ["Last seen", new Date(eventTime(o)).toLocaleString()],
        ]}
      />
    </Card>
  );
}

