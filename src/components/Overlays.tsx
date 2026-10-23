import { HealthDialog } from "./HealthDialog";
import { SnapshotDialog } from "./SnapshotDialog";
import { PromoteDialog } from "./PromoteDialog";
import { RunbookEditor } from "./Runbooks";
import { useEffect, useState } from "react";
import { CheckCircle2, Info, OctagonAlert, ShieldAlert, TriangleAlert, X, XCircle } from "lucide-react";
import { Kube, type ImpactItem, type Report } from "../mygo";
import { templates } from "../lib/templates";
import { confirmChange } from "../lib/actions";
import { toast, useActiveView, useApp, useAsk, useToasts } from "../lib/store";
import { Button, Field, Modal, Spinner, useAutoFocus } from "./ui";
import { YamlEditor } from "./YamlEditor";
import { AddClusterDialog, ClusterSettingsDialog } from "./ClusterDialogs";
import { CommandPalette } from "./CommandPalette";
import { AgentsDialog } from "./AgentsDialog";
import { AssistantSettingsDialog } from "./AssistantSettings";
import { SearchDialog } from "./SearchDialog";
import { CompareDialog } from "./CompareDialog";
import { DiffView } from "./DiffView";

export function Dialogs() {
  const dialog = useApp((s) => s.dialog);
  const close = () => useApp.getState().openDialog(null);
  if (!dialog) return null;
  switch (dialog.kind) {
    case "add":
      return <AddClusterDialog onClose={close} />;
    case "settings":
      return <ClusterSettingsDialog id={dialog.cluster} onClose={close} />;
    case "palette":
      return <CommandPalette onClose={close} />;
    case "create":
      return <CreateDialog initial={dialog.yaml ?? ""} onClose={close} />;
    case "agents":
      return <AgentsDialog onClose={close} />;
    case "assistant-settings":
      return <AssistantSettingsDialog onClose={close} />;
    case "health":
      return <HealthDialog onClose={close} />;
    case "snapshot":
      return <SnapshotDialog cluster={dialog.cluster} namespace={dialog.namespace} onClose={close} />;
    case "promote":
      return <PromoteDialog from={dialog.from} gvr={dialog.gvr} kind={dialog.object} namespace={dialog.namespace} name={dialog.name} onClose={close} />;
    case "runbook":
      return <RunbookEditor id={dialog.id} target={dialog.target} onClose={close} />;
    case "search":
      return <SearchDialog initial={dialog.query} onClose={close} />;
    case "compare":
      return <CompareDialog gvr={dialog.gvr} kind={dialog.object} namespace={dialog.namespace} name={dialog.name} onClose={close} />;
    case "report":
      return <ReportDialog cluster={dialog.cluster} kind={dialog.object} namespace={dialog.namespace} name={dialog.name} onClose={close} />;
  }
}

