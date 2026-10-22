import { useCallback, useEffect, useRef, useState } from "react";
import { ArrowLeft, Copy, MoreHorizontal, RefreshCw, Save, X } from "lucide-react";
import { Kube } from "../../mygo";
import { age, errorMessage, selectorOf, type KObj } from "../../lib/k8s";
import { eventTime } from "../../lib/resources";
import { objectActions } from "../../lib/objectActions";
import { confirmChange } from "../../lib/actions";
import { toast, useActiveView, useApp, type DetailRef } from "../../lib/store";
import { useNow, useWatch } from "../../lib/watch";
import { Badge, Button, Empty, ErrorBox, IconButton, Spinner, Tabs } from "../../components/ui";
import { ContextMenu } from "../../components/Menu";
import { YamlEditor } from "../../components/YamlEditor";
import { LogsView, type LogSource } from "../../components/LogsView";
import { TerminalView } from "../../components/TerminalView";
import { Overview } from "./Overview";
import { ObjectHistory } from "../ChangesView";
import { copy } from "./parts";

const WIDTH_KEY = "details-width";

function savedWidth() {
  try {
    return Number(localStorage.getItem(WIDTH_KEY)) || 0;
  } catch {
    return 0;
  }
}

export function DetailsPanel({ cluster }: { cluster: string }) {
  const view = useActiveView()!;
  const ref = view.details[view.details.length - 1]!;
  const [width, setWidth] = useState(() => savedWidth() || Math.round(window.innerWidth * 0.48));
  const dragging = useRef(false);

  const onDrag = (e: React.MouseEvent) => {
    e.preventDefault();
    dragging.current = true;
    const startX = e.clientX;
    const startW = width;
    let w = startW;
    const move = (ev: MouseEvent) => {
      w = Math.max(420, Math.min(window.innerWidth - 360, startW + (startX - ev.clientX)));
      setWidth(w);
    };
    const up = () => {
      dragging.current = false;
      window.removeEventListener("mousemove", move);
      window.removeEventListener("mouseup", up);
      try {
        localStorage.setItem(WIDTH_KEY, String(w));
      } catch {
        /* storage unavailable */
      }
    };
    window.addEventListener("mousemove", move);
    window.addEventListener("mouseup", up);
  };

  return (
    <aside className="details" style={{ width }}>
      <div className="details-resizer" onMouseDown={onDrag} />
      <DetailsContent key={`${ref.kind}/${ref.namespace}/${ref.name}`} cluster={cluster} detail={ref} depth={view.details.length} watchNamespace={view.namespace} />
    </aside>
  );
}

