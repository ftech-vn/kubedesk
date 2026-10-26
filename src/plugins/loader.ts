import * as React from "react";
import * as ReactDOM from "react-dom";
import * as jsxRuntime from "react/jsx-runtime";
import { create } from "zustand";
import { AppInfo, Plugins, events, type Plugin } from "../mygo";
import { errorMessage } from "../lib/k8s";
import { createApi, loadSettings } from "./api";
import { removeAll, reportError, setState, usePlugins } from "./registry";

// Loads the plugins that are on: their bundle, a CommonJS module, runs with
// module, exports, require and KubeDesk, and require gives it the app's
// React and the plugin's own "kubedesk" module. Plugins load one at a time,
// and turning one off or rebuilding one in development unloads it first.

/** usePluginList is the plugins the app knows of, on or off. */
export const usePluginList = create<{ list: Plugin[]; loaded: boolean }>(() => ({ list: [], loaded: false }));

interface Loaded {
  dispose(): void;
  cleanup?: () => void;
  deactivate?: () => void;
}

const loaded = new Map<string, Loaded>();
let appVersion = "";
let queue: Promise<void> = Promise.resolve();

// React's production build has no jsxDEV; plugins built in development
// mode call it, with the arguments of jsx first.
const jsxDevRuntime = {
  Fragment: jsxRuntime.Fragment,
  jsxDEV: (type: React.ElementType, props: object, key?: React.Key) => jsxRuntime.jsx(type, props, key),
};

/** startPlugins loads the plugins that are on, and follows changes to them. */
export async function startPlugins() {
  appVersion = (await AppInfo.about().catch(() => ({ version: "" }))).version;
  events.pluginsChanged.on((id) => schedule(() => (id ? reload(id) : sync())));
  await schedule(sync);
}

/** refreshPlugins reads the list again and loads or unloads what changed. */
export function refreshPlugins() {
  return schedule(sync);
}

function schedule(f: () => Promise<void>) {
  queue = queue.then(f).catch((e) => console.error("plugins:", e));
  return queue;
}

async function readList() {
  const list = await Plugins.list();
  usePluginList.setState({ list, loaded: true });
  usePlugins.setState({ names: Object.fromEntries(list.map((p) => [p.id, p.name])) });
  return list;
}

async function sync() {
  const list = await readList();
  const wanted = new Map(list.filter((p) => p.enabled && !p.error).map((p) => [p.id, p]));
  for (const id of [...loaded.keys()]) if (!wanted.has(id)) unload(id);
  for (const p of list) if (!wanted.has(p.id)) setState(p.id, null);
  for (const p of wanted.values()) if (!loaded.has(p.id)) await load(p);
}

async function reload(id: string) {
  const list = await readList();
  unload(id);
  const p = list.find((x) => x.id === id);
  if (p?.enabled && !p.error) await load(p);
  else setState(id, null);
}

async function load(p: Plugin) {
  setState(p.id, { status: "loading" });
  const { api, dispose } = createApi({ id: p.id, name: p.name, version: p.version }, appVersion);
  const entry: Loaded = { dispose };
  loaded.set(p.id, entry);
  try {
    const [source] = await Promise.all([Plugins.source(p.id), loadSettings(p.id)]);
    const modules: Record<string, unknown> = {
      react: React,
      "react/jsx-runtime": jsxRuntime,
      "react/jsx-dev-runtime": jsxDevRuntime,
      "react-dom": ReactDOM,
      kubedesk: api,
    };
    const require = (name: string) => {
      if (name in modules) return modules[name];
      throw new Error(`Kube Desk does not provide "${name}": bundle it into the plugin`);
    };
    const module: { exports: Record<string, any> } = { exports: {} };
    // The plugin's code is the user's to trust, as an extension of the
    // app; turning a plugin on says so.
    const run = new Function("module", "exports", "require", "KubeDesk", `${source}\n//# sourceURL=kubedesk-plugin://${encodeURIComponent(p.id)}/main.js`);
    run.call(undefined, module, module.exports, require, api);
    const ex = module.exports;
    const def = ex.default;
    const activate = typeof ex.activate === "function" ? ex.activate : typeof def === "function" ? def : typeof def?.activate === "function" ? def.activate : undefined;
    entry.deactivate = typeof ex.deactivate === "function" ? ex.deactivate : typeof def?.deactivate === "function" ? def.deactivate : undefined;
    if (activate) {
      const r = await activate(api);
      if (typeof r === "function") entry.cleanup = r;
    }
    if (loaded.get(p.id) === entry) setState(p.id, { status: "loaded" });
  } catch (e) {
    reportError(p.id, e);
    if (loaded.get(p.id) === entry) unload(p.id);
    setState(p.id, { status: "failed", error: errorMessage(e) });
  }
}

function unload(id: string) {
  const entry = loaded.get(id);
  if (!entry) return;
  loaded.delete(id);
  for (const f of [entry.cleanup, entry.deactivate]) {
    try {
      f?.();
    } catch (e) {
      reportError(id, e);
    }
  }
  entry.dispose();
  removeAll(id);
}
