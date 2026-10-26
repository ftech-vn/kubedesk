// The Kube Desk plugin API, version 1.
//
// A plugin imports it as the "kubedesk" module, which the app gives each
// plugin when it runs it; it is never bundled into the plugin. React is
// shared the same way: import "react", and the app's copy is used.
//
//   import { registerPage, k8s, ui } from "kubedesk";
//
// Everything a register function adds goes away when the plugin is turned
// off or reloaded; each also returns a function that removes it earlier.

declare module "kubedesk" {
  import type { ComponentType, ReactNode } from "react";

  /** The version of this API; it changes only when plugins would break. */
  export const apiVersion: 1;
  /** The version of Kube Desk, such as "0.8.0". */
  export const appVersion: string;
  /** The plugin, as its package.json describes it. */
  export const plugin: { id: string; name: string; version: string };

  export type Disposer = () => void;

  /** A resource of the Kubernetes API: { group: "apps", version: "v1", resource: "deployments" }. */
  export interface GVR {
    group: string;
    version: string;
    resource: string;
  }

  /** A Kubernetes object, as the API returns it. */
  export interface KObj {
    apiVersion?: string;
    kind?: string;
    metadata: {
      name: string;
      namespace?: string;
      uid: string;
      creationTimestamp?: string;
      deletionTimestamp?: string;
      labels?: Record<string, string>;
      annotations?: Record<string, string>;
      ownerReferences?: { apiVersion: string; kind: string; name: string; uid: string; controller?: boolean }[];
      resourceVersion?: string;
      generation?: number;
    };
    spec?: any;
    status?: any;
    data?: Record<string, string>;
    [key: string]: any;
  }

  /** A cluster in Kube Desk. */
  export interface Cluster {
    id: string;
    name: string;
    context: string;
    server: string;
    /** Production clusters ask to type their name before every change. */
    production: boolean;
    /** Read-only clusters refuse every change. */
    readOnly: boolean;
    /** Snapshots are namespaces saved to a file, with no API server. */
    snapshot: boolean;
  }

  /** An object the user is looking at, or acting on. */
  export interface ObjectContext {
    cluster: string;
    gvr: GVR;
    kind: string;
    object: KObj;
  }

  /** Where the user is: the cluster shown, if any, and its namespace ("" for all). */
  export interface AppContext {
    cluster: string | null;
    namespace: string;
  }

  /** Icons are components that take a size, such as those of lucide-react. */
  export type Icon = ComponentType<{ size?: number }>;

  // ---- Extending the app ----

  /** registerPage adds a page, listed in the sidebar of every cluster under Plugins. */
  export function registerPage(page: {
    id: string;
    title: string;
    icon?: Icon;
    component: ComponentType<{ cluster: string; namespace: string }>;
  }): Disposer;

  /** registerDetailsTab adds a tab to the details of objects of some kinds, or of all. */
  export function registerDetailsTab(tab: {
    id: string;
    label: string;
    /** Kinds, such as ["Deployment", "StatefulSet"]; all when left out. */
    kinds?: string[];
    when?(ctx: ObjectContext): boolean;
    component: ComponentType<ObjectContext>;
  }): Disposer;

  /** registerDetailsSection adds a section under the Overview tab of objects' details. */
  export function registerDetailsSection(section: {
    id: string;
    title?: string;
    kinds?: string[];
    when?(ctx: ObjectContext): boolean;
    component: ComponentType<ObjectContext>;
  }): Disposer;

  /** registerObjectAction adds an item to the menu of objects, in lists and details. */
  export function registerObjectAction(action: {
    id: string;
    label: string;
    icon?: Icon;
    kinds?: string[];
    when?(ctx: ObjectContext): boolean;
    danger?: boolean;
    run(ctx: ObjectContext): void | Promise<void>;
  }): Disposer;

  /** registerColumn adds a column to the lists of some kinds, before Age. */
  export function registerColumn(column: {
    id: string;
    title: string;
    kinds: string[];
    /** value is what the column sorts and filters by. */
    value(o: KObj): string | number;
    render?(o: KObj): ReactNode;
    align?: "right";
    width?: number | string;
    mono?: boolean;
  }): Disposer;

  /** registerCommand adds a command to the ⌘K palette. */
  export function registerCommand(command: {
    id: string;
    label: string;
    keywords?: string;
    icon?: Icon;
    when?(ctx: AppContext): boolean;
    run(ctx: AppContext): void | Promise<void>;
  }): Disposer;

  /** registerTitleBarAction adds a component to the right of the title bar, such as a button. */
  export function registerTitleBarAction(action: { id: string; component: ComponentType }): Disposer;

  /** registerOverviewCard adds a card to the overview of every cluster. */
  export function registerOverviewCard(card: {
    id: string;
    title?: string;
    /** wide cards take two columns. */
    wide?: boolean;
    component: ComponentType<{ cluster: string; namespace: string }>;
  }): Disposer;

  /** registerSettings gives the plugin a settings page, in Plugins. */
  export function registerSettings(component: ComponentType<{ settings: Record<string, unknown>; save(next: Record<string, unknown>): Promise<void> }>): Disposer;

  export type AppEvent =
    | { type: "cluster-changed"; cluster: string | null }
    | { type: "namespace-changed"; cluster: string; namespace: string }
    | { type: "page-changed"; cluster: string; page: string }
    | { type: "object-opened"; cluster: string; gvr: GVR; kind: string; namespace: string; name: string };

