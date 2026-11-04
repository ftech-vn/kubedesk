import { useEffect, useRef, useState } from "react";
import { Channel } from "mygo-runtime";
import { CheckCircle2, ChevronDown, ChevronRight, Circle, Dices, Eye, EyeOff, Info, Loader2, MinusCircle, Play, RotateCcw, TriangleAlert, XCircle } from "lucide-react";
import { Stacks, type PlannedComponent, type Stack, type StackField, type StackJob, type StackPlan } from "../mygo";
import { age, errorMessage, podStatus } from "../lib/k8s";
import { typeToConfirm } from "../lib/actions";
import { byKey } from "../lib/resources";
import { ask, useApp } from "../lib/store";
import { useStacks } from "../lib/stacks";
import { useNow, useWatch } from "../lib/watch";
import { AccessButtons, stackIcon } from "../views/StacksView";
import { Badge, Button, ErrorBox, Field, Modal, Spinner } from "./ui";
import { ClusterTile } from "./ClusterRail";
import { Markdown } from "./Markdown";
import { YamlEditor } from "./YamlEditor";

/** StackDialog asks for a stack's settings, runs it, and follows the run. */
export function StackDialog({ cluster, stack, namespace, job, onClose }: { cluster: string; stack: string; namespace?: string; job?: string; onClose(): void }) {
  const catalog = useStacks((s) => s.catalog);
  const loadCatalog = useStacks((s) => s.loadCatalog);
  const st = catalog?.stacks.find((s) => s.id === stack);
  const [jobID, setJobID] = useState(job);
  useEffect(() => {
    if (!catalog) loadCatalog();
  }, [catalog, loadCatalog]);
  // The open job is not told of in a toast.
  useEffect(() => {
    if (jobID && jobID !== job) useApp.setState((s) => (s.dialog?.kind === "stack" ? { dialog: { ...s.dialog, job: jobID } } : {}));
  }, [jobID, job]);

  if (jobID) return <JobView cluster={cluster} stack={st} id={jobID} onClose={onClose} />;
  if (!catalog)
    return (
      <Modal title="Stack" onClose={onClose}>
        <Spinner />
      </Modal>
    );
  if (!st)
    return (
      <Modal title="Stack" onClose={onClose}>
        <ErrorBox message={`There is no stack ${stack} any more.`} />
      </Modal>
    );
  return <Configure cluster={cluster} st={st} initialNamespace={namespace ?? st.namespace} onStarted={setJobID} onClose={onClose} />;
}

