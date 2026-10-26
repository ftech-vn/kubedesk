import { useState } from "react";
import { app, k8s, registerObjectAction, registerPage, ui } from "kubedesk";

const pods = { group: "", version: "v1", resource: "pods" };

/** A page that lists the pods that are not running well. */
function Unhealthy({ cluster, namespace }: { cluster: string; namespace: string }) {
  const { items, loading, error } = k8s.useList(cluster, pods, namespace);
  const [only, setOnly] = useState(true);
  if (error) return <ui.ErrorBox message={error} />;
  const shown = only ? items.filter((p) => k8s.podStatus(p).tone === "bad") : items;
  return (
    <ui.Page
      title="Hello"
      subtitle={namespace ? `Pods in ${namespace}` : "Pods in all namespaces"}
      actions={<ui.Button onClick={() => setOnly(!only)}>{only ? "Show all" : "Only unhealthy"}</ui.Button>}
    >
      {loading ? (
        <ui.Spinner />
      ) : (
        <ui.Table
          items={shown}
          columns={[
            { id: "name", title: "Name", value: (p) => p.metadata.name },
            { id: "namespace", title: "Namespace", value: (p) => p.metadata.namespace ?? "" },
            { id: "status", title: "Status", value: (p) => k8s.podStatus(p).reason, render: (p) => <ui.Badge tone={k8s.podStatus(p).tone}>{k8s.podStatus(p).reason}</ui.Badge> },
          ]}
          onOpen={(p) => app.openObject(cluster, { gvr: pods, kind: "Pod", namespace: p.metadata.namespace ?? "", name: p.metadata.name })}
        />
      )}
    </ui.Page>
  );
}

// activate runs when Kube Desk loads the plugin. What it registers goes
// away when the plugin is turned off, or reloaded in development.
export function activate() {
  registerPage({ id: "unhealthy", title: "Hello", component: Unhealthy });
  registerObjectAction({
    id: "say-hello",
    label: "Say hello",
    kinds: ["Pod"],
    run: ({ object }) => app.toast.ok(`Hello, ${object.metadata.name}`),
  });
}
