import { useEffect, useState } from "react";
import { CheckCircle2, FileUp, FolderOpen, KeyRound, ClipboardPaste, RefreshCw, Trash2 } from "lucide-react";
import { Clusters, type Cluster, type KubeconfigCandidate } from "../mygo";
import { errorMessage } from "../lib/k8s";
import { toast, useApp } from "../lib/store";
import { clusterColor, palette, sourceLabel } from "../lib/clusterLook";
import { Button, Field, Modal, Tabs, useAutoFocus } from "./ui";
import { ClusterTile, reconnect, removeCluster } from "./ClusterRail";

type Mode = "file" | "paste" | "token";

export function AddClusterDialog({ onClose }: { onClose(): void }) {
  const [mode, setMode] = useState<Mode>("file");
  const [candidates, setCandidates] = useState<KubeconfigCandidate[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [name, setName] = useState("");
  const [content, setContent] = useState("");
  const [server, setServer] = useState("");
  const [token, setToken] = useState("");
  const [ca, setCA] = useState("");
  const [insecure, setInsecure] = useState(false);
  const [namespace, setNamespace] = useState("");
  const nameRef = useAutoFocus<HTMLInputElement>();

  useEffect(() => {
    Clusters.discover().then(setCandidates, () => setCandidates([]));
  }, []);

  const added = async (clusters: Cluster[] | undefined | null) => {
    if (!clusters?.length) return;
    const s = useApp.getState();
    await s.refresh();
    for (const c of clusters) s.checkStatus(c.id);
    toast.ok(clusters.length === 1 ? `Added ${clusters[0]!.name}` : `Added ${clusters.length} clusters`, clusters.map((c) => c.name).join(", "));
    s.activate(clusters[0]!.id);
    onClose();
  };

  const attempt = async (f: () => Promise<Cluster[] | null | undefined>) => {
    setBusy(true);
    setError("");
    try {
      await added(await f());
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const submit = () => {
    if (mode === "paste") return attempt(() => Clusters.addKubeconfig(name, content));
    if (mode === "token") return attempt(() => Clusters.addManual({ name, server, token, caData: ca, insecure, namespace }));
  };

  return (
    <Modal
      title="Add a cluster"
      onClose={onClose}
      width={620}
      footer={
        mode !== "file" && (
          <>
            <Button variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button variant="primary" busy={busy} onClick={submit} disabled={mode === "paste" ? !content.trim() : !server.trim()}>
              Add cluster
            </Button>
          </>
        )
      }
    >
      <Tabs
        tabs={[
          { id: "file", label: <span className="tab-label"><FileUp size={14} /> Kubeconfig file</span> },
          { id: "paste", label: <span className="tab-label"><ClipboardPaste size={14} /> Paste kubeconfig</span> },
          { id: "token", label: <span className="tab-label"><KeyRound size={14} /> Server &amp; token</span> },
        ]}
        value={mode}
        onChange={(m) => {
          setMode(m);
          setError("");
        }}
      />
      <div className="add-body">
        {mode === "file" && (
          <>
            <p className="muted small">The app reads the file where it is, so clusters stay up to date when tools such as <code>gcloud</code>, <code>aws</code> or <code>az</code> rewrite it. The contexts of <code>~/.kube/config</code> and <code>$KUBECONFIG</code> are always shown.</p>
            <Button variant="primary" icon={<FolderOpen size={15} />} busy={busy} onClick={() => attempt(() => Clusters.pickFiles())}>
              Choose kubeconfig files…
            </Button>
            {candidates && candidates.length > 0 && (
              <div className="candidates">
                <div className="field-label">Found in ~/.kube</div>
                {candidates.map((c) => (
                  <div key={c.path} className="candidate">
                    <div>
                      <div className="mono small">{c.path}</div>
                      <div className="faint small">{c.contexts.join(", ")}</div>
                    </div>
                    <Button size="sm" onClick={() => attempt(() => Clusters.addFile(c.path))}>
                      Add
                    </Button>
                  </div>
                ))}
              </div>
            )}
          </>
        )}
        {mode === "paste" && (
          <>
            <Field label="Name" hint="Optional; shown instead of the context's name">
              <input ref={nameRef} value={name} onChange={(e) => setName(e.target.value)} placeholder="production-eu" />
            </Field>
            <Field label="Kubeconfig" hint="Kept in the app's data folder, readable only by you">
              <textarea className="mono" rows={12} value={content} onChange={(e) => setContent(e.target.value)} placeholder={"apiVersion: v1\nkind: Config\nclusters:\n  - name: …"} spellCheck={false} />
            </Field>
          </>
        )}
        {mode === "token" && (
          <>
            <div className="form-row">
              <Field label="Name">
                <input ref={nameRef} value={name} onChange={(e) => setName(e.target.value)} placeholder="staging" />
              </Field>
              <Field label="Default namespace">
                <input value={namespace} onChange={(e) => setNamespace(e.target.value)} placeholder="all namespaces" />
              </Field>
            </div>
            <Field label="API server">
              <input value={server} onChange={(e) => setServer(e.target.value)} placeholder="https://10.0.0.1:6443" className="mono" />
            </Field>
            <Field label="Bearer token" hint="A service account token, for example from kubectl create token">
              <textarea className="mono" rows={3} value={token} onChange={(e) => setToken(e.target.value)} spellCheck={false} />
            </Field>
            <Field label="CA certificate (PEM)" hint="Leave empty to trust the system's certificates">
              <textarea className="mono" rows={4} value={ca} onChange={(e) => setCA(e.target.value)} disabled={insecure} placeholder="-----BEGIN CERTIFICATE-----" spellCheck={false} />
            </Field>
            <label className="check">
              <input type="checkbox" checked={insecure} onChange={(e) => setInsecure(e.target.checked)} /> Skip TLS verification (insecure)
            </label>
          </>
        )}
        {error && <div className="form-error">{error}</div>}
      </div>
    </Modal>
  );
}

export function ClusterSettingsDialog({ id, onClose }: { id: string; onClose(): void }) {
  const c = useApp((s) => s.clusters.find((x) => x.id === id));
  const status = useApp((s) => s.status[id]);
  const [alias, setAlias] = useState(c && c.name !== c.context ? c.name : "");
  const [color, setColor] = useState(c?.color ?? "");
  const [production, setProduction] = useState(c?.production ?? false);
  const [readOnly, setReadOnly] = useState(c?.readOnly ?? false);
  const [busy, setBusy] = useState(false);
  if (!c) return null;

  const save = async () => {
    setBusy(true);
    try {
      await Clusters.update(id, { alias, color, hidden: c.hidden, production, readOnly });
      await useApp.getState().refresh();
      onClose();
    } catch (e) {
      toast.error("Could not save", e);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      title={
        <span className="row gap">
          <ClusterTile c={{ ...c, color, name: alias || c.context }} size={26} /> Cluster settings
        </span>
      }
      onClose={onClose}
      width={520}
      footer={
        <>
          <Button variant="danger" icon={<Trash2 size={14} />} onClick={() => removeCluster(c).then(onClose)}>
            Remove
          </Button>
          <div className="spacer" />
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" busy={busy} onClick={save}>
            Save
          </Button>
        </>
      }
    >
      <div className="add-body">
        <Field label="Display name">
          <input value={alias} onChange={(e) => setAlias(e.target.value)} placeholder={c.context} autoFocus />
        </Field>
        <Field label="Color">
          <div className="swatches">
            <button className={`swatch auto ${!color ? "on" : ""}`} onClick={() => setColor("")} title="Automatic" style={{ background: clusterColor({ id: c.id, color: "" }) }} />
            {palette.map((p) => (
              <button key={p} className={`swatch ${color === p ? "on" : ""}`} onClick={() => setColor(p)} style={{ background: p }} />
            ))}
          </div>
        </Field>
        <div className="toggles">
          <label className="toggle">
            <input type="checkbox" checked={production} onChange={(e) => setProduction(e.target.checked)} />
            <span>
              <b>Production cluster</b>
              <span className="faint small">Marked in red, and every change asks you to type the cluster's name first.</span>
            </span>
          </label>
          <label className="toggle">
            <input type="checkbox" checked={readOnly} onChange={(e) => setReadOnly(e.target.checked)} />
            <span>
              <b>Read-only</b>
              <span className="faint small">Kube Desk refuses every change to this cluster, shells included. Logs and port forwards still work.</span>
            </span>
          </label>
        </div>
        <dl className="kv compact">
          <div className="kv-row">
            <dt>Context</dt>
            <dd className="mono small">{c.context}</dd>
          </div>
          <div className="kv-row">
            <dt>Server</dt>
            <dd className="mono small">{c.server}</dd>
          </div>
          <div className="kv-row">
            <dt>Source</dt>
            <dd className="mono small" title={c.source}>
              {sourceLabel(c)} · {c.source}
            </dd>
          </div>
          <div className="kv-row">
            <dt>Status</dt>
            <dd className="small">
              {status?.checking ? "Checking…" : status?.ok ? (
                <span className="text-ok">
                  <CheckCircle2 size={13} /> Connected · {status.version}
                </span>
              ) : (
                <span className="text-bad">{status?.error ?? "Not checked"}</span>
              )}
              <button className="link small" style={{ marginLeft: 8 }} onClick={() => reconnect(c.id)}>
                <RefreshCw size={12} /> Test again
              </button>
            </dd>
          </div>
        </dl>
      </div>
    </Modal>
  );
}
