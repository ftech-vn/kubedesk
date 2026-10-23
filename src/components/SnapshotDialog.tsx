import { useState } from "react";
import { Camera, CheckCircle2, FolderOpen } from "lucide-react";
import { Clusters, Kube, type SnapshotResult } from "../mygo";
import { errorMessage, formatBytes } from "../lib/k8s";
import { toast, useApp } from "../lib/store";
import { useWatch } from "../lib/watch";
import { Button, Field, Modal } from "./ui";

/** SnapshotDialog saves a namespace to a file others open without access to the cluster. */
export function SnapshotDialog({ cluster, namespace, onClose }: { cluster: string; namespace: string; onClose(): void }) {
  const name = useApp((s) => s.clusters.find((c) => c.id === cluster)?.name ?? cluster);
  const namespaces = useWatch(cluster, { group: "", version: "v1", resource: "namespaces" });
  const [ns, setNs] = useState(namespace);
  const [logs, setLogs] = useState(true);
  const [changes, setChanges] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState<SnapshotResult | null>(null);

  const take = async () => {
    setBusy(true);
    setError("");
    try {
      const r = await Kube.exportSnapshot(cluster, ns, { logs, changes });
      if (r.path) setDone(r);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      title={
        <span className="row gap">
          <Camera size={17} /> Snapshot of a namespace
        </span>
      }
      onClose={onClose}
      width={560}
      footer={
        done ? (
          <>
            <Button variant="ghost" icon={<FolderOpen size={14} />} onClick={() => Kube.revealFile(done.path).catch((e) => toast.error("Could not show the file", e))}>
              Show in Finder
            </Button>
            <div className="spacer" />
            <Button variant="primary" onClick={onClose}>
              Done
            </Button>
          </>
        ) : (
          <>
            <div className="spacer" />
            <Button variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button variant="primary" busy={busy} disabled={!ns} onClick={take}>
              Save snapshot…
            </Button>
          </>
        )
      }
    >
      {done ? (
        <div className="stack">
          <div className="test-ok">
            <CheckCircle2 size={14} /> Saved {done.objects} objects{done.logs ? ` and the logs of ${done.logs} containers` : ""}, {formatBytes(done.bytes)}.
          </div>
          <div className="mono small selectable">{done.path}</div>
          <p className="muted small">
            Anyone with Kube Desk opens it from the Finder, or with <b>Open snapshot</b> on the clusters page, and browses it like a read-only cluster, without access to {name}.
          </p>
          {done.skipped?.length > 0 && <p className="faint small">Left out, as your access does not allow listing them: {done.skipped.join(", ")}.</p>}
        </div>
      ) : (
        <div className="add-body">
          <p className="muted small">
            Saves what a namespace of <b>{name}</b> holds right now to a <code>.kubedesk</code> file: every object, its events, the nodes its pods run on, and what you choose below. Others open it in Kube Desk as a read-only cluster, with no access to {name}.
          </p>
          <Field label="Namespace">
            <select value={ns} onChange={(e) => setNs(e.target.value)}>
              {!ns && <option value="">Choose a namespace</option>}
              {(namespaces.items.length ? namespaces.items.map((n) => n.metadata.name) : ns ? [ns] : []).sort().map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
            </select>
          </Field>
          <div className="toggles">
            <label className="toggle">
              <input type="checkbox" checked={logs} onChange={(e) => setLogs(e.target.checked)} />
              <span>
                <b>Logs</b>
                <span className="faint small">The last 300 lines of each container, and of its run before the last restart. Tokens and passwords are masked.</span>
              </span>
            </label>
            <label className="toggle">
              <input type="checkbox" checked={changes} onChange={(e) => setChanges(e.target.checked)} />
              <span>
                <b>Changes</b>
                <span className="faint small">The timeline Kube Desk recorded, if you watch this namespace.</span>
              </span>
            </label>
          </div>
          <p className="faint small">Secret values never go in the file: only a fingerprint of each, so that changes still show.</p>
          {error && <div className="form-error">{error}</div>}
        </div>
      )}
    </Modal>
  );
}

/** openSnapshots asks for snapshot files and shows the first one opened. */
export async function openSnapshots() {
  try {
    const opened = await Clusters.openSnapshot();
    if (!opened.length) return;
    await useApp.getState().refresh();
    useApp.getState().activate(opened[0]!.id);
    toast.ok(opened.length === 1 ? `Opened the snapshot of ${opened[0]!.name}` : `Opened ${opened.length} snapshots`);
  } catch (e) {
    toast.error("Could not open the snapshot", e);
  }
}
