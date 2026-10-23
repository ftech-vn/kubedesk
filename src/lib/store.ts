import { create } from "zustand";
import { Clusters, type Cluster, type ClusterStatus, type GVR, type ImpactItem, type ObjectDiff, type RunbookTarget, type SourceError, type Theme } from "../mygo";
import { errorMessage } from "./k8s";

/** DetailRef points at an object shown in the details panel. */
export interface DetailRef {
  gvr: GVR;
  kind: string;
  namespace: string;
  name: string;
  tab?: string;
}

/** View is where the user is in a cluster; each cluster keeps its own. */
export interface View {
  section: string;
  namespace: string;
  details: DetailRef[];
  filter: string;
}

export interface Status extends Partial<ClusterStatus> {
  checking?: boolean;
}

interface AppStore {
  loaded: boolean;
  clusters: Cluster[];
  sourceErrors: SourceError[];
  /** active is the cluster shown, or null for the clusters page. */
  active: string | null;
  views: Record<string, View>;
  savedNamespaces: Record<string, string>;
  status: Record<string, Status>;
  theme: Theme;
  dialog: null | { kind: "add" } | { kind: "settings"; cluster: string } | { kind: "palette" } | { kind: "create"; yaml?: string } | { kind: "report"; cluster: string; object: string; namespace: string; name: string }
    | { kind: "agents" }
    | { kind: "assistant-settings" }
    | { kind: "health"; cluster?: string }
    | { kind: "snapshot"; cluster: string; namespace: string }
    | { kind: "promote"; from: string; gvr: GVR; object: string; namespace: string; name: string }
    | { kind: "runbook"; id?: string; target?: RunbookTarget }
    | { kind: "search"; query?: string }
    | { kind: "compare"; gvr: GVR; object: string; namespace: string; name: string };

  load(): Promise<void>;
  refresh(): Promise<void>;
  activate(id: string | null): void;
  checkStatus(id: string): Promise<void>;
  setSection(section: string): void;
  setNamespace(ns: string): void;
  setFilter(filter: string): void;
  openDetail(ref: DetailRef, replace?: boolean): void;
  setDetailTab(tab: string): void;
  backDetail(): void;
  closeDetail(): void;
  openDialog(d: AppStore["dialog"]): void;
  setTheme(theme: Theme): void;
}

const defaultView = (ns: string): View => ({ section: "overview", namespace: ns, details: [], filter: "" });

export const useApp = create<AppStore>((set, get) => ({
  loaded: false,
  clusters: [],
  sourceErrors: [],
  active: null,
  views: {},
  savedNamespaces: {},
  status: {},
  theme: "system",
  dialog: null,

  async load() {
    const [list, state] = await Promise.all([Clusters.list(), Clusters.state()]);
    set({ clusters: list.clusters, sourceErrors: list.errors, savedNamespaces: state.namespaces, theme: state.theme, loaded: true });
    const visible = list.clusters.filter((c) => !c.hidden);
    const initial = visible.find((c) => c.id === state.active) ?? null;
    if (initial) get().activate(initial.id);
    for (const c of visible) get().checkStatus(c.id);
  },

  async refresh() {
    const list = await Clusters.list();
    set({ clusters: list.clusters, sourceErrors: list.errors });
  },

  activate(id) {
    const s = get();
    if (id && !s.views[id]) {
      const cl = s.clusters.find((c) => c.id === id);
      const ns = s.savedNamespaces[id] ?? cl?.namespace ?? "";
      set({ views: { ...s.views, [id]: defaultView(ns) } });
    }
    set({ active: id });
    Clusters.setActive(id ?? "").catch(() => {});
    if (id && !s.status[id]?.ok) get().checkStatus(id);
  },

  async checkStatus(id) {
    set((s) => ({ status: { ...s.status, [id]: { ...s.status[id], checking: true } } }));
    try {
      const st = await Clusters.test(id);
      set((s) => ({ status: { ...s.status, [id]: { ...st, checking: false } } }));
    } catch (e) {
      set((s) => ({ status: { ...s.status, [id]: { ok: false, error: errorMessage(e), checking: false } } }));
    }
  },

  setSection(section) {
    updateView(set, get, (v) => ({ ...v, section, details: [], filter: "" }));
  },
  setNamespace(namespace) {
    const id = get().active;
    if (!id) return;
    updateView(set, get, (v) => ({ ...v, namespace }));
    set((s) => ({ savedNamespaces: { ...s.savedNamespaces, [id]: namespace } }));
    Clusters.setNamespace(id, namespace).catch(() => {});
  },
  setFilter(filter) {
    updateView(set, get, (v) => ({ ...v, filter }));
  },
  openDetail(ref, replace) {
    updateView(set, get, (v) => ({ ...v, details: replace ? [ref] : [...v.details, ref] }));
  },
  setDetailTab(tab) {
    updateView(set, get, (v) => {
      const details = [...v.details];
      const top = details.pop();
      return top ? { ...v, details: [...details, { ...top, tab }] } : v;
    });
  },
  backDetail() {
    updateView(set, get, (v) => ({ ...v, details: v.details.slice(0, -1) }));
  },
  closeDetail() {
    updateView(set, get, (v) => ({ ...v, details: [] }));
  },
  openDialog(dialog) {
    set({ dialog });
  },
  setTheme(theme) {
    set({ theme });
    // Go switches the appearance of the windows, which the page follows
    // through prefers-color-scheme.
    Clusters.setTheme(theme).catch((e) => toast.error("Could not change the theme", e));
  },
}));

