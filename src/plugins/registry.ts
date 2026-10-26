import { create } from "zustand";
import type { ComponentType, ReactNode } from "react";
import type { GVR } from "../mygo";
import type { KObj } from "../lib/k8s";

// What plugins added to the app. Each contribution carries the plugin's ID,
// so that turning a plugin off or reloading it removes all it added, and an
// error in it is put down to it.

export type Icon = ComponentType<{ size?: number }>;

export interface ObjectContext {
  cluster: string;
  gvr: GVR;
  kind: string;
  object: KObj;
}

export interface AppContext {
  cluster: string | null;
  namespace: string;
}

interface Base {
  /** key is unique among all plugins: the plugin's ID and the contribution's. */
  key: string;
  plugin: string;
  id: string;
}

export interface PageContribution extends Base {
  title: string;
  icon?: Icon;
  component: ComponentType<{ cluster: string; namespace: string }>;
}

export interface ObjectFilter {
  kinds?: string[];
  when?(ctx: ObjectContext): boolean;
}

export interface DetailsTabContribution extends Base, ObjectFilter {
  label: string;
  component: ComponentType<ObjectContext>;
}

export interface DetailsSectionContribution extends Base, ObjectFilter {
  title?: string;
  component: ComponentType<ObjectContext>;
}

export interface ActionContribution extends Base, ObjectFilter {
  label: string;
  icon?: Icon;
  danger?: boolean;
  run(ctx: ObjectContext): void | Promise<void>;
}

export interface ColumnContribution extends Base {
  title: string;
  kinds: string[];
  value(o: KObj): string | number;
  render?(o: KObj): ReactNode;
  align?: "right";
  width?: number | string;
  mono?: boolean;
}

export interface CommandContribution extends Base {
  label: string;
  keywords?: string;
  icon?: Icon;
  when?(ctx: AppContext): boolean;
  run(ctx: AppContext): void | Promise<void>;
}

export interface TitleBarContribution extends Base {
  component: ComponentType;
}

export interface OverviewCardContribution extends Base {
  title?: string;
  wide?: boolean;
  component: ComponentType<{ cluster: string; namespace: string }>;
}

export interface SettingsContribution extends Base {
  component: ComponentType<{ settings: Record<string, unknown>; save(next: Record<string, unknown>): Promise<void> }>;
}

export interface Contributions {
  pages: PageContribution[];
  detailsTabs: DetailsTabContribution[];
  detailsSections: DetailsSectionContribution[];
  actions: ActionContribution[];
  columns: ColumnContribution[];
  commands: CommandContribution[];
  titleBar: TitleBarContribution[];
  overviewCards: OverviewCardContribution[];
  settings: SettingsContribution[];
}

export type ContributionKind = keyof Contributions;

/** PluginState is how loading a plugin went. */
export interface PluginState {
  status: "loading" | "loaded" | "failed";
  error?: string;
}

interface PluginStore extends Contributions {
  states: Record<string, PluginState>;
  /** names are the plugins' names, by ID, for messages. */
  names: Record<string, string>;
}

const empty = (): Contributions => ({
  pages: [],
  detailsTabs: [],
  detailsSections: [],
  actions: [],
  columns: [],
  commands: [],
  titleBar: [],
  overviewCards: [],
  settings: [],
});

export const usePlugins = create<PluginStore>(() => ({ ...empty(), states: {}, names: {} }));

/** add adds a contribution, replacing one with the same key, and returns what removes it. */
export function add<K extends ContributionKind>(kind: K, c: Contributions[K][number]): () => void {
  usePlugins.setState((s) => ({ [kind]: [...(s[kind] as Base[]).filter((x) => x.key !== c.key), c] }) as Partial<PluginStore>);
  return () => usePlugins.setState((s) => ({ [kind]: (s[kind] as Base[]).filter((x) => x !== c) }) as Partial<PluginStore>);
}

/** removeAll removes what a plugin added. */
export function removeAll(plugin: string) {
  usePlugins.setState((s) => {
    const next: Partial<PluginStore> = {};
    for (const kind of Object.keys(empty()) as ContributionKind[]) {
      const list = s[kind] as Base[];
      if (list.some((c) => c.plugin === plugin)) (next as Record<string, Base[]>)[kind] = list.filter((c) => c.plugin !== plugin);
    }
    return next;
  });
}

export function setState(plugin: string, state: PluginState | null) {
  usePlugins.setState((s) => {
    const states = { ...s.states };
    if (state) states[plugin] = state;
    else delete states[plugin];
    return { states };
  });
}

/** matches tells whether a contribution is for an object. */
export function matches(f: ObjectFilter & { plugin: string }, ctx: ObjectContext): boolean {
  if (f.kinds && !f.kinds.includes(ctx.kind)) return false;
  if (!f.when) return true;
  return guard(f.plugin, () => !!f.when!(ctx), false);
}

/** guard runs a plugin's function, reporting what it throws instead of letting it break the app. */
export function guard<T>(plugin: string, f: () => T, fallback: T): T {
  try {
    return f();
  } catch (e) {
    reportError(plugin, e);
    return fallback;
  }
}

const reported = new Set<string>();

/** reportError logs an error of a plugin, once per message. */
export function reportError(plugin: string, e: unknown) {
  const msg = e instanceof Error ? e.message : String(e);
  const key = `${plugin}\n${msg}`;
  if (reported.has(key)) return;
  reported.add(key);
  console.error(`[plugin ${plugin}]`, e);
}

export const pluginPagePrefix = "plugin:";
export const pluginTabPrefix = "plugin/";