function Configure({ cluster, st, initialNamespace, onStarted, onClose }: { cluster: string; st: Stack; initialNamespace: string; onStarted(id: string): void; onClose(): void }) {
  const cl = useApp((s) => s.clusters.find((c) => c.id === cluster));
  const [namespace, setNamespace] = useState(initialNamespace);
  const [values, setValues] = useState<Record<string, string> | null>(null);
  const [overrides, setOverrides] = useState<Record<string, string>>({});
  const [plan, setPlan] = useState<StackPlan | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [advanced, setAdvanced] = useState(false);
  const [open, setOpen] = useState<string | null>(null);
  const loadedFrom = useRef<string | null>(null);
  const now = useNow(30000);

  const key = JSON.stringify([namespace, values, overrides]);
  useEffect(() => {
    let alive = true;
    const t = setTimeout(
      () => {
        Stacks.plan(cluster, { stack: st.id, namespace, values: values ?? {}, overrides }).then(
          (p) => {
            if (!alive) return;
            setPlan(p);
            setError("");
            // The first plan fills in the defaults, generated passwords and
            // the settings of the stack already there.
            if (values === null) {
              setValues(p.values);
              loadedFrom.current = p.installed?.namespace ?? null;
            }
          },
          (e) => alive && setError(errorMessage(e)),
        );
      },
      values === null ? 0 : 300,
    );
    return () => {
      alive = false;
      clearTimeout(t);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, cluster, st.id]);

  // Moving to a namespace with the stack in it brings back its settings.
  useEffect(() => {
    const ns = plan?.installed?.namespace;
    if (ns && loadedFrom.current !== ns) {
      loadedFrom.current = ns;
      setValues(null);
    }
  }, [plan]);

  const set = (k: string, v: string) => setValues((cur) => ({ ...(cur ?? {}), [k]: v }));
  const fieldErrors = plan?.fieldErrors ?? {};
  const blocking = !plan || Object.keys(fieldErrors).length > 0 || plan.helmMissing || plan.components.some((c) => c.error);
  const basic = st.fields?.filter((f) => !f.advanced) ?? [];
  const extra = st.fields?.filter((f) => f.advanced) ?? [];

  const start = async () => {
    if (!plan || blocking || !cl) return;
    const name = typeToConfirm(cluster);
    if (name) {
      const r = await ask({ title: `Run ${st.name} in production?`, message: `${plan.components.filter((c) => !c.skipped).length} components are installed into ${namespace} of ${cl.name}.`, confirmLabel: "Run", danger: true, typeToConfirm: name });
      if (!r.ok) return;
    }
    setBusy(true);
    try {
      onStarted(await Stacks.install(cluster, { stack: st.id, namespace, values: values ?? {}, overrides }));
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
          {stackIcon(st.category, 17)} {plan?.installed ? `Update ${st.name}` : `Run ${st.name}`}
        </span>
      }
      onClose={onClose}
      width={860}
      className="stack-dialog"
      footer={
        <>
          {cl && (
            <span className="row gap-sm small muted">
              <ClusterTile c={cl} size={18} /> into <b>{cl.name}</b> / <span className="mono">{namespace}</span>
            </span>
          )}
          <div className="spacer" />
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" icon={<Play size={14} />} busy={busy} disabled={blocking || !!cl?.readOnly} onClick={start} title={cl?.readOnly ? `${cl.name} is read-only` : undefined}>
            {plan?.installed ? "Update" : "Run"}
          </Button>
        </>
      }
    >
      <div className="stack-config">
        <p className="muted">{st.description}</p>
        {st.requires && <p className="faint small">Needs: {st.requires}</p>}
        {plan?.installed && (
          <div className="hint-bar inline">
            <Info size={15} />
            <span>
              {st.name} is already in <b>{namespace}</b>, {plan.installed.status}, updated {age(plan.installed.updated, now)} ago. Running it again updates it with these settings.
            </span>
          </div>
        )}
        {plan?.helmMissing && <ErrorBox title="The helm command is not installed" message="This stack installs Helm charts. Install Helm (brew install helm), then try again." />}
        {plan?.warnings.map((w) => (
          <div key={w} className="hint-bar inline warn">
            <TriangleAlert size={15} />
            <span>{w}</span>
          </div>
        ))}
        {error && <ErrorBox message={error} />}

        <div className="stack-form">
          <Field label="Namespace" hint={fieldErrors.namespace ?? "Made if it is missing."}>
            <input className={`mono ${fieldErrors.namespace ? "invalid" : ""}`} value={namespace} onChange={(e) => setNamespace(e.target.value.trim())} spellCheck={false} />
          </Field>
          {values && basic.map((f) => <FieldInput key={f.key} f={f} value={values[f.key] ?? ""} error={fieldErrors[f.key]} onChange={(v) => set(f.key, v)} />)}
        </div>
        {extra.length > 0 && values && (
          <>
            <button className="disclosure" onClick={() => setAdvanced(!advanced)}>
              {advanced ? <ChevronDown size={13} /> : <ChevronRight size={13} />} More settings
            </button>
            {advanced && (
              <div className="stack-form">
                {extra.map((f) => (
                  <FieldInput key={f.key} f={f} value={values[f.key] ?? ""} error={fieldErrors[f.key]} onChange={(v) => set(f.key, v)} />
                ))}
              </div>
            )}
          </>
        )}

        <div className="section-title">Components</div>
        <div className="stack-components">
          {(plan?.components ?? []).map((c, i) => (
            <ComponentRow
              key={c.name}
              index={i + 1}
              c={c}
              namespace={namespace}
              open={open === c.name}
              onToggle={() => setOpen(open === c.name ? null : c.name)}
              onEdit={(v) => setOverrides((o) => ({ ...o, [c.name]: v }))}
              onReset={() =>
                setOverrides((o) => {
                  const next = { ...o };
                  delete next[c.name];
                  return next;
                })
              }
            />
          ))}
          {!plan && <Spinner />}
        </div>
      </div>
    </Modal>
  );
}

function FieldInput({ f, value, error, onChange }: { f: StackField; value: string; error?: string; onChange(v: string): void }) {
  const [shown, setShown] = useState(false);
  const hint = error ? <span className="tc tone-bad">{error}</span> : f.hint;
  if (f.type === "bool")
    return (
      <label className="check field-check" title={f.hint}>
        <input type="checkbox" checked={value === "true"} onChange={(e) => onChange(String(e.target.checked))} />
        <span>
          {f.label}
          {f.hint && <span className="field-hint">{f.hint}</span>}
        </span>
      </label>
    );
  if (f.type === "select")
    return (
      <Field label={f.label} hint={hint}>
        <select value={value} onChange={(e) => onChange(e.target.value)}>
          {f.options?.map((o) => (
            <option key={o}>{o}</option>
          ))}
        </select>
      </Field>
    );
  if (f.type === "password")
    return (
      <Field label={f.label} hint={hint}>
        <div className="input-with-buttons">
          <input className={`mono ${error ? "invalid" : ""}`} type={shown ? "text" : "password"} value={value} onChange={(e) => onChange(e.target.value)} spellCheck={false} autoComplete="off" />
          <button type="button" className="icon-btn" title={shown ? "Hide" : "Show"} onClick={() => setShown(!shown)}>
            {shown ? <EyeOff size={14} /> : <Eye size={14} />}
          </button>
          {f.generate && (
            <button type="button" className="icon-btn" title="Make a new password" onClick={() => onChange("")}>
              <Dices size={14} />
            </button>
          )}
        </div>
      </Field>
    );
  return (
    <Field label={f.label} hint={hint}>
      <input className={error ? "invalid" : ""} type={f.type === "number" ? "number" : "text"} value={value} onChange={(e) => onChange(e.target.value)} spellCheck={false} />
    </Field>
  );
}

const kindLabel = { helm: "Helm chart", urls: "Manifests from URLs", manifest: "Manifest" } as Record<string, string>;

function ComponentRow({ index, c, namespace, open, onToggle, onEdit, onReset }: { index: number; c: PlannedComponent; namespace: string; open: boolean; onToggle(): void; onEdit(v: string): void; onReset(): void }) {
  return (
    <div className={`stack-component ${c.skipped ? "skipped" : ""}`}>
      <button className="stack-component-head" onClick={onToggle} disabled={c.skipped}>
        <span className="step-num">{index}</span>
        <div className="stack-component-main">
          <div className="row gap-sm">
            <b>{c.title}</b>
            <Badge tone="muted">{kindLabel[c.kind]}</Badge>
            {c.namespace !== namespace && <span className="mono small faint">in {c.namespace}</span>}
            {c.skipped && <Badge tone="muted">off</Badge>}
            {c.overridden && <Badge tone="warn">edited</Badge>}
            {c.error && <Badge tone="bad">error</Badge>}
          </div>
          {c.kind !== "manifest" && <div className="mono small faint stack-source">{c.source}</div>}
        </div>
        {!c.skipped && (open ? <ChevronDown size={14} /> : <ChevronRight size={14} />)}
      </button>
      {open && !c.skipped && (
        <div className="stack-component-body">
          {c.error && <ErrorBox message={c.error} />}
          {c.kind === "urls" ? (
            <div className="small muted">Kube Desk downloads these and applies what they hold. Objects without a namespace go into {c.namespace}.</div>
          ) : (
            <>
              <div className="yaml-toolbar">
                <span className="faint small">{c.kind === "helm" ? "The chart's values, as they will be given." : "What will be applied."} Edit to change them for this run.</span>
                {c.overridden && (
                  <Button size="sm" variant="ghost" icon={<RotateCcw size={13} />} onClick={onReset}>
                    Back to the settings
                  </Button>
                )}
              </div>
              <div className="stack-yaml">
                <YamlEditor value={c.content || "# The chart's defaults.\n"} onChange={onEdit} />
              </div>
            </>
          )}
        </div>
      )}
    </div>
  );
}

const stepIcon = (status: string) =>
  status === "done" ? <CheckCircle2 size={16} className="tc tone-ok" /> : status === "running" ? <Loader2 size={16} className="spin tc tone-info" /> : status === "failed" ? <XCircle size={16} className="tc tone-bad" /> : status === "skipped" ? <MinusCircle size={16} className="faint" /> : <Circle size={16} className="faint" />;

/** JobView follows a stack being installed or removed. */
function JobView({ cluster, stack, id, onClose }: { cluster: string; stack?: Stack; id: string; onClose(): void }) {
  const [job, setJob] = useState<StackJob | null>(null);
  const [error, setError] = useState("");
  const [showOutput, setShowOutput] = useState(false);
  const out = useRef<HTMLPreElement>(null);
  const pods = useWatch(cluster, byKey.get("pods")!.gvr, job?.namespace ?? "");

  useEffect(() => {
    const ch = new Channel<StackJob>((j) => setJob(j));
    Stacks.follow(id, ch).catch((e) => setError(errorMessage(e)));
    return () => ch.close();
  }, [id]);
  useEffect(() => {
    if (job?.state === "failed") setShowOutput(true);
  }, [job?.state]);
  useEffect(() => {
    const el = out.current;
    if (el && el.scrollHeight - el.scrollTop - el.clientHeight < 80) el.scrollTop = el.scrollHeight;
  }, [job?.output?.length, showOutput]);

  const nsPods = job ? pods.items : [];
  const runningState = job?.state === "running";
  const title = job ? `${job.action === "install" ? "Installing" : "Uninstalling"} ${job.name}` : "Stack";

  return (
    <Modal
      title={
        <span className="row gap">
          {stack ? stackIcon(stack.category, 17) : null}
          {job?.state === "done" ? `${job.name} ${job.action === "install" ? "is running" : "was removed"}` : title}
        </span>
      }
      onClose={onClose}
      width={860}
      className="stack-dialog"
      footer={
        <>
          {runningState && <span className="faint small">Closing this keeps it going; it shows under Stacks.</span>}
          <div className="spacer" />
          {runningState && (
            <Button variant="ghost" onClick={() => Stacks.cancel(id)}>
              Stop
            </Button>
          )}
          {job && job.state !== "running" && job.action === "install" && (
            <Button
              variant="ghost"
              onClick={() => {
                const s = useApp.getState();
                s.setNamespace(job.namespace);
                s.setSection("pods");
                onClose();
              }}
            >
              Show its pods
            </Button>
          )}
          <Button variant={runningState ? "default" : "primary"} onClick={onClose}>
            Close
          </Button>
        </>
      }
    >
      {error ? (
        <ErrorBox message={error} />
      ) : !job ? (
        <Spinner />
      ) : (
        <div className="stack-job">
          {job.state === "failed" && <ErrorBox title={job.action === "install" ? "The stack could not be installed" : "The stack could not be removed"} message={job.error ?? ""} />}
          {job.state === "cancelled" && <div className="hint-bar inline warn">Stopped. What the finished steps did stays; run the stack again to go on.</div>}
          <div className="stack-job-grid">
            <ol className="stack-steps">
              {job.steps.map((s) => (
                <li key={s.name} className={`step-${s.status}`}>
                  {stepIcon(s.status)}
                  <div>
                    <div>{s.title}</div>
                    {s.detail && <div className="small tc tone-bad stack-step-detail">{s.detail}</div>}
                  </div>
                </li>
              ))}
            </ol>
            {job.action === "install" && (
              <div className="stack-pods">
                <div className="section-title">
                  Pods in {job.namespace} <span className="count">{nsPods.length}</span>
                </div>
                {nsPods.length === 0 ? (
                  <div className="faint small">None yet.</div>
                ) : (
                  nsPods.slice(0, 14).map((p) => {
                    const ps = podStatus(p);
                    return (
                      <div key={p.metadata.name} className="stack-pod">
                        <span className={`status-dot tone-${ps.tone}`} />
                        <span className="mono small ellipsis">{p.metadata.name}</span>
                        <span className="faint small nowrap">
                          {ps.ready}/{ps.total} · {ps.reason}
                        </span>
                      </div>
                    );
                  })
                )}
                {nsPods.length > 14 && <div className="faint small">…and {nsPods.length - 14} more</div>}
              </div>
            )}
          </div>

          {job.state === "done" && job.action === "install" && stack && (
            <div className="stack-done">
              {stack.access && stack.access.length > 0 && (
                <div className="row gap wrap">
                  {stack.access.map((a, i) => (
                    <AccessButtons key={i} cluster={cluster} namespace={job.namespace} stack={job.stack} index={i} label={`Open ${a.label}`} hasPassword={!!(a.password || a.passwordSecret)} />
                  ))}
                </div>
              )}
              {job.notes && <Markdown text={job.notes} />}
            </div>
          )}

          <button className="disclosure" onClick={() => setShowOutput(!showOutput)}>
            {showOutput ? <ChevronDown size={13} /> : <ChevronRight size={13} />} Output ({job.output?.length ?? 0} lines)
          </button>
          {showOutput && (
            <pre ref={out} className="stack-output selectable">
              {(job.output ?? []).join("\n") || "Nothing yet."}
            </pre>
          )}
        </div>
      )}
    </Modal>
  );
}
