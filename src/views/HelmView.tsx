import { useCallback, useEffect, useMemo, useState } from "react";
import { ArrowLeftRight, Package, RefreshCw, RotateCcw, Search, X } from "lucide-react";
import { Helm, type HelmRelease, type HelmReleaseDetail } from "../mygo";
import { findDef } from "../lib/discovery";
import { age, errorMessage, type Tone } from "../lib/k8s";
import { readOnly, typeToConfirm } from "../lib/actions";
import { ask, toast, useActiveView, useApp } from "../lib/store";
import { useNow } from "../lib/watch";
import { Badge, Button, Empty, ErrorBox, IconButton, KV, Spinner, Tabs } from "../components/ui";
import { YamlEditor } from "../components/YamlEditor";
import { DiffView } from "../components/DiffView";

const statusTone = (s: string): Tone =>
  s === "deployed" ? "ok" : s === "failed" ? "bad" : s.startsWith("pending") ? "info" : s === "uninstalling" ? "warn" : "muted";

/** HelmView lists the Helm releases of a namespace, read from the cluster. */
export function HelmView({ cluster }: { cluster: string }) {
  const view = useActiveView()!;
  const now = useNow(10000);
  const [releases, setReleases] = useState<HelmRelease[] | null>(null);
  const [error, setError] = useState("");
  const [filter, setFilter] = useState("");
  const [open, setOpen] = useState<{ namespace: string; name: string } | null>(null);
  const [tick, setTick] = useState(0);

  const load = useCallback(() => {
    Helm.releases(cluster, view.namespace).then(
      (r) => {
        setReleases(r);
        setError("");
      },
      (e) => setError(errorMessage(e)),
    );
  }, [cluster, view.namespace]);

  useEffect(() => {
    setReleases(null);
    load();
    const t = setInterval(load, 20000);
    return () => clearInterval(t);
  }, [load]);

  const f = filter.toLowerCase();
  const shown = (releases ?? []).filter((r) => !f || `${r.name} ${r.namespace} ${r.chart} ${r.status}`.toLowerCase().includes(f));

  return (
    <div className="helm-view">
      <div className="list-view">
        <header className="list-header">
          <div className="list-title">
            <Package size={18} className="muted" />
            <h1>Helm releases</h1>
            <span className="count">{releases === null && !error ? <Spinner size={13} /> : shown.length}</span>
            <span className="scope">{view.namespace ? `in ${view.namespace}` : "all namespaces"}</span>
          </div>
          <div className="list-tools">
            <div className="search-input">
              <Search size={14} />
              <input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="Filter releases…" />
              {filter && (
                <button className="clear" onClick={() => setFilter("")}>
                  <X size={13} />
                </button>
              )}
            </div>
            <IconButton label="Reload" onClick={load}>
              <RefreshCw size={15} />
            </IconButton>
          </div>
        </header>
        {error ? (
          <div className="list-error">
            <ErrorBox title="Could not read the Helm releases" message={error} action={<Button onClick={load}>Retry</Button>} />
          </div>
        ) : releases && !releases.length ? (
          <Empty icon={<Package size={28} />} title="No Helm releases">
            {view.namespace ? `Nothing was installed with Helm in namespace ${view.namespace}.` : "Nothing was installed with Helm in this cluster."}
          </Empty>
        ) : (
          <div className="table-wrap">
            <table className="simple-table helm-table">
              <thead>
                <tr>
                  <th>Name</th>
                  {!view.namespace && <th>Namespace</th>}
                  <th>Chart</th>
                  <th>App version</th>
                  <th className="right">Revision</th>
                  <th>Status</th>
                  <th className="right">Updated</th>
                </tr>
              </thead>
              <tbody>
                {shown.map((r) => (
                  <tr key={`${r.namespace}/${r.name}`} className={open?.name === r.name && open.namespace === r.namespace ? "selected" : ""} onClick={() => setOpen({ namespace: r.namespace, name: r.name })}>
                    <td className="cell-name">{r.name}</td>
                    {!view.namespace && <td>{r.namespace}</td>}
                    <td className="mono small">
                      {r.chart}
                      <span className="faint">-{r.chartVersion}</span>
                    </td>
                    <td className="mono small">{r.appVersion || "—"}</td>
                    <td className="right">{r.revision}</td>
                    <td>
                      <Badge tone={statusTone(r.status)}>{r.status}</Badge>
                    </td>
                    <td className="right faint" title={r.updated}>
                      {r.updated ? age(r.updated, now) : "—"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
      {open && (
        <HelmDetail
          key={`${open.namespace}/${open.name}/${tick}`}
          cluster={cluster}
          namespace={open.namespace}
          name={open.name}
          onClose={() => setOpen(null)}
          onChanged={() => {
            load();
            setTick((t) => t + 1);
          }}
        />
      )}
    </div>
  );
}

type Tab = "overview" | "values" | "manifest" | "history";

function HelmDetail({ cluster, namespace, name, onClose, onChanged }: { cluster: string; namespace: string; name: string; onClose(): void; onChanged(): void }) {
  const [d, setD] = useState<HelmReleaseDetail | null>(null);
  const [error, setError] = useState("");
  const [tab, setTab] = useState<Tab>("overview");
  const [allValues, setAllValues] = useState(false);
  const [compare, setCompare] = useState<number | null>(null);
  const openDetail = useApp((s) => s.openDetail);
  const now = useNow(10000);

  useEffect(() => {
    Helm.release(cluster, namespace, name, 0).then(setD, (e) => setError(errorMessage(e)));
  }, [cluster, namespace, name]);

  const rollback = async (revision: number) => {
    if (readOnly(cluster)) return;
    let plan;
    try {
      plan = await Helm.planRollback(cluster, namespace, name, revision);
    } catch (e) {
      return toast.error("Could not work out the rollback", e);
    }
    if (plan.helmMissing) return toast.error("The helm command is not installed", "Rolling back runs helm rollback, as Helm records it. Install Helm (brew install helm), then try again.");
    const removed = plan.removed.length ? `\n\nHelm deletes what revision ${revision} does not have:\n${plan.removed.slice(0, 12).join("\n")}${plan.removed.length > 12 ? `\n…and ${plan.removed.length - 12} more` : ""}` : "";
    const r = await ask({
      title: `Roll ${name} back to revision ${revision}?`,
      message: `Helm makes revision ${d!.revision + 1} from revision ${revision}. These are the changes the cluster will get.${removed}`,
      confirmLabel: "Roll back",
      danger: true,
      diffs: plan.diffs,
      typeToConfirm: typeToConfirm(cluster),
    });
    if (!r.ok) return;
    toast.info(`Rolling ${name} back…`);
    try {
      const out = await Helm.rollback(cluster, namespace, name, revision);
      toast.ok(`Rolled ${name} back to revision ${revision}`, out);
      onChanged();
    } catch (e) {
      toast.error("Rollback failed", e);
    }
  };

  const openObject = async (o: HelmReleaseDetail["objects"][number]) => {
    const def = await findDef(cluster, o.kind, o.apiVersion);
    if (!def) return toast.info(`Kube Desk cannot open ${o.kind} objects`);
    onClose();
    openDetail({ gvr: def.gvr, kind: o.kind, namespace: def.namespaced ? o.namespace : "", name: o.name }, true);
  };

  return (
    <aside className="details helm-details">
      <header className="details-header">
        <div className="details-title">
          <div className="details-kind">
            <Badge tone="info">Helm release</Badge>
            <span className="muted small">{namespace}</span>
          </div>
          <h2 title={name}>{name}</h2>
        </div>
        <div className="details-actions">
          <IconButton label="Close" onClick={onClose}>
            <X size={16} />
          </IconButton>
        </div>
      </header>
      <Tabs
        tabs={[
          { id: "overview" as Tab, label: "Overview" },
          { id: "values" as Tab, label: "Values" },
          { id: "manifest" as Tab, label: "Manifest" },
          { id: "history" as Tab, label: "History" },
        ]}
        value={tab}
        onChange={(t) => setTab(t as Tab)}
      />
      <div className={`details-body ${tab === "values" || tab === "manifest" ? "fill" : ""}`}>
        {error ? (
          <ErrorBox message={error} />
        ) : !d ? (
          <div className="center-pad">
            <Spinner />
          </div>
        ) : tab === "overview" ? (
          <div className="stack">
            <KV
              items={[
                ["Status", <Badge tone={statusTone(d.status)}>{d.status}</Badge>],
                ["Chart", <span className="mono">{`${d.chart}-${d.chartVersion}`}</span>],
                ["App version", d.appVersion || "—"],
                ["Revision", String(d.revision)],
                ["Updated", d.updated ? `${age(d.updated, now)} ago` : "—"],
                ["Description", d.description || "—"],
              ]}
            />
            <div>
              <div className="section-title">Objects ({d.objects.length})</div>
              <div className="helm-objects">
                {d.objects.map((o) => (
                  <button key={`${o.kind}/${o.namespace}/${o.name}`} className="helm-object" onClick={() => openObject(o)}>
                    <Badge tone="muted">{o.kind}</Badge> <span className="mono small">{o.name}</span>
                  </button>
                ))}
              </div>
            </div>
            {d.notes && (
              <div>
                <div className="section-title">Notes</div>
                <pre className="helm-notes selectable">{d.notes}</pre>
              </div>
            )}
          </div>
        ) : tab === "values" ? (
          <div className="yaml-tab">
            <div className="yaml-toolbar">
              <div className="segmented">
                <button className={!allValues ? "on" : ""} onClick={() => setAllValues(false)}>
                  Given at install
                </button>
                <button className={allValues ? "on" : ""} onClick={() => setAllValues(true)}>
                  With chart defaults
                </button>
              </div>
            </div>
            <YamlEditor value={(allValues ? d.allValues : d.values) || "# No values were given: the chart's defaults apply.\n"} readOnly />
          </div>
        ) : tab === "manifest" ? (
          <div className="yaml-tab">
            <div className="yaml-toolbar">
              <span className="faint small">What revision {d.revision} rendered. Secret values are masked.</span>
            </div>
            <YamlEditor value={d.manifest} readOnly />
          </div>
        ) : compare !== null ? (
          <RevisionDiff cluster={cluster} namespace={namespace} name={name} from={compare} to={d.revision} onBack={() => setCompare(null)} />
        ) : (
          <table className="simple-table">
            <thead>
              <tr>
                <th>Revision</th>
                <th>Updated</th>
                <th>Status</th>
                <th>Chart</th>
                <th>Description</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {d.history.map((h) => (
                <tr key={h.revision}>
                  <td>{h.revision}</td>
                  <td className="faint" title={h.updated}>
                    {h.updated ? age(h.updated, now) : "—"}
                  </td>
                  <td>
                    <Badge tone={statusTone(h.status)}>{h.status}</Badge>
                  </td>
                  <td className="mono small">{h.chartVersion}</td>
                  <td className="small">{h.description}</td>
                  <td className="right nowrap">
                    {h.revision !== d.revision && (
                      <>
                        <Button size="sm" variant="ghost" icon={<ArrowLeftRight size={13} />} onClick={() => setCompare(h.revision)}>
                          Diff
                        </Button>
                        <Button size="sm" variant="ghost" icon={<RotateCcw size={13} />} onClick={() => rollback(h.revision)}>
                          Roll back
                        </Button>
                      </>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </aside>
  );
}

/** RevisionDiff shows what changed between two revisions of a release. */
function RevisionDiff({ cluster, namespace, name, from, to, onBack }: { cluster: string; namespace: string; name: string; from: number; to: number; onBack(): void }) {
  const [pair, setPair] = useState<[HelmReleaseDetail, HelmReleaseDetail] | null>(null);
  const [error, setError] = useState("");
  const [what, setWhat] = useState<"values" | "manifest">("values");
  useEffect(() => {
    Promise.all([Helm.release(cluster, namespace, name, from), Helm.release(cluster, namespace, name, to)]).then(setPair, (e) => setError(errorMessage(e)));
  }, [cluster, namespace, name, from, to]);
  const texts = useMemo(() => (pair ? (what === "values" ? [pair[0].values, pair[1].values] : [pair[0].manifest, pair[1].manifest]) : ["", ""]), [pair, what]);
  return (
    <div className="stack">
      <div className="row gap">
        <Button size="sm" variant="ghost" onClick={onBack}>
          ← History
        </Button>
        <span className="small">
          Revision {from} → {to}
        </span>
        <div className="spacer" />
        <div className="segmented">
          <button className={what === "values" ? "on" : ""} onClick={() => setWhat("values")}>
            Values
          </button>
          <button className={what === "manifest" ? "on" : ""} onClick={() => setWhat("manifest")}>
            Manifest
          </button>
        </div>
      </div>
      {error ? (
        <ErrorBox message={error} />
      ) : !pair ? (
        <Spinner />
      ) : (
        <>
          {pair[0].chartVersion !== pair[1].chartVersion && (
            <div className="small">
              Chart <span className="mono">{pair[0].chartVersion}</span> → <span className="mono">{pair[1].chartVersion}</span>
            </div>
          )}
          <DiffView before={texts[0]!} after={texts[1]!} />
        </>
      )}
    </div>
  );
}
