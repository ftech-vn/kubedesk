import { create } from "zustand";
import { Kube, type AccessCheck, type GVR } from "../mygo";

// What the user may do in each cluster, as the cluster's RBAC answers it.
// Buttons the user may not use are disabled; an unknown answer enables
// them, and the cluster has the last word anyway.

const TTL = 110_000;

interface Answer {
  allowed: boolean;
  at: number;
}

interface AccessStore {
  answers: Record<string, Answer>;
}

export const useAccess = create<AccessStore>(() => ({ answers: {} }));

const keyOf = (cluster: string, c: AccessCheck) => `${cluster}|${c.verb}|${c.group}|${c.resource}|${c.subresource ?? ""}|${c.namespace ?? ""}`;

const queue = new Map<string, { cluster: string; check: AccessCheck }>();
const inflight = new Set<string>();
let timer: ReturnType<typeof setTimeout> | null = null;

function flush() {
  timer = null;
  const byCluster = new Map<string, { key: string; check: AccessCheck }[]>();
  for (const [key, q] of queue) {
    const list = byCluster.get(q.cluster) ?? [];
    list.push({ key, check: q.check });
    byCluster.set(q.cluster, list);
  }
  queue.clear();
  for (const [cluster, list] of byCluster) {
    Kube.can(
      cluster,
      list.map((x) => x.check),
    )
      .then((res) => {
        const at = Date.now();
        useAccess.setState((s) => {
          const answers = { ...s.answers };
          list.forEach((x, i) => (answers[x.key] = { allowed: res[i] ?? true, at }));
          return { answers };
        });
      })
      .catch(() => {
        /* unknown stays allowed */
      })
      .finally(() => list.forEach((x) => inflight.delete(x.key)));
  }
}

function request(cluster: string, check: AccessCheck, key: string) {
  if (inflight.has(key)) return;
  inflight.add(key);
  queue.set(key, { cluster, check });
  if (!timer) timer = setTimeout(flush, 15);
}

/**
 * can answers an access check from what is known: false only when the
 * cluster said no. It asks the cluster when it does not know yet, and the
 * components that use useAccessUpdates render again with the answer.
 */
export function can(cluster: string, check: AccessCheck): boolean {
  const key = keyOf(cluster, check);
  const a = useAccess.getState().answers[key];
  if (!a || Date.now() - a.at > TTL) request(cluster, check, key);
  if (a) return a.allowed;
  // Allowed in every namespace means allowed in this one.
  if (check.namespace) {
    const all = useAccess.getState().answers[keyOf(cluster, { ...check, namespace: "" })];
    if (all?.allowed) return true;
  }
  return true;
}

export const canDo = (cluster: string, verb: string, gvr: GVR, namespace = "", subresource?: string) =>
  can(cluster, { verb, group: gvr.group, resource: gvr.resource, subresource, namespace });

/** useAccessUpdates renders the component again when answers arrive. */
export function useAccessUpdates() {
  useAccess((s) => s.answers);
}

export const noAccess = "Your access to this cluster does not allow this";
