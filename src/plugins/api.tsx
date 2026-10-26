import { useSyncExternalStore, type ReactNode } from "react";
import { create } from "zustand";
import { Kube, Plugins, type Cluster as AppCluster, type GVR } from "../mygo";
import { age, formatBytes, formatCores, parseQuantity, podStatus, type KObj } from "../lib/k8s";
import { useWatch } from "../lib/watch";
import { ask, toast, useApp } from "../lib/store";
import { deleteObjects, readOnly, typeToConfirm } from "../lib/actions";
import { Badge, Button, Card, Dot, Empty, ErrorBox, Field, IconButton, KV, Labels, Meter, Modal, Spinner, Tabs } from "../components/ui";
import { Markdown } from "../components/Markdown";
import { ResourceTable } from "../components/ResourceTable";
import type { Column } from "../lib/resources";
import { add, guard, pluginPagePrefix, reportError, usePlugins, type AppContext, type Contributions, type ContributionKind } from "./registry";

// The "kubedesk" module each plugin gets: a small facade over the app, the
// same for every version of plugin API 1, whatever the app's insides become.

export const API_VERSION = 1;

export interface PluginMeta {
  id: string;
  name: string;
  version: string;
}

type Listener = (e: AppEvent) => void;

type AppEvent =
  | { type: "cluster-changed"; cluster: string | null }
  | { type: "namespace-changed"; cluster: string; namespace: string }
  | { type: "page-changed"; cluster: string; page: string }
  | { type: "object-opened"; cluster: string; gvr: GVR; kind: string; namespace: string; name: string };

const listeners = new Set<{ plugin: string; fn: Listener }>();

// Where the user goes, told to the plugins that listen.
useApp.subscribe((s, prev) => {
  if (!listeners.size) return;
  const events: AppEvent[] = [];
  if (s.active !== prev.active) events.push({ type: "cluster-changed", cluster: s.active });
  const id = s.active;
  const v = id ? s.views[id] : undefined;
  const pv = id ? prev.views[id] : undefined;
  if (id && v && pv && s.active === prev.active) {
    if (v.namespace !== pv.namespace) events.push({ type: "namespace-changed", cluster: id, namespace: v.namespace });
    if (v.section !== pv.section) events.push({ type: "page-changed", cluster: id, page: v.section });
    const top = v.details[v.details.length - 1];
    const ptop = pv.details[pv.details.length - 1];
    if (top && (top.kind !== ptop?.kind || top.name !== ptop?.name || top.namespace !== ptop?.namespace)) {
      events.push({ type: "object-opened", cluster: id, gvr: top.gvr, kind: top.kind, namespace: top.namespace, name: top.name });
    }
  }
  for (const e of events) for (const l of listeners) guard(l.plugin, () => l.fn(e), undefined);
});

/** toCluster is what plugins see of a cluster. */
function toCluster(c: AppCluster) {
  return { id: c.id, name: c.name, context: c.context, server: c.server, production: c.production, readOnly: c.readOnly, snapshot: c.sourceKind === "snapshot" };
}

function appContext(): AppContext {
  const s = useApp.getState();
  return { cluster: s.active, namespace: s.active ? (s.views[s.active]?.namespace ?? "") : "" };
}

// Settings of the plugins, as the app keeps them, by plugin ID.
const useSettingsStore = create<Record<string, Record<string, unknown>>>(() => ({}));

export async function loadSettings(plugin: string) {
  const s = await Plugins.settings(plugin).catch(() => ({}));
  useSettingsStore.setState({ [plugin]: s });
}

/** Page is the frame of a plugin's page. */
function Page({ title, subtitle, actions, children }: { title: ReactNode; subtitle?: ReactNode; actions?: ReactNode; children: ReactNode }) {
  return (
    <div className="page plugin-page">
      <header className="page-header">
        <div>
          <h1>{title}</h1>
          {subtitle && <div className="muted small">{subtitle}</div>}
        </div>
        {actions && <div className="page-actions">{actions}</div>}
      </header>
      {children}
    </div>
  );
}

function Table({ items, columns, defaultSort, onOpen }: { items: KObj[]; columns: Column[]; defaultSort?: string; onOpen?(o: KObj): void }) {
  return <ResourceTable items={items} columns={columns} defaultSort={defaultSort} onOpen={onOpen ?? (() => {})} />;
}

const ui = { Button, IconButton, Badge, Dot, Card, Empty, ErrorBox, Spinner, Tabs, KV, Labels, Meter, Field, Modal, Markdown, Page, Table };

/**
 * createApi makes the module of one plugin. What it registers is marked as
 * the plugin's, and dispose removes all of it.
 */
