import { useEffect, useState } from "react";
import { create } from "zustand";
import { Kube, Runbooks, type Runbook } from "../mygo";
import { deleteObjects, restart, scale } from "./actions";
import { findDef } from "./discovery";
import type { KObj } from "./k8s";
import { toast, useApp } from "./store";
import { useAssistant } from "./assistant";

/** useRunbookVersion changes whenever a runbook is saved or deleted. */
export const useRunbookVersion = create<{ v: number; bump(): void }>((set) => ({ v: 0, bump: () => set((s) => ({ v: s.v + 1 })) }));

/** useRunbooksFor are the runbooks of an object, a namespace or a cluster. */
export function useRunbooksFor(cluster: string, namespace: string, kind: string, name: string): Runbook[] {
  const v = useRunbookVersion((s) => s.v);
  const [list, setList] = useState<Runbook[]>([]);
  useEffect(() => {
    let alive = true;
    Runbooks.for(cluster, namespace, kind, name).then(
      (l) => alive && setList(l ?? []),
      () => alive && setList([]),
    );
    return () => {
      alive = false;
    };
  }, [cluster, namespace, kind, name, v]);
  return list;
}

/** Where a runbook is read: the object it is shown with, if any. */
export interface RunbookContext {
  cluster: string;
  namespace: string;
  kind?: string;
  name?: string;
}

/** The actions runbooks can link to, as kubedesk:<action>?kind=…&name=… */
export const runbookActions = [
  { action: "logs", label: "Logs", hint: "open the logs" },
  { action: "restart", label: "Restart", hint: "restart the workload, after you confirm" },
  { action: "scale", label: "Scale", hint: "scale the workload, after you confirm" },
  { action: "report", label: "Incident report", hint: "gather an incident report" },
  { action: "events", label: "Events", hint: "open the events" },
  { action: "history", label: "History", hint: "open the recent changes" },
  { action: "map", label: "Map", hint: "open the map of the app" },
  { action: "yaml", label: "YAML", hint: "open the YAML" },
  { action: "open", label: "Open", hint: "open another object: add kind=, name= and namespace=" },
  { action: "delete", label: "Delete", hint: "delete an object, such as a stuck pod, after you confirm" },
  { action: "ask", label: "Ask the assistant", hint: "ask the assistant: add q=your question" },
];

const tabs: Record<string, string> = { logs: "logs", events: "events", history: "history", map: "map", yaml: "yaml", open: "overview" };

/** runAction runs a kubedesk: link of a runbook, on the object it names or the one the runbook is read with. */
export async function runAction(href: string, ctx: RunbookContext) {
  let u: URL;
  try {
    u = new URL(href);
  } catch {
    return toast.error("This runbook link is not valid", href);
  }
  const action = u.pathname.replace(/^\/+/, "");
  const q = u.searchParams;
  const app = useApp.getState();
  if (action === "ask") {
    const text = q.get("q") ?? "";
    useAssistant.getState().toggle(true);
    if (text) useAssistant.getState().send(text);
    return;
  }
  if (app.active !== ctx.cluster) app.activate(ctx.cluster);
  const kind = q.get("kind") ?? ctx.kind;
  const name = q.get("name") ?? ctx.name;
  const namespace = q.get("namespace") ?? ctx.namespace;
  if (!kind || !name) return toast.error(`“${action}” needs an object`, "Add kind= and name= to the link, or read the runbook from the object's details.");
  const def = await findDef(ctx.cluster, kind);
  if (!def) return toast.error(`Kube Desk does not know the kind ${kind}`);
  const ns = def.namespaced ? namespace : "";
  if (action in tabs) {
    useApp.getState().openDetail({ gvr: def.gvr, kind: def.kind, namespace: ns, name, tab: q.get("tab") ?? tabs[action] }, false);
    return;
  }
  if (action === "report") {
    useApp.getState().openDialog({ kind: "report", cluster: ctx.cluster, object: def.kind, namespace: ns, name });
    return;
  }
  let o: KObj;
  try {
    o = (await Kube.get(ctx.cluster, def.gvr, ns, name)) as KObj;
  } catch (e) {
    return toast.error(`${def.kind} ${name} is not there`, e);
  }
  if (action === "restart") return restart(ctx.cluster, def.gvr, o);
  if (action === "scale") return scale(ctx.cluster, def.gvr, o);
  if (action === "delete") return void deleteObjects(ctx.cluster, def.gvr, def.kind, [o]);
  toast.error(`Runbooks have no action “${action}”`, runbookActions.map((a) => a.action).join(", "));
}
