import { useState, type ReactNode } from "react";
import { ChevronDown, ChevronRight, Copy, Eye, EyeOff, Link2 } from "lucide-react";
import type { GVR } from "../../mygo";
import { age, decodeBase64, podStatus, type KObj } from "../../lib/k8s";
import { findDef } from "../../lib/discovery";
import { toast, useApp } from "../../lib/store";
import { useNow } from "../../lib/watch";
import { Badge, Card, IconButton, KV, Labels } from "../../components/ui";

export const podsGVR: GVR = { group: "", version: "v1", resource: "pods" };

/** ObjLink opens another object in the details panel. */
export function ObjLink({ cluster, kind, apiVersion, name, namespace, children }: { cluster: string; kind: string; apiVersion?: string; name: string; namespace?: string; children?: ReactNode }) {
  const open = async () => {
    const def = await findDef(cluster, kind, apiVersion);
    if (!def) return toast.error(`This app cannot show ${kind} objects`);
    useApp.getState().openDetail({ gvr: def.gvr, kind: def.kind, namespace: def.namespaced ? (namespace ?? "") : "", name });
  };
  return (
    <button className="link" onClick={open}>
      {children ?? name}
    </button>
  );
}

export function copy(text: string, what = "Copied") {
  navigator.clipboard.writeText(text).then(
    () => toast.ok(what),
    (e) => toast.error("Could not copy", e),
  );
}

