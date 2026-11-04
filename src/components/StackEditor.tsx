import { useEffect, useState } from "react";
import { CheckCircle2, FileCode2, FolderOpen } from "lucide-react";
import { Stacks, type Stack } from "../mygo";
import { errorMessage } from "../lib/k8s";
import { run, toast } from "../lib/store";
import { newStackTemplate, useStacks } from "../lib/stacks";
import { Button, ErrorBox, Modal, Spinner } from "./ui";
import { YamlEditor } from "./YamlEditor";

/**
 * StackEditor writes the user's stacks: a new one, one of theirs, or a
 * copy; a built-in stack it only shows.
 */
export function StackEditor({ id, source, readOnly, onClose }: { id?: string; source?: string; readOnly?: boolean; onClose(): void }) {
  const loadCatalog = useStacks((s) => s.loadCatalog);
  const [text, setText] = useState<string | null>(source ?? (id ? null : newStackTemplate));
  const [checked, setChecked] = useState<Stack | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (id && source === undefined) Stacks.source(id).then(setText, (e) => setError(errorMessage(e)));
  }, [id, source]);

  // The stack is checked as it is written.
  useEffect(() => {
    if (text === null || readOnly) return;
    const t = setTimeout(() => {
      Stacks.check(text).then(
        (st) => {
          setChecked(st);
          setError("");
        },
        (e) => {
          setChecked(null);
          setError(errorMessage(e));
        },
      );
    }, 400);
    return () => clearTimeout(t);
  }, [text, readOnly]);

  const save = async () => {
    if (text === null || readOnly) return;
    setBusy(true);
    try {
      const st = await Stacks.save(text, id ?? "");
      toast.ok(`Saved ${st.name}`, "It is in your stacks, ready to run.");
      await loadCatalog();
      onClose();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const title = readOnly ? "Stack definition" : id ? "Edit stack" : "New stack";
  return (
    <Modal
      title={
        <span className="row gap">
          <FileCode2 size={17} /> {title}
        </span>
      }
      onClose={onClose}
      width={880}
      className="stack-editor"
      footer={
        <>
          <Button variant="ghost" icon={<FolderOpen size={14} />} onClick={() => run("Could not open the folder", () => Stacks.reveal())}>
            Your stacks folder
          </Button>
          <div className="spacer" />
          {readOnly ? (
            <Button variant="primary" onClick={onClose}>
              Close
            </Button>
          ) : (
            <>
              <Button variant="ghost" onClick={onClose}>
                Cancel
              </Button>
              <Button variant="primary" busy={busy} disabled={!checked} onClick={save}>
                Save
              </Button>
            </>
          )}
        </>
      }
    >
      <div className="stack-editor-body">
        {readOnly ? (
          <div className="faint small">A built-in stack. To change it, duplicate it as your own: a stack of yours with the same id replaces the built-in one.</div>
        ) : error ? (
          <ErrorBox title="Not a valid stack yet" message={error} />
        ) : checked ? (
          <div className="stack-check small">
            <CheckCircle2 size={14} className="tc tone-ok" />
            <span>
              <b>{checked.name}</b> · {checked.components.length} component{checked.components.length === 1 ? "" : "s"} · {checked.fields?.length ?? 0} setting{checked.fields?.length === 1 ? "" : "s"} · namespace{" "}
              <span className="mono">{checked.namespace}</span>
              {checked.needsHelm ? " · needs helm" : ""}
            </span>
          </div>
        ) : (
          <div className="faint small">Checking…</div>
        )}
        <div className="stack-editor-yaml">{text === null ? <Spinner /> : <YamlEditor value={text} onChange={readOnly ? undefined : setText} readOnly={readOnly} onSave={save} />}</div>
      </div>
    </Modal>
  );
}
