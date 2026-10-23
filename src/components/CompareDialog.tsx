import { useEffect, useMemo, useState } from "react";
import { GitCompareArrows } from "lucide-react";
import { Kube, type CompareResult, type GVR } from "../mygo";
import { useApp } from "../lib/store";
import { errorMessage } from "../lib/k8s";
import { ErrorBox, Modal, Spinner } from "./ui";
import { DiffView } from "./DiffView";
import { ClusterTile } from "./ClusterRail";

/** CompareDialog lines up an object across clusters, and what differs. */
export function CompareDialog({ gvr, kind, namespace, name, onClose }: { gvr: GVR; kind: string; namespace: string; name: string; onClose(): void }) {
  const clusters = useApp((s) => s.clusters);
  const all = useMemo(() => clusters.filter((c) => !c.hidden), [clusters]);
  const status = useApp((s) => s.status);
  const active = useApp((s) => s.active);
  const [chosen, setChosen] = useState<string[]>(() => all.filter((c) => c.id === active || status[c.id]?.ok).map((c) => c.id));
  const [res, setRes] = useState<CompareResult | null>(null);
  const [error, setError] = useState("");
  const [onlyDiff, setOnlyDiff] = useState(true);
  const [diffPair, setDiffPair] = useState<[number, number] | null>(null);

  useEffect(() => {
    setRes(null);
    setError("");
    Kube.compare({ gvr, kind, namespace, name, clusters: chosen }).then(setRes, (e) => setError(errorMessage(e)));
  }, [gvr, kind, namespace, name, chosen.join(",")]);

  const rows = useMemo(() => (res ? res.rows.filter((r) => !onlyDiff || r.differs) : []), [res, onlyDiff]);
  const found = res ? res.columns.filter((c) => c.found).length : 0;
  const differing = res ? res.rows.filter((r) => r.differs).length : 0;
  const toggle = (id: string) => setChosen((c) => (c.includes(id) ? c.filter((x) => x !== id) : [...c, id]));

  return (
    <Modal
      title={
        <span className="row gap">
          <GitCompareArrows size={18} /> {kind} {namespace ? `${namespace}/` : ""}
          {name} across clusters
        </span>
      }
      onClose={onClose}
      width={Math.min(1200, 360 + chosen.length * 220)}
      className="compare-modal"
    >
      <div className="compare-clusters">
        {all.map((c) => (
          <label key={c.id} className={`compare-cluster ${chosen.includes(c.id) ? "on" : ""}`}>
            <input type="checkbox" checked={chosen.includes(c.id)} onChange={() => toggle(c.id)} />
            <ClusterTile c={c} size={18} />
            {c.name}
          </label>
        ))}
      </div>
      {error ? (
        <ErrorBox message={error} />
      ) : !res ? (
        <div className="center-pad">
          <Spinner />
        </div>
      ) : (
        <>
          <div className="row gap compare-summary">
            <span className="small">
              Found in {found} of {res.columns.length} clusters · <b>{differing}</b> field{differing === 1 ? "" : "s"} differ
            </span>
            <div className="spacer" />
            <label className="check">
              <input type="checkbox" checked={onlyDiff} onChange={(e) => setOnlyDiff(e.target.checked)} /> Only differences
            </label>
          </div>
          <div className="compare-table" style={{ gridTemplateColumns: `minmax(160px, 1.1fr) repeat(${res.columns.length}, minmax(140px, 1fr))` }}>
            <div className="ct-head">Field</div>
            {res.columns.map((c, i) => (
              <div key={c.cluster} className="ct-head">
                {c.clusterName}
                {!c.found && <span className="faint small"> · {c.error ? "error" : "not found"}</span>}
                {c.found && found > 1 && i > 0 && (
                  <button className="link small" onClick={() => setDiffPair([res.columns.findIndex((x) => x.found), i])}>
                    YAML diff
                  </button>
                )}
                {c.found && all.length > 1 && (
                  <button className="link small" title={`Copy it from ${c.clusterName} to another cluster`} onClick={() => useApp.getState().openDialog({ kind: "promote", from: c.cluster, gvr, object: kind, namespace, name })}>
                    Promote
                  </button>
                )}
              </div>
            ))}
            {rows.length === 0 && (
              <div className="ct-empty faint small" style={{ gridColumn: `1 / span ${res.columns.length + 1}` }}>
                {found < 2 ? "It exists in fewer than two of these clusters." : "Every field is the same."}
              </div>
            )}
            {rows.map((r) => (
              <div key={r.field} className={`ct-row ${r.differs ? "differs" : ""}`} style={{ display: "contents" }}>
                <div className="ct-field mono">{r.field}</div>
                {r.values.map((v, i) => (
                  <div key={i} className="ct-cell mono selectable" title={v}>
                    {res.columns[i]!.found ? v : ""}
                  </div>
                ))}
              </div>
            ))}
          </div>
          {res.columns.some((c) => c.error) && (
            <div className="faint small" style={{ marginTop: 8 }}>
              {res.columns
                .filter((c) => c.error)
                .map((c) => `${c.clusterName}: ${c.error}`)
                .join(" · ")}
            </div>
          )}
          {diffPair && res.columns[diffPair[0]] && res.columns[diffPair[1]] && (
            <div style={{ marginTop: 12 }}>
              <div className="small">
                <b>{res.columns[diffPair[0]]!.clusterName}</b> → <b>{res.columns[diffPair[1]]!.clusterName}</b>
              </div>
              <DiffView before={res.columns[diffPair[0]]!.yaml ?? ""} after={res.columns[diffPair[1]]!.yaml ?? ""} />
            </div>
          )}
        </>
      )}
    </Modal>
  );
}
