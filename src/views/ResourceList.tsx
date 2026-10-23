import { useMemo, useState } from "react";
import { Plus, ScrollText, Search, SquareTerminal, Trash2, X } from "lucide-react";
import type { KObj } from "../lib/k8s";
import type { ResourceDef } from "../lib/resources";
import { useApp, useActiveView } from "../lib/store";
import { useWatch } from "../lib/watch";
import { deleteObjects, openPodTab } from "../lib/actions";
import { objectActions } from "../lib/objectActions";
import { ResourceTable, type RowAction } from "../components/ResourceTable";
import { ContextMenu } from "../components/Menu";
import { Button, Empty, ErrorBox, IconButton, Spinner } from "../components/ui";
import { newResourceTemplate } from "../lib/templates";
import { useMetricsColumns } from "../lib/metricsColumns";
import { canDo, noAccess, useAccessUpdates } from "../lib/access";

/** matches filters by words of the name and columns, and key=value labels. */
export function matches(o: KObj, def: ResourceDef, filter: string): boolean {
  const words = filter.toLowerCase().split(/\s+/).filter(Boolean);
  if (!words.length) return true;
  const labels = o.metadata.labels ?? {};
  const text = [o.metadata.name, o.metadata.namespace ?? "", ...def.columns.map((c) => String(c.value(o, 0)))].join(" ").toLowerCase();
  return words.every((w) => {
    const eq = w.indexOf("=");
    if (eq > 0) {
      const k = w.slice(0, eq);
      const v = w.slice(eq + 1);
      return Object.entries(labels).some(([lk, lv]) => lk.toLowerCase() === k && (v === "" || lv.toLowerCase() === v));
    }
    return text.includes(w);
  });
}

export function ResourceList({ cluster, def }: { cluster: string; def: ResourceDef }) {
  const view = useActiveView()!;
  const { setFilter, openDetail, openDialog } = useApp();
  const namespace = def.namespaced ? view.namespace : "";
  const ro = useApp((s) => !!s.clusters.find((c) => c.id === cluster)?.readOnly);
  const editable = def.kind !== "Event" && !ro;
  const { items, loading, error } = useWatch(cluster, def.gvr, namespace);
  const [checked, setChecked] = useState<Set<string>>(new Set());
  const [menu, setMenu] = useState<{ o: KObj; x: number; y: number } | null>(null);
  const filtered = useMemo(() => items.filter((o) => matches(o, def, view.filter)), [items, def, view.filter]);
  const top = view.details[view.details.length - 1];
  const selectedUid = top ? items.find((o) => o.metadata.name === top.name && (o.metadata.namespace ?? "") === top.namespace && top.kind === def.kind)?.metadata.uid : undefined;
  const metrics = useMetricsColumns(cluster, def, namespace);
  useAccessUpdates();
  const mayCreate = canDo(cluster, "create", def.gvr, namespace);
  const mayDelete = (o: KObj) => canDo(cluster, "delete", def.gvr, o.metadata.namespace ?? "");
  const columns = useMemo(() => {
    const cols = def.namespaced && namespace ? def.columns.filter((c) => c.id !== "namespace") : def.columns;
    if (!metrics.length) return cols;
    // Usage goes before the age, which stays last.
    return [...cols.slice(0, -1), ...metrics, ...cols.slice(-1)];
  }, [def, namespace, metrics]);

  const actions: RowAction[] = [];
  if (def.kind === "Pod") {
    actions.push({ label: "Logs", icon: <ScrollText size={14} />, run: (o) => openPodTab(o, "logs") });
    if (!ro) actions.push({ label: "Shell", icon: <SquareTerminal size={14} />, run: (o) => openPodTab(o, "terminal"), show: (o) => o.status?.phase === "Running" && canDo(cluster, "create", def.gvr, o.metadata.namespace ?? "", "exec") });
  }
  if (editable) actions.push({ label: "Delete", icon: <Trash2 size={14} />, run: (o) => deleteObjects(cluster, def.gvr, def.kind, [o]), danger: true, show: mayDelete });

  const checkedObjs = filtered.filter((o) => checked.has(o.metadata.uid));
  const Icon = def.icon;

  return (
    <div className="list-view">
      <header className="list-header">
        <div className="list-title">
          <Icon size={18} className="muted" />
          <h1>{def.title}</h1>
          <span className="count">{loading ? <Spinner size={13} /> : filtered.length === items.length ? items.length : `${filtered.length} / ${items.length}`}</span>
          {def.namespaced && <span className="scope">{namespace ? `in ${namespace}` : "all namespaces"}</span>}
        </div>
        <div className="list-tools">
          {checkedObjs.length > 0 && (
            <div className="bulk">
              <span>{checkedObjs.length} selected</span>
              <Button
                size="sm"
                variant="danger"
                icon={<Trash2 size={14} />}
                disabled={!checkedObjs.every(mayDelete)}
                title={checkedObjs.every(mayDelete) ? undefined : noAccess}
                onClick={async () => {
                  if (await deleteObjects(cluster, def.gvr, def.kind, checkedObjs)) setChecked(new Set());
                }}
              >
                Delete
              </Button>
              <IconButton label="Clear selection" onClick={() => setChecked(new Set())}>
                <X size={14} />
              </IconButton>
            </div>
          )}
          <div className="search-input">
            <Search size={14} />
            <input value={view.filter} onChange={(e) => setFilter(e.target.value)} placeholder={`Filter ${def.title.toLowerCase()}…  (app=web)`} />
            {view.filter && (
              <button className="clear" onClick={() => setFilter("")}>
                <X size={13} />
              </button>
            )}
          </div>
          {editable && (
            <Button size="sm" icon={<Plus size={14} />} disabled={!mayCreate} title={mayCreate ? undefined : noAccess} onClick={() => openDialog({ kind: "create", yaml: newResourceTemplate(def, namespace) })}>
              Create
            </Button>
          )}
        </div>
      </header>
      {error && !items.length ? (
        <div className="list-error">
          <ErrorBox title={`Could not list ${def.title.toLowerCase()}`} message={error} />
        </div>
      ) : !loading && !items.length ? (
        <Empty icon={<Icon size={28} />} title={`No ${def.title.toLowerCase()}`}>
          {def.namespaced && namespace ? `There are none in namespace ${namespace}.` : "There are none in this cluster."}
        </Empty>
      ) : (
        <>
          {error && <div className="list-warning">{error} — retrying…</div>}
          <ResourceTable
            key={def.key}
            items={filtered}
            columns={columns}
            defaultSort={def.defaultSort}
            selectedUid={selectedUid}
            actions={actions}
            checked={editable ? checked : undefined}
            onCheck={editable ? setChecked : undefined}
            onOpen={(o) => openDetail({ gvr: def.gvr, kind: def.kind, namespace: o.metadata.namespace ?? "", name: o.metadata.name }, true)}
            onContextMenu={(o, x, y) => setMenu({ o, x, y })}
          />
        </>
      )}
      {menu && <ContextMenu x={menu.x} y={menu.y} items={objectActions(cluster, def.gvr, def.kind, menu.o)} onClose={() => setMenu(null)} />}
    </div>
  );
}
