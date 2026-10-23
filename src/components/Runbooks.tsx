import { useEffect, useMemo, useRef, useState } from "react";
import { BookOpen, FolderOpen, Pencil, Plus, Search, Trash2, X } from "lucide-react";
import { Runbooks, type Runbook, type RunbookFolder, type RunbookTarget } from "../mygo";
import { errorMessage } from "../lib/k8s";
import { runAction, runbookActions, useRunbookVersion, type RunbookContext } from "../lib/runbooks";
import { confirmAction, toast, useActiveView, useApp } from "../lib/store";
import { Badge, Button, Empty, ErrorBox, Field, IconButton, Modal, Spinner } from "./ui";
import { Markdown } from "./Markdown";
import { ContextMenu } from "./Menu";

const targetLine = (t: RunbookTarget) =>
  [t.cluster && `cluster ${t.cluster}`, t.namespace && `ns ${t.namespace}`, t.kind, t.name].filter(Boolean).join(" · ") || "everything";

/** RunbookView shows a runbook, with its actions running on ctx. */
export function RunbookView({ runbook, ctx, onEdit }: { runbook: Runbook; ctx: RunbookContext; onEdit?(): void }) {
  // Read away from an object, the actions run on the one object the
  // runbook is for, when it names one.
  const exact = (v?: string) => !!v && !v.includes("*");
  const only = runbook.targets.find((t) => exact(t.kind) && exact(t.name));
  const at = ctx.kind && ctx.name ? ctx : only ? { ...ctx, kind: only.kind, name: only.name, namespace: exact(only.namespace) ? only.namespace! : ctx.namespace } : ctx;
  return (
    <article className="runbook">
      <header className="runbook-head">
        <BookOpen size={15} className="muted" />
        <h3>{runbook.title}</h3>
        <div className="spacer" />
        {onEdit && (
          <IconButton label="Edit" onClick={onEdit}>
            <Pencil size={14} />
          </IconButton>
        )}
      </header>
      <div className="runbook-targets">
        {runbook.targets.map((t, i) => (
          <Badge key={i} tone="muted">
            {targetLine(t)}
          </Badge>
        ))}
        <span className="faint small mono">{runbook.id}</span>
      </div>
      <Markdown className="md runbook-body" text={runbook.body || "_Empty._"} onAction={(href) => runAction(href, at)} />
    </article>
  );
}

/** RunbooksFor lists the runbooks of an object, in its details. */
export function RunbooksFor({ list, ctx }: { list: Runbook[]; ctx: RunbookContext }) {
  const openDialog = useApp((s) => s.openDialog);
  return (
    <div className="stack">
      {list.map((rb) => (
        <RunbookView key={rb.id} runbook={rb} ctx={ctx} onEdit={() => openDialog({ kind: "runbook", id: rb.id })} />
      ))}
    </div>
  );
}

