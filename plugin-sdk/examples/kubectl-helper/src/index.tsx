import { Terminal } from "lucide-react";
import { app, k8s, registerCommand, registerDetailsTab, registerObjectAction, registerSettings, settings, ui, type GVR, type KObj } from "kubedesk";
import { useState } from "react";

// kubectl commands for what you look at in Kube Desk, and the requests and
// limits of a pod's containers.

function contextOf(cluster: string): string {
  return app.clusters().find((c) => c.id === cluster)?.context ?? "";
}

function quote(s: string): string {
  return /^[\w@%+=:,./-]+$/.test(s) ? s : `'${s.replace(/'/g, `'\\''`)}'`;
}

/** command is the kubectl command that gets an object. */
function command(cluster: string, gvr: GVR, object: KObj): string {
  const s = settings.get();
  const kubectl = String(s.kubectl || "kubectl");
  const output = String(s.output || "yaml");
  const resource = gvr.group ? `${gvr.resource}.${gvr.group}` : gvr.resource;
  const parts = [kubectl, "--context", quote(contextOf(cluster)), "get", `${resource}/${object.metadata.name}`];
  if (object.metadata.namespace) parts.push("-n", quote(object.metadata.namespace));
  parts.push("-o", output);
  return parts.join(" ");
}

function Resources({ object }: { object: KObj }) {
  const containers: any[] = object.spec?.containers ?? [];
  const cell = (v?: string) => v ?? <span className="muted">—</span>;
  const cpu = (r: any) => r?.cpu && k8s.formatCores(k8s.parseQuantity(r.cpu));
  const mem = (r: any) => r?.memory && k8s.formatBytes(k8s.parseQuantity(r.memory));
  return (
    <div style={{ padding: 16 }}>
      {containers.map((c) => (
        <ui.Card key={c.name} title={c.name}>
          <ui.KV
            items={[
              ["CPU request", cell(cpu(c.resources?.requests))],
              ["CPU limit", cell(cpu(c.resources?.limits))],
              ["Memory request", cell(mem(c.resources?.requests))],
              ["Memory limit", cell(mem(c.resources?.limits))],
            ]}
          />
          {!c.resources?.limits?.memory && <div className="text-warn small">No memory limit: the container can use all of its node's memory.</div>}
        </ui.Card>
      ))}
    </div>
  );
}

function Settings({ settings: s, save }: { settings: Record<string, unknown>; save(next: Record<string, unknown>): Promise<void> }) {
  const [kubectl, setKubectl] = useState(String(s.kubectl ?? "kubectl"));
  const [output, setOutput] = useState(String(s.output ?? "yaml"));
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
      <ui.Field label="Command" hint="kubectl, or another name for it, such as k or oc.">
        <input value={kubectl} onChange={(e) => setKubectl(e.target.value)} />
      </ui.Field>
      <ui.Field label="Output">
        <select value={output} onChange={(e) => setOutput(e.target.value)}>
          <option value="yaml">yaml</option>
          <option value="json">json</option>
          <option value="wide">wide</option>
        </select>
      </ui.Field>
      <div>
        <ui.Button variant="primary" onClick={() => save({ kubectl: kubectl.trim() || "kubectl", output })}>
          Save
        </ui.Button>
      </div>
    </div>
  );
}

export function activate() {
  registerObjectAction({
    id: "copy-command",
    label: "Copy kubectl command",
    icon: Terminal,
    run: async ({ cluster, gvr, object }) => {
      await app.copy(command(cluster, gvr, object));
      app.toast.ok("kubectl command copied");
    },
  });
  registerCommand({
    id: "use-context",
    label: "Copy kubectl use-context for this cluster",
    keywords: "kubectl context config",
    icon: Terminal,
    when: ({ cluster }) => !!cluster,
    run: async ({ cluster }) => {
      await app.copy(`kubectl config use-context ${quote(contextOf(cluster!))}`);
      app.toast.ok("Command copied");
    },
  });
  registerDetailsTab({ id: "resources", label: "Resources", kinds: ["Pod"], component: Resources });
  registerSettings(Settings);
}
