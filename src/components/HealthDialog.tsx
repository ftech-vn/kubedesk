import { useEffect, useMemo } from "react";
import { create } from "zustand";
import { AlertTriangle, CheckCircle2, Info, XCircle } from "lucide-react";
import { Clusters, type ClusterHealth, type HealthIssue } from "../mygo";
import { toast, useApp } from "../lib/store";
import { age } from "../lib/k8s";
import { Button, Modal, Spinner } from "./ui";
import { ClusterTile } from "./ClusterRail";

interface HealthStore {
  list: ClusterHealth[] | null;
  warned: boolean;
  load(): Promise<void>;
}

/** useHealth is the health of every cluster's kubeconfig entry. */
export const useHealth = create<HealthStore>((set, get) => ({
  list: null,
  warned: false,
  async load() {
    try {
      const list = await Clusters.health();
      set({ list });
      // Say once, at start, when credentials are about to stop working.
      if (!get().warned) {
        set({ warned: true });
        const clusters = useApp.getState().clusters.filter((c) => !c.hidden);
        const soon = list.filter((h) => clusters.some((c) => c.id === h.id) && h.issues.some((i) => /expire/.test(i.text) && i.level !== "info"));
        if (soon.length) toast.info(`Credentials of ${soon.length} cluster${soon.length > 1 ? "s" : ""} expire soon or did`, "Open Kubeconfig health, in ⌘K, to see which.");
      }
    } catch {
      /* shown when opened */
    }
  },
}));

/** issuesOf returns what matters of a cluster's health: problems, then notes. */
export function issuesOf(list: ClusterHealth[] | null, id: string): HealthIssue[] {
  return list?.find((h) => h.id === id)?.issues ?? [];
}

export function IssueIcon({ level, size = 14 }: { level: string; size?: number }) {
  if (level === "bad") return <XCircle size={size} className="text-bad" />;
  if (level === "warn") return <AlertTriangle size={size} className="text-warn" />;
  return <Info size={size} className="faint" />;
}

export function IssueList({ issues }: { issues: HealthIssue[] }) {
  return (
    <ul className="health-issues">
      {issues.map((i, n) => (
        <li key={n}>
          <IssueIcon level={i.level} />
          <div>
            <div>{i.text}</div>
            {i.fix && <div className="faint small">{i.fix}</div>}
          </div>
        </li>
      ))}
    </ul>
  );
}

/** HealthDialog lists what is wrong with how the kubeconfigs reach the clusters. */
export function HealthDialog({ onClose }: { onClose(): void }) {
  const { list, load } = useHealth();
  const clusters = useApp((s) => s.clusters);
  const status = useApp((s) => s.status);
  const openDialog = useApp((s) => s.openDialog);
  useEffect(() => {
    load();
  }, [load]);

  const rows = useMemo(() => {
    if (!list) return [];
    return clusters
      .filter((c) => !c.hidden)
      .map((c) => {
        const issues = [...issuesOf(list, c.id)];
        const st = status[c.id];
        const seen = list.find((h) => h.id === c.id)?.lastSeen;
        if (st && !st.ok && !st.checking && st.error) {
          issues.unshift({ level: "bad", text: seen ? `Not reachable now; last reached ${age(seen)} ago` : "Not reachable, and never reached from Kube Desk", fix: st.error });
        }
        return { c, issues };
      })
      .filter((r) => r.issues.length)
      .sort((a, b) => rank(a.issues) - rank(b.issues));
  }, [list, clusters, status]);

  return (
    <Modal title="Kubeconfig health" onClose={onClose} width={680} footer={<Button onClick={onClose}>Close</Button>}>
      {!list ? (
        <div className="center-pad">
          <Spinner />
        </div>
      ) : rows.length === 0 ? (
        <div className="health-ok">
          <CheckCircle2 size={22} className="text-ok" />
          <div>
            <div>Every kubeconfig looks fine.</div>
            <div className="faint small">Kube Desk checks certificates and tokens for expiry, credential plugins, removed auth providers, and clusters added twice.</div>
          </div>
        </div>
      ) : (
        <div className="health-list">
          {rows.map(({ c, issues }) => (
            <div key={c.id} className="health-cluster">
              <div className="health-cluster-head">
                <ClusterTile c={c} size={22} />
                <b>{c.name}</b>
                <span className="faint small mono">{c.context}</span>
                <div className="spacer" />
                <Button size="sm" variant="ghost" onClick={() => openDialog({ kind: "settings", cluster: c.id })}>
                  Settings
                </Button>
              </div>
              <IssueList issues={issues} />
            </div>
          ))}
        </div>
      )}
    </Modal>
  );
}

const rank = (issues: HealthIssue[]) => (issues.some((i) => i.level === "bad") ? 0 : issues.some((i) => i.level === "warn") ? 1 : 2);
