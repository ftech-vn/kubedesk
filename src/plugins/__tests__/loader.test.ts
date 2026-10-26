import { beforeAll, describe, expect, mock, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";

// The loader runs the cert-manager example as Kube Desk would: its build,
// evaluated with the app's React and its own "kubedesk" module.

const example = join(import.meta.dir, "../../../plugin-sdk/examples/cert-manager");
// Build it as its author would, so the test runs the build of today's source.
const built = Bun.spawnSync(["bun", "run", "build"], { cwd: example, stderr: "pipe" });
if (!built.success) throw new Error(`building the example failed: ${built.stderr}`);
const certManager = readFileSync(join(example, "dist/main.js"), "utf8");

type P = { id: string; name: string; version: string; source: string; path: string; enabled: boolean; error?: string };
let list: P[] = [];
const sources: Record<string, string> = {};
const saved: Record<string, Record<string, unknown>> = {};
let onChanged: (id: string) => void = () => {};

const event = () => ({ on: () => () => {} });
mock.module("../../mygo", () => ({
  AppInfo: { about: async () => ({ version: "0.8.0", canUpdate: false }) },
  Plugins: {
    list: async () => list,
    source: async (id: string) => sources[id] ?? "",
    settings: async (id: string) => saved[id] ?? {},
    saveSettings: async (id: string, s: Record<string, unknown>) => void (saved[id] = s),
  },
  Kube: { watch: async () => {}, list: async () => [], can: async () => [true] },
  Clusters: { setActive: async () => {}, list: async () => ({ clusters: [], errors: [] }) },
  events: {
    pluginsChanged: { on: (f: (id: string) => void) => ((onChanged = f), () => {}) },
    snapshotOpened: event(),
    pluginsOpen: event(),
    timelineChanged: event(),
  },
}));

let loader: typeof import("../loader");
let registry: typeof import("../registry");

beforeAll(async () => {
  (globalThis as any).navigator ??= {};
  (globalThis as any).navigator.platform ??= "MacIntel";
  loader = await import("../loader");
  registry = await import("../registry");
});

const plugin = (id: string, enabled = true): P => ({ id, name: id, version: "1.0.0", source: "installed", path: `/p/${id}`, enabled });
const keys = (kind: keyof import("../registry").Contributions) => registry.usePlugins.getState()[kind].map((c) => c.key);
const settle = () => loader.refreshPlugins();

describe("plugins", () => {
  test("load what they register", async () => {
    list = [plugin("kubedesk-plugin-cert-manager")];
    sources["kubedesk-plugin-cert-manager"] = certManager;
    saved["kubedesk-plugin-cert-manager"] = { warnDays: 30 };
    await loader.startPlugins();
    expect(registry.usePlugins.getState().states["kubedesk-plugin-cert-manager"]).toEqual({ status: "loaded" });
    expect(keys("pages")).toEqual(["kubedesk-plugin-cert-manager/certificates"]);
    expect(keys("overviewCards")).toEqual(["kubedesk-plugin-cert-manager/certificates"]);
    expect(keys("columns")).toEqual(["kubedesk-plugin-cert-manager/expires"]);
    expect(keys("detailsSections")).toEqual(["kubedesk-plugin-cert-manager/certificate"]);
    expect(keys("actions")).toEqual(["kubedesk-plugin-cert-manager/open-secret"]);
    expect(keys("settings")).toEqual(["kubedesk-plugin-cert-manager/settings"]);
  });

  test("render with the app's React", () => {
    const section = registry.usePlugins.getState().detailsSections[0]!;
    const cert = {
      kind: "Certificate",
      metadata: { name: "web", namespace: "shop", uid: "1" },
      spec: { secretName: "web-tls" },
      status: { conditions: [{ type: "Ready", status: "True", reason: "Ready" }], notAfter: new Date(Date.now() + 20 * 86400e3).toISOString() },
    };
    const html = renderToStaticMarkup(createElement(section.component, { cluster: "c", gvr: { group: "cert-manager.io", version: "v1", resource: "certificates" }, kind: "Certificate", object: cert }));
    expect(html).toContain("web-tls");
    // 20 days left is within the 30 days of the plugin's settings.
    expect(html).toContain("tone-warn");
    expect(html).toContain("in 20 days");
  });

  test("filter actions by kind and when", () => {
    const action = registry.usePlugins.getState().actions[0]!;
    const ctx = (kind: string, spec: object) => ({ cluster: "c", gvr: { group: "", version: "v1", resource: "x" }, kind, object: { metadata: { name: "a", uid: "u" }, spec } });
    expect(registry.matches(action, ctx("Certificate", { secretName: "s" }))).toBe(true);
    expect(registry.matches(action, ctx("Certificate", {}))).toBe(false);
    expect(registry.matches(action, ctx("Secret", { secretName: "s" }))).toBe(false);
  });

  test("turned off, leave nothing behind", async () => {
    list = [plugin("kubedesk-plugin-cert-manager", false)];
    await settle();
    expect(keys("pages")).toEqual([]);
    expect(keys("columns")).toEqual([]);
    expect(registry.usePlugins.getState().states["kubedesk-plugin-cert-manager"]).toBeUndefined();
  });

  test("that fail are put down to themselves, and do not stop others", async () => {
    sources.broken = `const k = require("kubedesk"); k.registerPage({ id: "half", title: "Half", component: () => null }); throw new Error("boom");`;
    sources.missing = `require("lodash");`;
    sources.good = `const k = require("kubedesk"); exports.activate = () => { k.registerCommand({ id: "hi", label: "Hi", run() {} }); return () => { globalThis.cleanedUp = true; }; };`;
    list = [plugin("broken"), plugin("missing"), plugin("good")];
    await settle();
    const states = registry.usePlugins.getState().states;
    expect(states.broken).toEqual({ status: "failed", error: "boom" });
    expect(states.missing?.error).toContain(`does not provide "lodash"`);
    expect(states.good).toEqual({ status: "loaded" });
    // What the broken plugin registered before it threw is gone.
    expect(keys("pages")).toEqual([]);
    expect(keys("commands")).toEqual(["good/hi"]);
  });

  test("reload one plugin when its build changes", async () => {
    sources.good = `const k = require("kubedesk"); exports.activate = () => { k.registerCommand({ id: "bye", label: "Bye", run() {} }); };`;
    onChanged("good");
    await settle();
    expect((globalThis as any).cleanedUp).toBe(true);
    expect(keys("commands")).toEqual(["good/bye"]);
  });
});