export function createApi(meta: PluginMeta, appVersion: string) {
  const disposers: (() => void)[] = [];
  const plugin = meta.id;

  function register<K extends ContributionKind>(kind: K, what: string, c: Omit<Contributions[K][number], "key" | "plugin">) {
    if (!c || typeof c.id !== "string" || !c.id) throw new Error(`${what} needs an id`);
    const off = add(kind, { ...c, plugin, key: `${plugin}/${c.id}` } as Contributions[K][number]);
    disposers.push(off);
    return off;
  }

  /** wrap runs a plugin's callback, putting what it throws or rejects down to the plugin. */
  const wrap =
    <A extends unknown[]>(f: (...a: A) => unknown) =>
    async (...a: A) => {
      try {
        await f(...a);
      } catch (e) {
        reportError(plugin, e);
        toast.error(`${meta.name} failed`, e);
      }
    };

  const api = {
    apiVersion: API_VERSION,
    appVersion,
    plugin: { ...meta },

    registerPage: (p: Contributions["pages"][number]) => register("pages", "registerPage", p),
    registerDetailsTab: (t: Contributions["detailsTabs"][number]) => register("detailsTabs", "registerDetailsTab", t),
    registerDetailsSection: (s: Contributions["detailsSections"][number]) => register("detailsSections", "registerDetailsSection", s),
    registerObjectAction: (a: Contributions["actions"][number]) => register("actions", "registerObjectAction", { ...a, run: wrap(a.run) }),
    registerColumn: (c: Contributions["columns"][number]) => register("columns", "registerColumn", c),
    registerCommand: (c: Contributions["commands"][number]) => register("commands", "registerCommand", { ...c, run: wrap(c.run) }),
    registerTitleBarAction: (a: Contributions["titleBar"][number]) => register("titleBar", "registerTitleBarAction", a),
    registerOverviewCard: (c: Contributions["overviewCards"][number]) => register("overviewCards", "registerOverviewCard", c),
    registerSettings: (component: Contributions["settings"][number]["component"]) => register("settings", "registerSettings", { id: "settings", component }),
    onEvent(fn: Listener) {
      const l = { plugin, fn };
      listeners.add(l);
      const off = () => listeners.delete(l);
      disposers.push(off);
      return off;
    },

    k8s: {
      useList: (cluster: string, gvr: GVR, namespace = "") => useWatch(cluster, gvr, namespace),
      list: (cluster: string, gvr: GVR, namespace = "") => Kube.list(cluster, gvr, namespace) as Promise<KObj[]>,
      get: (cluster: string, gvr: GVR, namespace: string, name: string) => Kube.get(cluster, gvr, namespace, name) as Promise<KObj>,
      request: (cluster: string, path: string) => Kube.request(cluster, path),
      resources: (cluster: string) => Kube.resources(cluster),
      async apply(cluster: string, yaml: string, opts: { namespace?: string; title?: string } = {}) {
        if (readOnly(cluster)) return false;
        const namespace = opts.namespace ?? appContext().namespace;
        const diffs = await Kube.planApply(cluster, yaml, namespace);
        if (diffs.every((d) => d.action === "unchanged")) {
          toast.info("Nothing to change", "The cluster already has these objects as they are.");
          return false;
        }
        const r = await ask({
          title: opts.title ?? `${meta.name}: apply to the cluster?`,
          message: `${meta.name} asks to make these changes.`,
          confirmLabel: "Apply",
          diffs,
          typeToConfirm: typeToConfirm(cluster),
        });
        if (!r.ok) return false;
        const res = await Kube.apply(cluster, yaml, namespace);
        toast.ok("Applied", res.join("\n"));
        return true;
      },
      remove: (cluster: string, gvr: GVR, kind: string, objects: KObj[]) => deleteObjects(cluster, gvr, kind, objects),
      async can(cluster: string, verb: string, gvr: GVR, namespace = "") {
        const [ok] = await Kube.can(cluster, [{ verb, group: gvr.group, resource: gvr.resource, namespace }]);
        return ok ?? true;
      },
      age: (ts?: string) => age(ts),
      podStatus,
      parseQuantity,
      formatBytes,
      formatCores,
    },

    app: {
      useContext(): AppContext {
        const cluster = useApp((s) => s.active);
        const namespace = useApp((s) => (s.active ? (s.views[s.active]?.namespace ?? "") : ""));
        return { cluster, namespace };
      },
      useCluster() {
        const c = useApp((s) => s.clusters.find((x) => x.id === s.active));
        return c ? toCluster(c) : null;
      },
      clusters: () => useApp.getState().clusters.filter((c) => !c.hidden).map(toCluster),
      openObject(cluster: string, ref: { gvr: GVR; kind: string; namespace: string; name: string; tab?: string }) {
        const s = useApp.getState();
        if (s.active !== cluster) s.activate(cluster);
        useApp.getState().openDetail({ gvr: ref.gvr, kind: ref.kind, namespace: ref.namespace, name: ref.name, tab: ref.tab }, true);
      },
      openPage(id: string) {
        const s = useApp.getState();
        if (!s.active) return;
        // A plugin's own pages, by their id; the app's pages, by theirs.
        const own = usePlugins.getState().pages.some((p) => p.key === `${plugin}/${id}`);
        s.setSection(own ? `${pluginPagePrefix}${plugin}/${id}` : id);
      },
      toast: { ok: toast.ok, info: toast.info, error: toast.error },
      async confirm(opts: { title: string; message?: string; confirmLabel?: string; danger?: boolean; typeToConfirm?: string }) {
        return (await ask({ title: opts.title, message: opts.message, confirmLabel: opts.confirmLabel ?? "OK", danger: opts.danger, typeToConfirm: opts.typeToConfirm })).ok;
      },
      openURL: (url: string) => Kube.openURL(url),
      copy: (text: string) => navigator.clipboard.writeText(text),
    },

    settings: {
      get: () => useSettingsStore.getState()[plugin] ?? {},
      useSettings: () =>
        useSyncExternalStore(
          useSettingsStore.subscribe,
          () => useSettingsStore.getState()[plugin] ?? emptySettings,
        ),
      async save(next: Record<string, unknown>) {
        await Plugins.saveSettings(plugin, next);
        useSettingsStore.setState({ [plugin]: next });
      },
    },

    ui,
  };

  return {
    api,
    dispose() {
      for (const d of disposers.splice(0)) d();
    },
  };
}

const emptySettings: Record<string, unknown> = {};