function updateView(set: (p: Partial<AppStore>) => void, get: () => AppStore, f: (v: View) => View) {
  const s = get();
  const id = s.active;
  if (!id) return;
  const v = s.views[id] ?? defaultView("");
  set({ views: { ...s.views, [id]: f(v) } });
}

export function useActiveView(): View | null {
  return useApp((s) => (s.active ? (s.views[s.active] ?? null) : null));
}

export function useActiveCluster(): Cluster | null {
  return useApp((s) => s.clusters.find((c) => c.id === s.active) ?? null);
}

// ---- Toasts ----

export interface Toast {
  id: number;
  tone: "ok" | "bad" | "info";
  title: string;
  detail?: string;
}

interface ToastStore {
  toasts: Toast[];
  push(t: Omit<Toast, "id">): void;
  dismiss(id: number): void;
}

let toastSeq = 0;
export const useToasts = create<ToastStore>((set, get) => ({
  toasts: [],
  push(t) {
    const id = ++toastSeq;
    set({ toasts: [...get().toasts, { ...t, id }] });
    setTimeout(() => get().dismiss(id), t.tone === "bad" ? 9000 : 4000);
  },
  dismiss(id) {
    set({ toasts: get().toasts.filter((t) => t.id !== id) });
  },
}));

export const toast = {
  ok: (title: string, detail?: string) => useToasts.getState().push({ tone: "ok", title, detail }),
  info: (title: string, detail?: string) => useToasts.getState().push({ tone: "info", title, detail }),
  error: (title: string, err?: unknown) => useToasts.getState().push({ tone: "bad", title, detail: err === undefined ? undefined : errorMessage(err) }),
};

/** run awaits an action and reports how it went. */
export async function run<T>(what: string, action: () => Promise<T>, done?: string): Promise<T | undefined> {
  try {
    const r = await action();
    if (done) toast.ok(done);
    return r;
  } catch (e) {
    toast.error(what, e);
    return undefined;
  }
}

// ---- Confirmations and prompts ----

export interface AskRequest {
  title: string;
  message?: string;
  confirmLabel?: string;
  danger?: boolean;
  input?: { label: string; value: string; type?: "text" | "number"; placeholder?: string };
  checkbox?: string;
  /** typeToConfirm is a name the user must type before confirming. */
  typeToConfirm?: string;
  /** diffs show what applying a document changes, object by object. */
  diffs?: ObjectDiff[];
  /** impact previews what the change does, given the input's value. */
  impact?: (value: string) => Promise<ImpactItem[]>;
  resolve(r: { ok: boolean; value: string; checked: boolean }): void;
}

export const useAsk = create<{ request: AskRequest | null }>(() => ({ request: null }));

export function ask(req: Omit<AskRequest, "resolve">): Promise<{ ok: boolean; value: string; checked: boolean }> {
  return new Promise((resolve) => {
    useAsk.setState({
      request: {
        ...req,
        resolve: (r) => {
          useAsk.setState({ request: null });
          resolve(r);
        },
      },
    });
  });
}

export async function confirmAction(title: string, message: string, confirmLabel = "Delete", danger = true) {
  return (await ask({ title, message, confirmLabel, danger })).ok;
}
