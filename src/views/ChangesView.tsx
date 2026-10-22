import { useEffect, useMemo, useState } from "react";
import { AlertTriangle, ChevronDown, ChevronRight, CirclePlus, Clock3, History, Moon, Pencil, Search, Trash2, Zap } from "lucide-react";
import { Alerts, events, type Change, type TimelineQuery } from "../mygo";
import { byKind } from "../lib/resources";
import { findDef } from "../lib/discovery";
import { errorMessage } from "../lib/k8s";
import { isWatched, toggleWatch, useAlerts } from "../lib/alerts";
import { useActiveView, useApp } from "../lib/store";
import { Badge, Button, Empty, ErrorBox, Spinner } from "../components/ui";
import { DiffView } from "../components/DiffView";

const ranges = [
  { id: "1h", label: "Last hour", ms: 3600e3 },
  { id: "6h", label: "Last 6 hours", ms: 6 * 3600e3 },
  { id: "24h", label: "Last 24 hours", ms: 24 * 3600e3 },
  { id: "7d", label: "Last 7 days", ms: 7 * 24 * 3600e3 },
];

/** useChanges reads the timeline, and again when it records more. */
export function useChanges(q: TimelineQuery | null) {
  const [changes, setChanges] = useState<Change[] | null>(null);
  const [error, setError] = useState("");
  const key = q ? JSON.stringify(q) : "";
  useEffect(() => {
    if (!q) return;
    let alive = true;
    let timer: ReturnType<typeof setTimeout> | null = null;
    const load = () =>
      Alerts.changes(q).then(
        (c) => alive && (setChanges(c), setError("")),
        (e) => alive && setError(errorMessage(e)),
      );
    load();
    const off = events.timelineChanged.on((cluster) => {
      if (cluster !== q.cluster || timer) return;
      timer = setTimeout(() => {
        timer = null;
        load();
      }, 400);
    });
    return () => {
      alive = false;
      off();
      if (timer) clearTimeout(timer);
    };
    // The key names the query.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);
  return { changes, error };
}

function sinceOf(ms: number) {
  return new Date(Date.now() - ms).toISOString().replace(/\.\d+Z$/, "Z");
}

export function ChangesView({ cluster }: { cluster: string }) {
  const view = useActiveView()!;
  const ns = view.namespace;
  const targets = useAlerts((s) => s.state.targets);
  const watchedHere = targets.filter((t) => t.cluster === cluster);
  const [range, setRange] = useState("24h");
  const [kind, setKind] = useState("");
  const [text, setText] = useState("");
  const [warningsOnly, setWarningsOnly] = useState(false);
  const [hideStatus, setHideStatus] = useState(false);
  const ms = ranges.find((r) => r.id === range)!.ms;
  // A minute's precision keeps the query stable between renders.
  const since = useMemo(() => sinceOf(ms), [ms, Math.floor(Date.now() / 60000)]);
  const { changes, error } = useChanges({ cluster, namespace: ns, since, limit: 2000 });

  const kinds = useMemo(() => [...new Set((changes ?? []).map((c) => c.kind))].sort(), [changes]);
  const shown = useMemo(() => {
    const t = text.toLowerCase();
    return (changes ?? []).filter(
      (c) =>
        (!kind || c.kind === kind) &&
        (!warningsOnly || c.warning) &&
        (!hideStatus || (c.type !== "status" && c.type !== "event")) &&
        (!t || `${c.kind} ${c.namespace} ${c.name} ${c.summary.join(" ")} ${c.actor ?? ""}`.toLowerCase().includes(t)),
    );
  }, [changes, kind, text, warningsOnly, hideStatus]);

  if (!watchedHere.length) {
    return (
      <Empty icon={<History size={30} />} title="The timeline records what you watch">
        <p>
          Watch a namespace, or the whole cluster, and Kube Desk records every change while it runs: what changed, who changed it,
          and a diff of before and after. It also finds what changed while it was closed.
        </p>
        <Button variant="primary" onClick={() => toggleWatch(cluster, ns)}>
          Watch {ns ? `namespace ${ns}` : "the whole cluster"}
        </Button>
      </Empty>
    );
  }

  const covered = watchedHere.some((t) => t.namespace === "" || t.namespace === ns);
  return (
    <div className="list-view">
      <header className="list-header">
        <div className="list-title">
          <History size={18} className="muted" />
          <h1>Changes</h1>
          <span className="count">{changes ? shown.length : <Spinner size={13} />}</span>
          <span className="scope">{ns ? `in ${ns}` : "all watched namespaces"}</span>
        </div>
        <div className="list-tools">
          <select className="select-sm" value={range} onChange={(e) => setRange(e.target.value)}>
            {ranges.map((r) => (
              <option key={r.id} value={r.id}>
                {r.label}
              </option>
            ))}
          </select>
          <select className="select-sm" value={kind} onChange={(e) => setKind(e.target.value)}>
            <option value="">All kinds</option>
            {kinds.map((k) => (
              <option key={k}>{k}</option>
            ))}
          </select>
          <label className="check">
            <input type="checkbox" checked={warningsOnly} onChange={(e) => setWarningsOnly(e.target.checked)} /> Problems only
          </label>
          <label className="check">
            <input type="checkbox" checked={hideStatus} onChange={(e) => setHideStatus(e.target.checked)} /> Edits only
          </label>
          <div className="search-input">
            <Search size={14} />
            <input value={text} onChange={(e) => setText(e.target.value)} placeholder="Filter: image, name, kubectl…" />
          </div>
        </div>
      </header>
      {!covered && ns && (
        <div className="list-warning">
          Namespace {ns} is not watched, so nothing of it is recorded.{" "}
          <button className="link small" onClick={() => toggleWatch(cluster, ns)}>
            Watch it
          </button>
        </div>
      )}
      {error ? (
        <ErrorBox message={error} />
      ) : !changes ? (
        <div className="center-pad">
          <Spinner />
        </div>
      ) : shown.length === 0 ? (
        <Empty icon={<Clock3 size={28} />} title="No changes">
          Nothing changed {ranges.find((r) => r.id === range)!.label.toLowerCase()}
          {kind || text || warningsOnly || hideStatus ? " that matches the filters" : ""}.
        </Empty>
      ) : (
        <div className="page timeline-page">
          <ChangeList cluster={cluster} changes={shown} showNamespace={!ns} />
        </div>
      )}
    </div>
  );
}

/** ChangeList shows changes by day, newest first. */
export function ChangeList({ cluster, changes, showNamespace = true, compact }: { cluster: string; changes: Change[]; showNamespace?: boolean; compact?: boolean }) {
  let day = "";
  return (
    <div className={`timeline ${compact ? "compact" : ""}`}>
      {changes.map((c) => {
        const d = new Date(c.time).toLocaleDateString(undefined, { weekday: "long", month: "short", day: "numeric" });
        const header = !compact && d !== day ? d : null;
        day = d;
        return (
          <div key={c.id}>
            {header && <div className="timeline-day">{header}</div>}
            <ChangeRow cluster={cluster} c={c} showNamespace={showNamespace} />
          </div>
        );
      })}
    </div>
  );
}

const typeIcon = (c: Change) => {
  if (c.warning) return <AlertTriangle size={14} />;
  switch (c.type) {
    case "created":
      return <CirclePlus size={14} />;
    case "deleted":
      return <Trash2 size={14} />;
    case "updated":
      return <Pencil size={14} />;
    default:
      return <Zap size={14} />;
  }
};

function ChangeRow({ cluster, c, showNamespace }: { cluster: string; c: Change; showNamespace: boolean }) {
  const [open, setOpen] = useState(false);
  const hasDiff = !!(c.before || c.after);
  const openObject = async () => {
    const def = byKind.get(c.kind) ?? (await findDef(cluster, c.kind));
    if (def) useApp.getState().openDetail({ gvr: def.gvr, kind: def.kind, namespace: c.namespace, name: c.name }, true);
  };
  return (
    <div className={`change ${c.warning ? "warning" : ""} type-${c.type}`}>
      <div className="change-time" title={new Date(c.time).toLocaleString()}>
        {new Date(c.time).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false })}
      </div>
      <div className="change-icon">{typeIcon(c)}</div>
      <div className="change-main">
        <div className="change-head">
          <button className="link change-obj" onClick={openObject}>
            <span className="muted">{c.kind}</span> {showNamespace && c.namespace ? `${c.namespace}/` : ""}
            {c.name}
          </button>
          <Badge tone={c.warning ? "warn" : c.type === "deleted" ? "bad" : c.type === "created" ? "ok" : "muted"}>{c.type === "event" ? "warning" : c.type}</Badge>
          {c.offline && (
            <span className="offline-tag" title="Found when Kube Desk started again">
              <Moon size={11} /> while closed
            </span>
          )}
          {c.actor && <span className="faint small">by {c.actor}</span>}
        </div>
        <div className="change-summary">
          {c.summary.map((s, i) => (
            <div key={i}>{s}</div>
          ))}
        </div>
        {hasDiff && (
          <button className="link small" onClick={() => setOpen(!open)}>
            {open ? <ChevronDown size={12} /> : <ChevronRight size={12} />} {c.type === "updated" ? "Diff" : c.type === "created" ? "Spec" : "Spec before"}
          </button>
        )}
        {open && hasDiff && <DiffView before={c.before ?? ""} after={c.after ?? ""} />}
      </div>
    </div>
  );
}

/** ObjectHistory is the changes of one object, for its details. */
export function ObjectHistory({ cluster, kind, namespace, name }: { cluster: string; kind: string; namespace: string; name: string }) {
  const targets = useAlerts((s) => s.state);
  const watched = isWatched(targets, cluster, namespace) || isWatched(targets, cluster, "");
  const since = useMemo(() => sinceOf(7 * 24 * 3600e3), []);
  const { changes, error } = useChanges(watched ? { cluster, kind, namespace, name, since, limit: 500 } : null);
  if (!watched)
    return (
      <Empty icon={<History size={26} />} title="Not recorded">
        <p>Watch {namespace ? `namespace ${namespace}` : "this cluster"} to record the changes of its objects.</p>
        <Button onClick={() => toggleWatch(cluster, namespace)}>Watch {namespace || "the cluster"}</Button>
      </Empty>
    );
  if (error) return <ErrorBox message={error} />;
  if (!changes)
    return (
      <div className="center-pad">
        <Spinner />
      </div>
    );
  if (!changes.length) return <Empty icon={<Clock3 size={26} />} title="No changes recorded">Kube Desk records changes while it runs, for 7 days.</Empty>;
  return <ChangeList cluster={cluster} changes={changes} showNamespace={false} />;
}
