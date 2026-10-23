import { useEffect, useMemo, useState } from "react";
import { ArrowRight, Info, Rocket } from "lucide-react";
import { Kube, type GVR, type PromotePlan, type PromoteRequest } from "../mygo";
import { errorMessage } from "../lib/k8s";
import { ask, toast, useApp } from "../lib/store";
import { Button, ErrorBox, Field, Modal, Spinner } from "./ui";
import { ClusterTile } from "./ClusterRail";
import { ObjectDiffs } from "./DiffView";

/** PromoteDialog copies an object from one cluster to another, after showing what changes there. */
export function PromoteDialog({ from, gvr, kind, namespace, name, onClose }: { from: string; gvr: GVR; kind: string; namespace: string; name: string; onClose(): void }) {
  const clusters = useApp((s) => s.clusters);
  const source = clusters.find((c) => c.id === from);
  const targets = useMemo(() => clusters.filter((c) => !c.hidden && c.id !== from && c.sourceKind !== "snapshot"), [clusters, from]);
  const [to, setTo] = useState(() => targets.find((c) => !c.readOnly)?.id ?? targets[0]?.id ?? "");
  const [targetNs, setTargetNs] = useState(namespace);
  const [images, setImages] = useState<Record<string, string>>({});
  const [withConfig, setWithConfig] = useState(true);
  const [withSecrets, setWithSecrets] = useState(false);
  const [plan, setPlan] = useState<PromotePlan | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const target = clusters.find((c) => c.id === to);

  const req: PromoteRequest = { from, to, gvr, kind, namespace, name, targetNamespace: targetNs.trim() || namespace, images, withConfig, withSecrets };
  const key = JSON.stringify(req);

  // The plan follows the choices, a moment after they settle.
  useEffect(() => {
    if (!to) return;
    let alive = true;
    setError("");
    const t = setTimeout(() => {
      Kube.planPromote(req).then(
        (p) => alive && setPlan(p),
        (e) => alive && (setError(errorMessage(e)), setPlan(null)),
      );
    }, 350);
    return () => {
      alive = false;
      clearTimeout(t);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);

  const changes = plan?.diffs.filter((d) => d.action !== "unchanged").length ?? 0;

  const promote = async () => {
    if (!target || !plan) return;
    if (target.production) {
      const ok = await ask({ title: `Promote ${kind} ${name} to ${target.name}?`, message: `${changes} object${changes === 1 ? "" : "s"} change in a production cluster.`, confirmLabel: "Promote", danger: true, typeToConfirm: target.name });
      if (!ok.ok) return;
    }
    setBusy(true);
    try {
      const res = await Kube.promote(req);
      toast.ok(`Promoted to ${target.name}`, res.join("\n"));
      onClose();
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
          <Rocket size={17} /> Promote {kind} {name}
        </span>
      }
      onClose={onClose}
      width={760}
      footer={
        <>
          <span className="faint small">{plan ? (changes ? `${changes} object${changes === 1 ? "" : "s"} change in ${target?.name}` : `${target?.name} already has it as it is`) : ""}</span>
          <div className="spacer" />
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" busy={busy} disabled={!plan || !changes || !!target?.readOnly} onClick={promote} title={target?.readOnly ? `${target.name} is read-only` : undefined}>
            Promote
          </Button>
        </>
      }
    >
      {!targets.length ? (
        <ErrorBox title="No other cluster" message="Add the cluster to promote to first." />
      ) : (
        <div className="add-body">
          <div className="promote-route">
            <div className="promote-end">
              {source && <ClusterTile c={source} size={22} />}
              <div>
                <div className="faint small">From</div>
                <b>{source?.name}</b>
                <div className="mono small faint">
                  {namespace ? `${namespace}/` : ""}
                  {name}
                </div>
              </div>
            </div>
            <ArrowRight size={18} className="faint" />
            <div className="promote-end">
              <Field label="To">
                <select value={to} onChange={(e) => setTo(e.target.value)}>
                  {targets.map((c) => (
                    <option key={c.id} value={c.id}>
                      {c.name}
                      {c.production ? " (production)" : ""}
                      {c.readOnly ? " (read-only)" : ""}
                    </option>
                  ))}
                </select>
              </Field>
              {namespace && (
                <Field label="Namespace">
                  <input value={targetNs} onChange={(e) => setTargetNs(e.target.value)} className="mono" spellCheck={false} />
                </Field>
              )}
            </div>
          </div>

          {plan && plan.containers.length > 0 && (
            <div>
              <div className="section-title">Images</div>
              <div className="promote-images">
                {plan.containers.map((c) => (
                  <label key={c.name} className="promote-image">
                    <span className="mono small">{c.name}</span>
                    <input value={images[c.name] ?? c.image} onChange={(e) => setImages({ ...images, [c.name]: e.target.value })} className="mono" spellCheck={false} />
                  </label>
                ))}
              </div>
            </div>
          )}

          {plan && (plan.configMaps.length > 0 || plan.secrets.length > 0) && (
            <div className="toggles">
              {plan.configMaps.length > 0 && (
                <label className="toggle">
                  <input type="checkbox" checked={withConfig} onChange={(e) => setWithConfig(e.target.checked)} />
                  <span>
                    <b>Bring its config maps</b>
                    <span className="faint small mono">{plan.configMaps.join(", ")}</span>
                  </span>
                </label>
              )}
              {plan.secrets.length > 0 && (
                <label className="toggle">
                  <input type="checkbox" checked={withSecrets} onChange={(e) => setWithSecrets(e.target.checked)} />
                  <span>
                    <b>Bring its secrets, values included</b>
                    <span className="faint small mono">{plan.secrets.join(", ")}</span>
                    <span className="faint small">Off by default: clusters usually hold their own credentials.</span>
                  </span>
                </label>
              )}
            </div>
          )}

          {plan?.notes.map((n) => (
            <div key={n} className="promote-note">
              <Info size={14} /> {n}
            </div>
          ))}
          {error && <div className="form-error">{error}</div>}
          {!plan && !error ? (
            <div className="center-pad">
              <Spinner />
            </div>
          ) : (
            plan && (
              <div>
                <div className="section-title">What {target?.name} gets</div>
                <ObjectDiffs diffs={plan.diffs} />
                <p className="faint small">Status, identifiers, owners and addresses the cluster gives are left out, so the target makes its own. Secret values are masked here.</p>
              </div>
            )
          )}
        </div>
      )}
    </Modal>
  );
}