/** RunbooksPage lists every runbook of the folder. */
export function RunbooksPage({ cluster }: { cluster: string }) {
  const view = useActiveView()!;
  const openDialog = useApp((s) => s.openDialog);
  const v = useRunbookVersion((s) => s.v);
  const bump = useRunbookVersion((s) => s.bump);
  const [list, setList] = useState<Runbook[] | null>(null);
  const [folder, setFolder] = useState<RunbookFolder | null>(null);
  const [error, setError] = useState("");
  const [filter, setFilter] = useState("");
  const [open, setOpen] = useState<string | null>(null);
  const [menu, setMenu] = useState<{ x: number; y: number } | null>(null);

  useEffect(() => {
    Runbooks.list().then(
      (l) => {
        setList(l);
        setError("");
      },
      (e) => setError(errorMessage(e)),
    );
    Runbooks.folder().then(setFolder, () => {});
  }, [v]);

  const f = filter.toLowerCase();
  const shown = (list ?? []).filter((rb) => !f || `${rb.title} ${rb.body} ${rb.targets.map(targetLine).join(" ")}`.toLowerCase().includes(f));
  const current = shown.find((rb) => rb.id === open) ?? shown[0];
  const ctx: RunbookContext = { cluster, namespace: view.namespace };

  return (
    <div className="list-view">
      <header className="list-header">
        <div className="list-title">
          <BookOpen size={18} className="muted" />
          <h1>Runbooks</h1>
          <span className="count">{list ? shown.length : <Spinner size={13} />}</span>
        </div>
        <div className="list-tools">
          <div className="search-input">
            <Search size={14} />
            <input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="Find a runbook…" />
            {filter && (
              <button className="clear" onClick={() => setFilter("")}>
                <X size={13} />
              </button>
            )}
          </div>
          <Button size="sm" icon={<FolderOpen size={14} />} onClick={(e) => setMenu({ x: e.clientX - 120, y: e.clientY + 14 })} title={folder?.path}>
            Folder
          </Button>
          <Button size="sm" variant="primary" icon={<Plus size={14} />} onClick={() => openDialog({ kind: "runbook", target: { namespace: view.namespace || undefined } })}>
            New runbook
          </Button>
        </div>
      </header>
      {error ? (
        <div className="list-error">
          <ErrorBox title="Could not read the runbooks" message={error} />
        </div>
      ) : list && !list.length ? (
        <Empty icon={<BookOpen size={28} />} title="No runbooks yet">
          <p>A runbook is a note of what to check and do when something fails, with buttons for the app's actions. Kube Desk shows it with the objects it is for, and the assistant follows it.</p>
          <p className="faint small">They are Markdown files in {folder?.default ? "the app's folder" : <span className="mono">{folder?.path}</span>}. Choose a folder in a git repository to share them with your team.</p>
          <Button variant="primary" onClick={() => openDialog({ kind: "runbook", target: { namespace: view.namespace || undefined } })}>
            Write a runbook
          </Button>
        </Empty>
      ) : (
        <div className="runbooks-split">
          <div className="runbooks-list">
            {shown.map((rb) => (
              <button key={rb.id} className={`runbooks-item ${current?.id === rb.id ? "on" : ""}`} onClick={() => setOpen(rb.id)}>
                <b>{rb.title}</b>
                <span className="faint small">{rb.targets.map(targetLine).join("; ") || "everything"}</span>
              </button>
            ))}
          </div>
          <div className="runbooks-view">{current && <RunbookView runbook={current} ctx={ctx} onEdit={() => openDialog({ kind: "runbook", id: current.id })} />}</div>
        </div>
      )}
      {menu && (
        <ContextMenu
          x={menu.x}
          y={menu.y}
          onClose={() => setMenu(null)}
          items={[
            { label: "Show in Finder", run: () => Runbooks.reveal().catch((e) => toast.error("Could not open the folder", e)) },
            {
              label: "Choose another folder…",
              run: async () => {
                try {
                  setFolder(await Runbooks.chooseFolder());
                  bump();
                } catch (e) {
                  toast.error("Could not use the folder", e);
                }
              },
            },
            ...(folder && !folder.default
              ? [
                  {
                    label: "Use the app's own folder",
                    run: async () => {
                      setFolder(await Runbooks.setFolder(""));
                      bump();
                    },
                  },
                ]
              : []),
          ]}
        />
      )}
    </div>
  );
}

const emptyTarget: RunbookTarget = { cluster: "", namespace: "", kind: "", name: "" };