  /** onEvent is told where the user goes in the app. */
  export function onEvent(listener: (e: AppEvent) => void): Disposer;

  // ---- Clusters ----

  export const k8s: {
    /** useList watches the objects of a resource, as the app's lists do. */
    useList(cluster: string, gvr: GVR, namespace?: string): { items: KObj[]; loading: boolean; error?: string };
    list(cluster: string, gvr: GVR, namespace?: string): Promise<KObj[]>;
    get(cluster: string, gvr: GVR, namespace: string, name: string): Promise<KObj>;
    /** request reads a path of the API with GET, such as "/apis/metrics.k8s.io/v1beta1/nodes", and returns the body. */
    request(cluster: string, path: string): Promise<string>;
    /** resources lists the resources the cluster serves, custom ones too. */
    resources(cluster: string): Promise<{ group: string; version: string; resource: string; kind: string; namespaced: boolean; verbs: string[] }[]>;
    /**
     * apply creates or replaces the objects of a YAML document after showing
     * the user the diff and asking, as editing YAML in the app does. It
     * resolves to false when the user declined. Read-only clusters refuse.
     */
    apply(cluster: string, yaml: string, opts?: { namespace?: string; title?: string }): Promise<boolean>;
    /** remove deletes objects after asking the user, as the app's Delete does. */
    remove(cluster: string, gvr: GVR, kind: string, objects: KObj[]): Promise<boolean>;
    /** can tells whether the user may do a verb, from the cluster's RBAC. */
    can(cluster: string, verb: string, gvr: GVR, namespace?: string): Promise<boolean>;
    age(timestamp?: string): string;
    /** podStatus is the status column of kubectl get pods. */
    podStatus(pod: KObj): { reason: string; tone: Tone; ready: number; total: number; restarts: number };
    parseQuantity(q: string | number | undefined): number;
    formatBytes(n: number): string;
    formatCores(n: number): string;
  };

  // ---- The app ----

  export const app: {
    useContext(): AppContext;
    useCluster(): Cluster | null;
    clusters(): Cluster[];
    /** openObject shows an object's details, at a tab if given. */
    openObject(cluster: string, ref: { gvr: GVR; kind: string; namespace: string; name: string; tab?: string }): void;
    /** openPage shows a page of this plugin, by its id, or of the app ("overview", "pods", "deployments"…). */
    openPage(id: string): void;
    toast: { ok(title: string, detail?: string): void; info(title: string, detail?: string): void; error(title: string, err?: unknown): void };
    confirm(opts: { title: string; message?: string; confirmLabel?: string; danger?: boolean; typeToConfirm?: string }): Promise<boolean>;
    openURL(url: string): Promise<void>;
    copy(text: string): Promise<void>;
  };

  /** The plugin's settings, which the app keeps. */
  export const settings: {
    get(): Record<string, unknown>;
    useSettings(): Record<string, unknown>;
    save(next: Record<string, unknown>): Promise<void>;
  };

  // ---- Components ----

  export type Tone = "ok" | "warn" | "bad" | "info" | "muted";

  export interface Column {
    id: string;
    title: string;
    value(o: KObj, now: number): string | number;
    render?(o: KObj, now: number): ReactNode;
    width?: number | string;
    align?: "right";
    mono?: boolean;
  }

  /** The app's own components, which follow its look and its light and dark themes. */
  export const ui: {
    Button: ComponentType<{ variant?: "default" | "primary" | "ghost" | "danger"; size?: "sm" | "md"; icon?: ReactNode; busy?: boolean; disabled?: boolean; title?: string; onClick?(e: React.MouseEvent): void; children?: ReactNode }>;
    IconButton: ComponentType<{ label: string; onClick?(e: React.MouseEvent): void; children: ReactNode }>;
    Badge: ComponentType<{ tone?: Tone; dot?: boolean; children: ReactNode }>;
    Dot: ComponentType<{ tone: Tone }>;
    Card: ComponentType<{ title?: ReactNode; actions?: ReactNode; className?: string; children: ReactNode }>;
    Empty: ComponentType<{ icon?: ReactNode; title: string; children?: ReactNode }>;
    ErrorBox: ComponentType<{ title?: string; message: string; action?: ReactNode }>;
    Spinner: ComponentType<{ size?: number }>;
    Tabs: ComponentType<{ tabs: { id: string; label: ReactNode }[]; value: string; onChange(v: string): void }>;
    KV: ComponentType<{ items: [ReactNode, ReactNode][] }>;
    Labels: ComponentType<{ labels?: Record<string, string>; max?: number }>;
    Meter: ComponentType<{ value: number; max: number; tone?: Tone }>;
    Field: ComponentType<{ label: string; hint?: ReactNode; children: ReactNode }>;
    Modal: ComponentType<{ title: ReactNode; onClose(): void; footer?: ReactNode; width?: number; children: ReactNode }>;
    Markdown: ComponentType<{ text: string }>;
    /** Page is the frame of a plugin page: a title, actions, and what scrolls under them. */
    Page: ComponentType<{ title: ReactNode; subtitle?: ReactNode; actions?: ReactNode; children: ReactNode }>;
    /** Table is the app's list of objects: sortable, virtualized, opening an object on click. */
    Table: ComponentType<{ items: KObj[]; columns: Column[]; defaultSort?: string; onOpen?(o: KObj): void }>;
  };
}
