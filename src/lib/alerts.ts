import { create } from "zustand";
import { Alerts, events, type AlertsState, type OpenRequest } from "../mygo";
import { byKind } from "./resources";
import { findDef } from "./discovery";
import { run, toast, useApp } from "./store";

const empty: AlertsState = { targets: [], alerts: [], pausedUntil: "", errors: {}, syncing: [] };

interface AlertsStore {
  state: AlertsState;
  init(): void;
}

let started = false;

/** useAlerts is the watchlist of the Go side, kept up to date by its events. */
export const useAlerts = create<AlertsStore>((set) => ({
  state: empty,
  init() {
    if (started) return;
    started = true;
    events.alertsChanged.on((state) => set({ state }));
    events.openObject.on((req) => openRequest(req));
    Alerts.state().then((state) => set({ state }), () => {});
    // A notification clicked while the window was closed opened it.
    Alerts.takePendingOpen().then((req) => req && openRequest(req), () => {});
  },
}));

export function isWatched(state: AlertsState, cluster: string, namespace: string) {
  return state.targets.some((t) => t.cluster === cluster && t.namespace === namespace);
}

export async function toggleWatch(cluster: string, namespace: string) {
  const watched = isWatched(useAlerts.getState().state, cluster, namespace);
  await run("Could not change the watchlist", () => Alerts.setWatched(cluster, namespace, !watched));
  if (!watched) {
    const name = useApp.getState().clusters.find((c) => c.id === cluster)?.name ?? "the cluster";
    toast.ok(`Watching ${namespace || "all of"} ${namespace ? `in ${name}` : name}`, "You'll get a notification when something breaks, also with the window closed.");
  }
}

/** openRequest shows the object a notification or the menu bar points at. */
export async function openRequest(req: OpenRequest) {
  const app = useApp.getState();
  if (!app.clusters.some((c) => c.id === req.cluster)) return;
  app.activate(req.cluster);
  if (!req.kind) return;
  const def = byKind.get(req.kind) ?? (await findDef(req.cluster, req.kind));
  if (!def) return;
  useApp.getState().openDetail({ gvr: def.gvr, kind: def.kind, namespace: req.namespace, name: req.name }, true);
}