function CreateDialog({ initial, onClose }: { initial: string; onClose(): void }) {
  const cluster = useApp((s) => s.active);
  const view = useActiveView();
  const [text, setText] = useState(initial);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const apply = async () => {
    if (!cluster || !(await confirmChange(cluster, "Apply to production?", "The objects of the document are created or replaced."))) return;
    setBusy(true);
    setError("");
    try {
      const res = await Kube.apply(cluster, text, view?.namespace ?? "");
      toast.ok("Applied", res.join("\n"));
      onClose();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal
      title="Create or apply resources"
      onClose={onClose}
      width={760}
      className="create-modal"
      footer={
        <>
          <select className="select-sm" value="" onChange={(e) => e.target.value && setText(templates[e.target.value]!(view?.namespace ?? ""))}>
            <option value="">Start from a template…</option>
            {Object.keys(templates).map((k) => (
              <option key={k}>{k}</option>
            ))}
          </select>
          <div className="spacer" />
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" busy={busy} onClick={apply}>
            Apply
          </Button>
        </>
      }
    >
      <p className="muted small" style={{ margin: "0 0 8px" }}>
        Several documents may be separated by <code>---</code>. Objects without a namespace go into <b>{view?.namespace || "default"}</b>; existing ones are replaced.
      </p>
      <div className="create-editor">
        <YamlEditor value={text} onChange={setText} onSave={apply} />
      </div>
      {error && <div className="form-error">{error}</div>}
    </Modal>
  );
}

export function AskDialog() {
  const req = useAsk((s) => s.request);
  if (!req) return null;
  return <AskInner key={req.title} />;
}

function AskInner() {
  const req = useAsk((s) => s.request)!;
  const [value, setValue] = useState(req.input?.value ?? "");
  const [checked, setChecked] = useState(false);
  const [typed, setTyped] = useState("");
  const inputRef = useAutoFocus<HTMLInputElement>();
  const confirmRef = useAutoFocus<HTMLInputElement>();
  const allowed = !req.typeToConfirm || typed.trim() === req.typeToConfirm;
  const done = (ok: boolean) => {
    if (ok && !allowed) return;
    req.resolve({ ok, value, checked });
  };
  return (
    <Modal
      title={req.title}
      onClose={() => done(false)}
      width={460}
      className={req.typeToConfirm ? "prod-confirm" : undefined}
      footer={
        <>
          <Button variant="ghost" onClick={() => done(false)}>
            Cancel
          </Button>
          <Button variant={req.danger ? "danger" : "primary"} onClick={() => done(true)} disabled={!allowed} autoFocus={!req.input && !req.typeToConfirm}>
            {req.confirmLabel ?? "OK"}
          </Button>
        </>
      }
    >
      {req.typeToConfirm && (
        <div className="prod-warning">
          <ShieldAlert size={16} />
          <span>
            This is the production cluster <b>{req.typeToConfirm}</b>.
          </span>
        </div>
      )}
      {req.message && <p className="ask-message">{req.message}</p>}
      {req.input && (
        <Field label={req.input.label}>
          <input
            ref={req.typeToConfirm ? undefined : inputRef}
            type={req.input.type ?? "text"}
            value={value}
            placeholder={req.input.placeholder}
            onChange={(e) => setValue(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && done(true)}
            min={req.input.type === "number" ? 0 : undefined}
          />
        </Field>
      )}
      {req.checkbox && (
        <label className="check">
          <input type="checkbox" checked={checked} onChange={(e) => setChecked(e.target.checked)} /> {req.checkbox}
        </label>
      )}
      {req.diffs?.map((d, i) => (
        <div key={i} className="approval-diff">
          <div className="approval-diff-head">
            <span className={`badge tone-${d.action === "create" ? "ok" : d.action === "unchanged" ? "muted" : "info"}`}>{d.action}</span>
            <span className="mono small">
              {d.kind} {d.namespace ? `${d.namespace}/` : ""}
              {d.name}
            </span>
          </div>
          {d.action !== "unchanged" && <DiffView before={d.before} after={d.after} />}
        </div>
      ))}
      {req.impact && <ImpactList load={req.impact} value={value} />}
      {req.typeToConfirm && (
        <Field label={`Type ${req.typeToConfirm} to confirm`}>
          <input
            ref={confirmRef}
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && done(true)}
            placeholder={req.typeToConfirm}
            autoComplete="off"
            spellCheck={false}
            className="mono"
          />
        </Field>
      )}
    </Modal>
  );
}

function ReportDialog({ cluster, kind, namespace, name, onClose }: { cluster: string; kind: string; namespace: string; name: string; onClose(): void }) {
  const [report, setReport] = useState<Report | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    Kube.report(cluster, kind, namespace, name).then(setReport, (e) => setError(e instanceof Error ? e.message : String(e)));
  }, [cluster, kind, namespace, name]);
  const copyIt = () =>
    report &&
    navigator.clipboard.writeText(report.markdown).then(
      () => toast.ok("Report copied", "Paste it into Slack, an issue or a chat with an AI assistant."),
      (e) => toast.error("Could not copy", e),
    );
  const save = async () => {
    if (!report) return;
    const path = await Kube.saveText(`${kind.toLowerCase()}-${name}-${new Date().toISOString().slice(0, 16).replace(/[:T]/g, "")}.md`, report.markdown).catch((e) => {
      toast.error("Could not save", e);
      return "";
    });
    if (path) toast.ok("Report saved", path);
  };
  return (
    <Modal
      title={`Incident report · ${kind} ${name}`}
      onClose={onClose}
      width={820}
      className="report-modal"
      footer={
        <>
          {report && (
            <span className="faint small">
              {report.redacted > 0 ? `${report.redacted} secret${report.redacted === 1 ? "" : "s"} masked in the logs · ` : ""}check before sharing
            </span>
          )}
          <div className="spacer" />
          <Button variant="ghost" onClick={save} disabled={!report}>
            Save .md…
          </Button>
          <Button variant="primary" onClick={copyIt} disabled={!report}>
            Copy Markdown
          </Button>
        </>
      }
    >
      {error ? (
        <div className="form-error">{error}</div>
      ) : !report ? (
        <div className="center-pad">
          <Spinner />
          <div className="faint small" style={{ marginTop: 8 }}>
            Gathering state, events, logs and recent changes…
          </div>
        </div>
      ) : (
        <pre className="report-text selectable">{report.markdown}</pre>
      )}
    </Modal>
  );
}

/** ImpactList shows what a change would do, worked out by the Go side. */
function ImpactList({ load, value }: { load: (value: string) => Promise<ImpactItem[]>; value: string }) {
  const [items, setItems] = useState<ImpactItem[] | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    let alive = true;
    setItems(null);
    setError("");
    const t = setTimeout(() => {
      load(value).then(
        (r) => alive && setItems(r),
        (e) => alive && setError(e instanceof Error ? e.message : String(e)),
      );
    }, 250);
    return () => {
      alive = false;
      clearTimeout(t);
    };
    // load is made once per dialog.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value]);
  return (
    <div className="impact">
      <div className="impact-title">What this affects</div>
      {error ? (
        <div className="faint small">{error}</div>
      ) : !items ? (
        <div className="faint small row gap">
          <Spinner size={12} /> Looking at what depends on it…
        </div>
      ) : items.length === 0 ? (
        <div className="faint small">Nothing else in the cluster depends on it.</div>
      ) : (
        <ul className="impact-list">
          {items.map((it, i) => (
            <li key={i} className={`impact-item ${it.severity}`}>
              {it.severity === "danger" ? <OctagonAlert size={14} /> : it.severity === "warning" ? <TriangleAlert size={14} /> : <Info size={14} />}
              <span>{it.text}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

export function Toasts() {
  const { toasts, dismiss } = useToasts();
  return (
    <div className="toasts">
      {toasts.map((t) => (
        <div key={t.id} className={`toast toast-${t.tone}`}>
          {t.tone === "ok" ? <CheckCircle2 size={16} /> : t.tone === "bad" ? <XCircle size={16} /> : <Info size={16} />}
          <div className="toast-body">
            <div className="toast-title">{t.title}</div>
            {t.detail && <div className="toast-detail">{t.detail}</div>}
          </div>
          <button className="toast-close" onClick={() => dismiss(t.id)}>
            <X size={14} />
          </button>
        </div>
      ))}
    </div>
  );
}