function DetailsContent({ cluster, detail, depth, watchNamespace }: { cluster: string; detail: DetailRef; depth: number; watchNamespace: string }) {
  const { backDetail, closeDetail, setDetailTab } = useApp();
  const ns = detail.namespace ? (watchNamespace === "" ? "" : detail.namespace) : "";
  const { items, loading, error } = useWatch(cluster, detail.gvr, ns);
  const obj = items.find((o) => o.metadata.name === detail.name && (o.metadata.namespace ?? "") === detail.namespace);
  const [menu, setMenu] = useState<{ x: number; y: number } | null>(null);
  const isPod = detail.kind === "Pod";
  const ro = useApp((s) => !!s.clusters.find((c) => c.id === cluster)?.readOnly);
  const [tabId, container] = (detail.tab ?? "overview").split(":") as [string, string | undefined];

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || document.querySelector(".modal-backdrop, .menu")) return;
      const t = e.target as HTMLElement;
      if (t.closest(".cm-editor, .xterm, input, textarea, select")) return;
      closeDetail();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [closeDetail]);

  const tabs = [
    { id: "overview", label: "Overview" },
    ...(isPod ? [{ id: "logs", label: "Logs" }, ...(ro ? [] : [{ id: "terminal", label: "Shell" }])] : []),
    ...(!isPod && obj && workloadLogSource(detail.kind, obj) ? [{ id: "logs", label: "Logs" }] : []),
    { id: "yaml", label: "YAML" },
    ...(detail.kind !== "Event" ? [{ id: "events", label: "Events" }] : []),
    ...(detail.kind !== "Event" ? [{ id: "history", label: "History" }] : []),
  ];

  return (
    <>
      <header className="details-header">
        {depth > 1 && (
          <IconButton label="Back" onClick={backDetail}>
            <ArrowLeft size={16} />
          </IconButton>
        )}
        <div className="details-title">
          <div className="details-kind">
            <Badge tone="info">{detail.kind}</Badge>
            {detail.namespace && <span className="muted small">{detail.namespace}</span>}
            {obj?.metadata.deletionTimestamp && <Badge tone="warn">Terminating</Badge>}
          </div>
          <h2 title={detail.name}>
            {detail.name}
            <button className="copy-btn" title="Copy name" onClick={() => copy(detail.name, "Name copied")}>
              <Copy size={13} />
            </button>
          </h2>
        </div>
        <div className="details-actions">
          {obj &&
            objectActions(cluster, detail.gvr, detail.kind, obj, { inDetails: true })
              .filter((a) => !a.danger)
              .slice(0, 3)
              .map((a) => (
                <Button key={a.label} size="sm" variant="ghost" icon={a.icon} onClick={a.run}>
                  {a.label.replace("…", "")}
                </Button>
              ))}
          {obj && (
            <IconButton label="More actions" onClick={(e) => setMenu({ x: e.clientX - 160, y: e.clientY + 12 })}>
              <MoreHorizontal size={16} />
            </IconButton>
          )}
          <IconButton label="Close (Esc)" onClick={closeDetail}>
            <X size={16} />
          </IconButton>
        </div>
      </header>
      <Tabs tabs={tabs} value={tabId} onChange={(t) => setDetailTab(t)} />
      <div className={`details-body ${tabId === "logs" || tabId === "terminal" || tabId === "yaml" ? "fill" : ""}`}>
        {!obj ? (
          loading ? (
            <div className="center-pad">
              <Spinner />
            </div>
          ) : error ? (
            <ErrorBox message={error} />
          ) : (
            <Empty title="Not found">
              {detail.kind} {detail.name} no longer exists{detail.namespace ? ` in ${detail.namespace}` : ""}.
            </Empty>
          )
        ) : tabId === "overview" ? (
          <Overview cluster={cluster} kind={detail.kind} o={obj} />
        ) : tabId === "logs" && isPod ? (
          <LogsView cluster={cluster} pod={obj} initialContainer={container} />
        ) : tabId === "logs" && workloadLogSource(detail.kind, obj) ? (
          <LogsView cluster={cluster} source={workloadLogSource(detail.kind, obj)!} />
        ) : tabId === "terminal" && isPod && !ro ? (
          <TerminalView cluster={cluster} pod={obj} initialContainer={container} />
        ) : tabId === "yaml" ? (
          <YamlTab cluster={cluster} detail={detail} obj={obj} readOnly={ro} />
        ) : tabId === "history" ? (
          <ObjectHistory cluster={cluster} kind={detail.kind} namespace={detail.namespace} name={detail.name} />
        ) : (
          <ObjectEvents cluster={cluster} obj={obj} />
        )}
      </div>
      {menu && obj && <ContextMenu x={menu.x} y={menu.y} items={objectActions(cluster, detail.gvr, detail.kind, obj, { inDetails: true })} onClose={() => setMenu(null)} />}
    </>
  );
}

const logKinds = new Set(["Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job", "Service"]);

/** workloadLogSource is the pods of a workload, for their logs together. */
function workloadLogSource(kind: string, o: KObj): LogSource | null {
  if (!logKinds.has(kind)) return null;
  const selector = kind === "Service" ? (o.spec?.selector as Record<string, string> | undefined) : selectorOf(o);
  if (!selector || !Object.keys(selector).length) return null;
  return {
    kind: "workload",
    name: o.metadata.name,
    namespace: o.metadata.namespace ?? "",
    selector,
    containers: (o.spec?.template?.spec?.containers ?? []).map((c: any) => c.name),
  };
}