/** RunbookEditor writes a runbook, new or existing. */
export function RunbookEditor({ id, target, onClose }: { id?: string; target?: RunbookTarget; onClose(): void }) {
  const bump = useRunbookVersion((s) => s.bump);
  const [rb, setRb] = useState<Runbook | null>(id ? null : { id: "", title: "", targets: [{ ...emptyTarget, ...target }], updated: "", body: starter });
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [preview, setPreview] = useState(false);
  const [insert, setInsert] = useState<{ x: number; y: number } | null>(null);
  const area = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    if (id) Runbooks.get(id).then((r) => setRb({ ...r, targets: r.targets.length ? r.targets : [{ ...emptyTarget }] }), (e) => setError(errorMessage(e)));
  }, [id]);

  const ctx = useMemo<RunbookContext>(() => {
    const s = useApp.getState();
    const t = rb?.targets[0];
    return { cluster: s.active ?? "", namespace: t?.namespace && !t.namespace.includes("*") ? t.namespace : "", kind: t?.kind || undefined, name: t?.name && !t.name.includes("*") ? t.name : undefined };
  }, [rb?.targets]);

  if (!rb)
    return (
      <Modal title="Runbook" onClose={onClose} width={760}>
        {error ? <ErrorBox message={error} /> : <Spinner />}
      </Modal>
    );

  const setTarget = (i: number, k: keyof RunbookTarget, value: string) => setRb({ ...rb, targets: rb.targets.map((t, j) => (j === i ? { ...t, [k]: value } : t)) });

  const insertAction = (action: string) => {
    const el = area.current;
    const def = runbookActions.find((a) => a.action === action)!;
    const snippet = action === "ask" ? `[Ask the assistant](kubedesk:ask?q=What%20is%20wrong%3F)` : action === "open" ? `[Open](kubedesk:open?kind=Deployment&name=NAME)` : `[${def.label}](kubedesk:${action})`;
    const at = el?.selectionStart ?? rb.body.length;
    const body = rb.body.slice(0, at) + snippet + rb.body.slice(el?.selectionEnd ?? at);
    setRb({ ...rb, body });
    setTimeout(() => {
      el?.focus();
      el?.setSelectionRange(at + snippet.length, at + snippet.length);
    }, 0);
  };

  const save = async () => {
    setBusy(true);
    setError("");
    try {
      await Runbooks.save(rb);
      bump();
      toast.ok("Runbook saved");
      onClose();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    if (!(await confirmAction(`Delete the runbook “${rb.title}”?`, `Its file, ${rb.id}, is deleted from the folder.`))) return;
    try {
      await Runbooks.delete(rb.id);
      bump();
      onClose();
    } catch (e) {
      setError(errorMessage(e));
    }
  };

  return (
    <Modal
      title={
        <span className="row gap">
          <BookOpen size={17} /> {rb.id ? "Edit runbook" : "New runbook"}
        </span>
      }
      onClose={onClose}
      width={800}
      footer={
        <>
          {rb.id && (
            <Button variant="danger" icon={<Trash2 size={14} />} onClick={remove}>
              Delete
            </Button>
          )}
          <div className="spacer" />
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" busy={busy} onClick={save} disabled={!rb.title.trim()}>
            Save
          </Button>
        </>
      }
    >
      <div className="add-body">
        <Field label="Title">
          <input value={rb.title} onChange={(e) => setRb({ ...rb, title: e.target.value })} placeholder="When ledger-sync crash loops" autoFocus />
        </Field>
        <Field label="For" hint="Kube Desk shows the runbook with what matches. Empty matches anything; * is a wildcard. The cluster is its name or context, which teammates share.">
          <div className="runbook-target-rows">
            {rb.targets.map((t, i) => (
              <div key={i} className="runbook-target-row">
                <input value={t.cluster ?? ""} onChange={(e) => setTarget(i, "cluster", e.target.value)} placeholder="any cluster" className="mono" spellCheck={false} />
                <input value={t.namespace ?? ""} onChange={(e) => setTarget(i, "namespace", e.target.value)} placeholder="any namespace" className="mono" spellCheck={false} />
                <input value={t.kind ?? ""} onChange={(e) => setTarget(i, "kind", e.target.value)} placeholder="any kind" className="mono" spellCheck={false} />
                <input value={t.name ?? ""} onChange={(e) => setTarget(i, "name", e.target.value)} placeholder="any name" className="mono" spellCheck={false} />
                <IconButton label="Remove" onClick={() => setRb({ ...rb, targets: rb.targets.filter((_, j) => j !== i) })} disabled={rb.targets.length === 1}>
                  <X size={14} />
                </IconButton>
              </div>
            ))}
            <button className="link small" style={{ alignSelf: "flex-start" }} onClick={() => setRb({ ...rb, targets: [...rb.targets, { ...emptyTarget }] })}>
              + Another target
            </button>
          </div>
        </Field>
        <div className="runbook-editor-bar">
          <div className="segmented">
            <button className={!preview ? "on" : ""} onClick={() => setPreview(false)}>
              Write
            </button>
            <button className={preview ? "on" : ""} onClick={() => setPreview(true)}>
              Preview
            </button>
          </div>
          <div className="spacer" />
          {!preview && (
            <Button size="sm" variant="ghost" icon={<Plus size={13} />} onClick={(e) => setInsert({ x: e.clientX - 200, y: e.clientY + 12 })}>
              Insert an action
            </Button>
          )}
        </div>
        {preview ? (
          <div className="runbook-preview">
            <Markdown className="md runbook-body" text={rb.body || "_Empty._"} onAction={(href) => runAction(href, ctx)} />
          </div>
        ) : (
          <textarea ref={area} className="runbook-text mono" value={rb.body} onChange={(e) => setRb({ ...rb, body: e.target.value })} spellCheck={false} rows={16} />
        )}
        <p className="faint small">Markdown. Action links, such as <code>[Restart](kubedesk:restart)</code>, become buttons that run on the object the runbook is read with, after the usual confirmation.</p>
        {error && <div className="form-error">{error}</div>}
      </div>
      {insert && <ContextMenu x={insert.x} y={insert.y} onClose={() => setInsert(null)} items={runbookActions.map((a) => ({ label: `${a.label} — ${a.hint}`, run: () => insertAction(a.action) }))} />}
    </Modal>
  );
}

const starter = `## Check

1. Open the logs, also of the run before the last restart: [Logs](kubedesk:logs)
2. See what changed lately: [History](kubedesk:history)

## Fix

- If it is stuck, restart it: [Restart](kubedesk:restart)
- Otherwise, [gather an incident report](kubedesk:report) and post it in #incidents.
`;
