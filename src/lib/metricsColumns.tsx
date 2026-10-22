import { useMemo } from "react";
import { Meter } from "../components/ui";
import { formatBytes, formatCores, parseQuantity, type KObj } from "./k8s";
import type { Column, ResourceDef } from "./resources";
import { usePoll } from "./watch";

const podMetrics = { group: "metrics.k8s.io", version: "v1beta1", resource: "pods" };
const nodeMetrics = { group: "metrics.k8s.io", version: "v1beta1", resource: "nodes" };

interface Usage {
  cpu: number;
  memory: number;
}

const key = (ns: string | undefined, name: string) => `${ns ?? ""}/${name}`;

/** limitsOf sums what a pod's containers may use: limits, else requests. */
function limitsOf(p: KObj): Usage {
  let cpu = 0;
  let memory = 0;
  for (const c of p.spec?.containers ?? []) {
    cpu += parseQuantity(c.resources?.limits?.cpu ?? c.resources?.requests?.cpu);
    memory += parseQuantity(c.resources?.limits?.memory ?? c.resources?.requests?.memory);
  }
  return { cpu, memory };
}

function UsageCell({ text, value, max, of }: { text: string; value: number; max: number; of: string }) {
  return (
    <span className="usage-cell" title={max ? `${Math.round((value / max) * 100)}% of ${of}` : undefined}>
      <span>{text}</span>
      {max > 0 && <Meter value={value} max={max} />}
    </span>
  );
}

/**
 * useMetricsColumns adds CPU and memory usage to the lists of pods and nodes,
 * from metrics-server, polled every 15 seconds. Clusters without it get no
 * columns.
 */
export function useMetricsColumns(cluster: string, def: ResourceDef, namespace: string): Column[] {
  const isPod = def.kind === "Pod";
  const isNode = def.kind === "Node";
  const gvr = isPod ? podMetrics : isNode ? nodeMetrics : null;
  const { items } = usePoll(cluster, gvr, isPod ? namespace : "", 15000);

  return useMemo(() => {
    if (!gvr || items.length === 0) return [];
    const usage = new Map<string, Usage>();
    for (const m of items) {
      if (isPod) {
        let cpu = 0;
        let memory = 0;
        for (const c of (m.containers as any[]) ?? []) {
          cpu += parseQuantity(c.usage?.cpu);
          memory += parseQuantity(c.usage?.memory);
        }
        usage.set(key(m.metadata.namespace, m.metadata.name), { cpu, memory });
      } else {
        usage.set(key("", m.metadata.name), { cpu: parseQuantity(m.usage?.cpu), memory: parseQuantity(m.usage?.memory) });
      }
    }
    const of = (o: KObj) => usage.get(key(isPod ? o.metadata.namespace : "", o.metadata.name));
    const cap = (o: KObj): Usage =>
      isPod ? limitsOf(o) : { cpu: parseQuantity(o.status?.allocatable?.cpu), memory: parseQuantity(o.status?.allocatable?.memory) };
    const capLabel = isPod ? "its limits (or requests)" : "allocatable";
    const cpu: Column = {
      id: "usage-cpu",
      title: isNode ? "CPU used" : "CPU",
      value: (o) => of(o)?.cpu ?? -1,
      render: (o) => {
        const u = of(o);
        if (!u) return <span className="faint">—</span>;
        return <UsageCell text={isNode ? `${Math.round((u.cpu / Math.max(cap(o).cpu, 1e-9)) * 100)}%` : formatCores(u.cpu)} value={u.cpu} max={cap(o).cpu} of={capLabel} />;
      },
      width: "92px",
    };
    const memory: Column = {
      id: "usage-memory",
      title: isNode ? "Mem used" : "Memory",
      value: (o) => of(o)?.memory ?? -1,
      render: (o) => {
        const u = of(o);
        if (!u) return <span className="faint">—</span>;
        return <UsageCell text={isNode ? `${Math.round((u.memory / Math.max(cap(o).memory, 1)) * 100)}%` : formatBytes(u.memory)} value={u.memory} max={cap(o).memory} of={capLabel} />;
      },
      width: "96px",
    };
    return [cpu, memory];
  }, [gvr, items, isPod, isNode]);
}