function YamlTab({ cluster, detail, obj, readOnly }: { cluster: string; detail: DetailRef; obj: KObj; readOnly: boolean }) {
  const [original, setOriginal] = useState<string | null>(null);
  const [text, setText] = useState("");
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const [stale, setStale] = useState(false);
  const loadedVersion = useRef<string | undefined>(undefined);
  const dirty = original !== null && text !== original;

  const load = useCallback(() => {
    Kube.getYAML(cluster, detail.gvr, detail.namespace, detail.name)
      .then((y) => {
        setOriginal(y);
        setText(y);
        setError("");
        setStale(false);
        loadedVersion.current = /resourceVersion: "?(\d+)"?/.exec(y)?.[1];
      })
      .catch((e) => setError(errorMessage(e)));
  }, [cluster, detail.gvr, detail.namespace, detail.name]);

  useEffect(load, [load]);

  // Follow changes made elsewhere while there is nothing unsaved.
  useEffect(() => {
    const rv = obj.metadata.resourceVersion;
    if (!loadedVersion.current || rv === loadedVersion.current) return;
    if (dirty) setStale(true);
    else load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [obj.metadata.resourceVersion]);

  const save = async () => {
    if (!dirty || saving || readOnly) return;
    if (!(await confirmChange(cluster, `Save ${detail.kind} ${detail.name}?`, "The object is replaced with the YAML you edited."))) return;
    setSaving(true);
    try {
      const res = await Kube.apply(cluster, text, detail.namespace);
      toast.ok("Saved", res.join("\n"));
      load();
    } catch (e) {
      toast.error("Could not save", e);
    } finally {
      setSaving(false);
    }
  };

  if (error) return <ErrorBox message={error} action={<Button onClick={load}>Retry</Button>} />;
  if (original === null)
    return (
      <div className="center-pad">
        <Spinner />
      </div>
    );
  return (
    <div className="yaml-tab">
      <div className="yaml-toolbar">
        {readOnly ? (
          <span className="faint small">Read-only cluster: the YAML cannot be changed here</span>
        ) : stale ? (
          <span className="text-warn small">The object changed in the cluster since you started editing.</span>
        ) : (
          <span className="faint small">{dirty ? "Unsaved changes" : "Edit and save to apply to the cluster"}</span>
        )}
        <div className="spacer" />
        <Button size="sm" variant="ghost" icon={<RefreshCw size={14} />} onClick={load} disabled={saving}>
          {dirty ? "Discard" : "Reload"}
        </Button>
        <Button size="sm" variant="ghost" icon={<Copy size={14} />} onClick={() => copy(text, "YAML copied")}>
          Copy
        </Button>
        {!readOnly && (
          <Button size="sm" variant="primary" icon={<Save size={14} />} onClick={save} disabled={!dirty} busy={saving}>
            Save
          </Button>
        )}
      </div>
      <YamlEditor value={text} onChange={setText} onSave={save} readOnly={readOnly} />
    </div>
  );
}

function ObjectEvents({ cluster, obj }: { cluster: string; obj: KObj }) {
  const now = useNow(5000);
  const { items, loading } = useWatch(cluster, { group: "", version: "v1", resource: "events" }, obj.metadata.namespace ?? "");
  const events = items.filter((e) => e.involvedObject?.uid === obj.metadata.uid || (e.involvedObject?.name === obj.metadata.name && e.involvedObject?.kind === obj.kind)).sort((a, b) => eventTime(b) - eventTime(a));
  if (loading)
    return (
      <div className="center-pad">
        <Spinner />
      </div>
    );
  if (!events.length) return <Empty title="No events">Events are kept for about an hour.</Empty>;
  return (
    <div className="event-list">
      {events.map((e) => (
        <div key={e.metadata.uid} className={`event ${e.type === "Warning" ? "warning" : ""}`}>
          <div className="event-head">
            <Badge tone={e.type === "Warning" ? "warn" : "muted"}>{e.reason}</Badge>
            <span className="faint small">
              {age(new Date(eventTime(e)).toISOString(), now)} ago{(e.count ?? 1) > 1 ? ` · ×${e.count}` : ""} · {e.source?.component ?? e.reportingComponent}
            </span>
          </div>
          <div className="event-message selectable">{e.message}</div>
        </div>
      ))}
    </div>
  );
}
