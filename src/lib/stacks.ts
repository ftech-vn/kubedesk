import { create } from "zustand";
import { Stacks, events, type StackCatalog, type StackJob } from "../mygo";
import { toast, useApp } from "./store";

interface StacksStore {
  catalog: StackCatalog | null;
  /** jobs are this session's installs and uninstalls, the latest first. */
  jobs: StackJob[];
  /** installedTick changes when a job ends, for the lists to reload. */
  installedTick: number;
  loadCatalog(): Promise<void>;
  init(): void;
}

let started = false;

export const useStacks = create<StacksStore>((set, get) => ({
  catalog: null,
  jobs: [],
  installedTick: 0,

  async loadCatalog() {
    set({ catalog: await Stacks.catalog() });
  },

  init() {
    if (started) return;
    started = true;
    Stacks.jobs()
      .then((jobs) => set({ jobs }))
      .catch(() => {});
    // A job that ends is told of even when its dialog was closed.
    events.stacksChanged.on((jobs) => {
      const before = new Map(get().jobs.map((j) => [j.id, j.state]));
      let ended = false;
      for (const j of jobs) {
        if (before.get(j.id) !== "running" || j.state === "running") continue;
        ended = true;
        const open = useApp.getState().dialog;
        if (open?.kind === "stack" && open.job === j.id) continue;
        const what = j.action === "install" ? "installed in" : "removed from";
        if (j.state === "done") toast.ok(`${j.name} ${what} ${j.namespace}`);
        else if (j.state === "failed") toast.error(`${j.name} failed in ${j.namespace}`, j.error);
      }
      set((s) => ({ jobs, installedTick: ended ? s.installedTick + 1 : s.installedTick }));
    });
  },
}));

/** categoryOrder puts the categories people look for first at the top. */
export const categoryOrder = ["Logging", "Monitoring", "Database", "Messaging", "Networking", "CI/CD"];

export const newStackTemplate = `# A stack is a set of components installed together into a namespace.
# Components run in order; each is one of:
#   helm:     a Helm chart (needs the helm command)
#   urls:     manifests downloaded from URLs, applied by Kube Desk
#   manifest: manifests written here, applied by Kube Desk
# Values, manifests and URLs are Go templates of the fields:
# {{ .myField }}, {{ quote .myField }}, {{ .Namespace }}, {{ if .flag }}…{{ end }}.
id: my-stack
name: My stack
category: Other
description: What it runs, in a sentence or two.
namespace: my-stack
requires: About 256 MB of memory.
fields:
  - key: replicas
    label: Replicas
    type: number          # text, password, number, bool or select
    default: "1"
  - key: password
    label: Password
    type: password
    generate: true        # made for you when left empty
  - key: withRedis
    label: Add a Redis
    type: bool
components:
  - name: app
    title: The app
    manifest: |
      apiVersion: apps/v1
      kind: Deployment
      metadata:
        name: app
      spec:
        replicas: {{ .replicas }}
        selector:
          matchLabels: {app: app}
        template:
          metadata:
            labels: {app: app}
          spec:
            containers:
              - name: app
                image: nginx:1.27
                ports:
                  - containerPort: 80
      ---
      apiVersion: v1
      kind: Service
      metadata:
        name: app
      spec:
        selector: {app: app}
        ports:
          - port: 80
  # - name: redis
  #   title: Redis
  #   when: withRedis       # only when the bool field is on
  #   helm:
  #     repo: https://charts.example.com
  #     chart: redis
  #     version: 1.2.3
  #     values: |
  #       auth:
  #         password: {{ quote .password }}
  # - name: operator
  #   namespace: my-operator  # a namespace of its own, shared with other stacks
  #   urls:
  #     - https://example.com/releases/v1.0.0/install.yaml
access:
  - label: The app
    service: app
    port: 80
notes: |
  Shown once the stack runs, in Markdown. The app is at
  \`http://app.{{ .Namespace }}.svc\`.
`;

/** copySource makes the YAML of a copy of a stack, with an id of its own. */
export function copySource(source: string, id: string, ids: string[]): string {
  let n = 2;
  let copy = `${id}-copy`;
  while (ids.includes(copy)) copy = `${id}-copy-${n++}`;
  return source.replace(/^id:\s*\S+/m, `id: ${copy}`).replace(/^name:\s*(.+)$/m, (_, name: string) => `name: ${name.trim()} (copy)`);
}