export function MetaCard({ cluster, o }: { cluster: string; o: KObj }) {
  const now = useNow(10000);
  const [showAnn, setShowAnn] = useState(false);
  const ann = Object.entries(o.metadata.annotations ?? {}).filter(([k]) => k !== "kubectl.kubernetes.io/last-applied-configuration");
  return (
    <Card title="Metadata">
      <KV
        items={[
          ["Name", <span className="mono selectable">{o.metadata.name}</span>],
          ...(o.metadata.namespace ? ([["Namespace", <ObjLink cluster={cluster} kind="Namespace" name={o.metadata.namespace} />]] as [ReactNode, ReactNode][]) : []),
          ["Created", o.metadata.creationTimestamp ? `${age(o.metadata.creationTimestamp, now)} ago · ${new Date(o.metadata.creationTimestamp).toLocaleString()}` : ""],
          ["UID", <span className="mono faint selectable">{o.metadata.uid}</span>],
          ...((o.metadata.ownerReferences ?? []).length
            ? ([
                [
                  "Controlled by",
                  <div className="stack">
                    {o.metadata.ownerReferences!.map((r) => (
                      <span key={r.uid}>
                        <span className="muted">{r.kind} </span>
                        <ObjLink cluster={cluster} kind={r.kind} apiVersion={r.apiVersion} name={r.name} namespace={o.metadata.namespace} />
                      </span>
                    ))}
                  </div>,
                ],
              ] as [ReactNode, ReactNode][])
            : []),
          ["Labels", <Labels labels={o.metadata.labels} />],
          [
            "Annotations",
            ann.length ? (
              <div>
                <button className="link" onClick={() => setShowAnn(!showAnn)}>
                  {showAnn ? <ChevronDown size={13} /> : <ChevronRight size={13} />} {ann.length} {ann.length === 1 ? "annotation" : "annotations"}
                </button>
                {showAnn && (
                  <div className="annotations">
                    {ann.map(([k, v]) => (
                      <div key={k} className="annotation">
                        <div className="annotation-key">{k}</div>
                        <div className="annotation-val mono selectable">{v}</div>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            ) : (
              ""
            ),
          ],
        ]}
      />
    </Card>
  );
}

export function Conditions({ conditions }: { conditions?: any[] }) {
  const now = useNow(10000);
  if (!conditions?.length) return null;
  return (
    <Card title="Conditions">
      <div className="mini-table">
        <div className="mini-row mini-head" style={{ gridTemplateColumns: "1.2fr 70px 1fr 2fr 70px" }}>
          <span>Type</span>
          <span>Status</span>
          <span>Reason</span>
          <span>Message</span>
          <span className="right">Age</span>
        </div>
        {conditions.map((c, i) => {
          const positive = c.status === "True";
          const inverted = /Pressure|Unavailable|Failed/.test(c.type);
          const tone = c.status === "Unknown" ? "muted" : positive !== inverted ? "ok" : "bad";
          return (
            <div key={i} className="mini-row" style={{ gridTemplateColumns: "1.2fr 70px 1fr 2fr 70px" }}>
              <span>{c.type}</span>
              <span>
                <Badge tone={tone}>{c.status}</Badge>
              </span>
              <span className="muted">{c.reason ?? ""}</span>
              <span className="cell-wrap muted">{c.message ?? ""}</span>
              <span className="right muted">{age(c.lastTransitionTime ?? c.lastHeartbeatTime, now)}</span>
            </div>
          );
        })}
      </div>
    </Card>
  );
}

export function PodMiniTable({ pods, title = "Pods", showNode = true, showNamespace = false }: { pods: KObj[]; title?: string; showNode?: boolean; showNamespace?: boolean }) {
  const now = useNow(5000);
  const open = useApp((s) => s.openDetail);
  const cols = `minmax(160px, 2fr) ${showNamespace ? "minmax(100px, 1fr) " : ""}60px minmax(100px, 1fr) 70px ${showNode ? "minmax(100px, 1fr) " : ""}60px`;
  return (
    <Card title={`${title} (${pods.length})`}>
      {pods.length === 0 ? (
        <div className="muted small">None</div>
      ) : (
        <div className="mini-table">
          <div className="mini-row mini-head" style={{ gridTemplateColumns: cols }}>
            <span>Name</span>
            {showNamespace && <span>Namespace</span>}
            <span>Ready</span>
            <span>Status</span>
            <span className="right">Restarts</span>
            {showNode && <span>Node</span>}
            <span className="right">Age</span>
          </div>
          {[...pods]
            .sort((a, b) => a.metadata.name.localeCompare(b.metadata.name))
            .map((p) => {
              const s = podStatus(p);
              return (
                <button
                  key={p.metadata.uid}
                  className="mini-row clickable"
                  style={{ gridTemplateColumns: cols }}
                  onClick={() => open({ gvr: podsGVR, kind: "Pod", namespace: p.metadata.namespace ?? "", name: p.metadata.name })}
                >
                  <span className="cell-name">{p.metadata.name}</span>
                  {showNamespace && <span className="muted">{p.metadata.namespace}</span>}
                  <span>{`${s.ready}/${s.total}`}</span>
                  <span>
                    <Badge tone={s.tone} dot>
                      {s.reason}
                    </Badge>
                  </span>
                  <span className={`right ${s.restarts ? "text-warn" : "muted"}`}>{s.restarts}</span>
                  {showNode && <span className="muted">{p.spec?.nodeName ?? ""}</span>}
                  <span className="right muted">{age(p.metadata.creationTimestamp, now)}</span>
                </button>
              );
            })}
        </div>
      )}
    </Card>
  );
}

export function DataEntries({ data, secret }: { data: Record<string, string>; secret?: boolean }) {
  const [revealed, setRevealed] = useState<Set<string>>(new Set());
  const entries = Object.entries(data);
  if (!entries.length) return <div className="muted small">No data</div>;
  return (
    <div className="data-entries">
      {entries.map(([k, raw]) => {
        const value = secret ? decodeBase64(raw) : raw;
        const shown = !secret || revealed.has(k);
        return (
          <div key={k} className="data-entry">
            <div className="data-key">
              <span className="mono">{k}</span>
              <span className="faint small">{value.length} bytes</span>
              <div className="spacer" />
              {secret && (
                <IconButton
                  label={shown ? "Hide" : "Reveal"}
                  onClick={() => {
                    const next = new Set(revealed);
                    if (shown) next.delete(k);
                    else next.add(k);
                    setRevealed(next);
                  }}
                >
                  {shown ? <EyeOff size={14} /> : <Eye size={14} />}
                </IconButton>
              )}
              <IconButton label="Copy value" onClick={() => copy(value, `Copied ${k}`)}>
                <Copy size={14} />
              </IconButton>
            </div>
            <pre className="data-value selectable">{shown ? value : "••••••••••••"}</pre>
          </div>
        );
      })}
    </div>
  );
}

export function Selector({ selector }: { selector?: Record<string, string> }) {
  if (!selector || !Object.keys(selector).length) return <span className="muted">—</span>;
  return (
    <span className="selector">
      <Link2 size={12} className="muted" />
      <Labels labels={selector} />
    </span>
  );
}
