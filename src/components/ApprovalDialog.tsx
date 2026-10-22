import { useEffect, useState } from "react";
import { create } from "zustand";
import { Bot, Info, OctagonAlert, ShieldAlert, TriangleAlert } from "lucide-react";
import { Agents, events, type ApprovalRequest } from "../mygo";
import { toast } from "../lib/store";
import { Badge, Button, Field, Modal } from "./ui";
import { DiffView } from "./DiffView";

interface ApprovalStore {
  pending: ApprovalRequest[];
  init(): void;
}

let started = false;

/** useApprovals holds the changes agents proposed, waiting for the user. */
export const useApprovals = create<ApprovalStore>((set) => ({
  pending: [],
  init() {
    if (started) return;
    started = true;
    events.agentsApprovals.on((pending) => set({ pending }));
    Agents.pending().then((pending) => set({ pending }), () => {});
  },
}));

/** ApprovalDialog asks the user about the oldest change an agent proposed. */
export function ApprovalDialog() {
  const pending = useApprovals((s) => s.pending);
  const req = pending[0];
  if (!req) return null;
  return <Approval key={req.id} req={req} more={pending.length - 1} />;
}

function Approval({ req, more }: { req: ApprovalRequest; more: number }) {
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [left, setLeft] = useState(() => Date.parse(req.expires) - Date.now());
  useEffect(() => {
    const t = setInterval(() => setLeft(Date.parse(req.expires) - Date.now()), 1000);
    return () => clearInterval(t);
  }, [req.expires]);
  const allowed = !req.production || typed.trim() === req.clusterName;

  const decide = async (approve: boolean) => {
    if (approve && !allowed) return;
    setBusy(true);
    try {
      await Agents.decide(req.id, approve);
      toast.info(approve ? "Approved" : "Rejected", req.summary);
    } catch (e) {
      toast.error("Could not answer", e);
    } finally {
      setBusy(false);
    }
  };

  const mins = Math.max(0, Math.floor(left / 60000));
  const secs = Math.max(0, Math.floor((left % 60000) / 1000));
  return (
    <Modal
      title={
        <span className="row gap">
          <Bot size={18} /> {req.client} asks to {req.action}
        </span>
      }
      onClose={() => decide(false)}
      width={760}
      className="approval"
      footer={
        <>
          <span className="faint small">
            {more > 0 ? `${more} more waiting · ` : ""}expires in {mins}:{String(secs).padStart(2, "0")}
          </span>
          <div className="spacer" />
          <Button variant="ghost" onClick={() => decide(false)} disabled={busy}>
            Reject
          </Button>
          <Button variant={req.action === "delete" || req.production ? "danger" : "primary"} onClick={() => decide(true)} disabled={!allowed} busy={busy}>
            Approve
          </Button>
        </>
      }
    >
      <div className="approval-body">
        <div className="approval-what">
          <span className="approval-summary">{req.summary}</span>
          <Badge tone={req.production ? "bad" : "info"}>{req.clusterName}</Badge>
        </div>
        {req.reason && (
          <blockquote className="approval-reason">
            <span className="faint small">Why, says {req.client}:</span>
            <div>{req.reason}</div>
          </blockquote>
        )}
        {req.impact && req.impact.length > 0 && (
          <div className="impact">
            <div className="impact-title">What this affects</div>
            <ul className="impact-list">
              {req.impact.map((it, i) => (
                <li key={i} className={`impact-item ${it.severity}`}>
                  {it.severity === "danger" ? <OctagonAlert size={14} /> : it.severity === "warning" ? <TriangleAlert size={14} /> : <Info size={14} />}
                  <span>{it.text}</span>
                </li>
              ))}
            </ul>
          </div>
        )}
        {req.diffs?.map((d, i) => (
          <div key={i} className="approval-diff">
            <div className="approval-diff-head">
              <Badge tone={d.action === "create" ? "ok" : d.action === "unchanged" ? "muted" : "info"}>{d.action}</Badge>
              <span className="mono small">
                {d.kind} {d.namespace ? `${d.namespace}/` : ""}
                {d.name}
              </span>
            </div>
            {d.action !== "unchanged" && <DiffView before={d.before} after={d.after} />}
          </div>
        ))}
        {req.production && (
          <>
            <div className="prod-warning">
              <ShieldAlert size={16} />
              <span>
                This is the production cluster <b>{req.clusterName}</b>.
              </span>
            </div>
            <Field label={`Type ${req.clusterName} to approve`}>
              <input value={typed} onChange={(e) => setTyped(e.target.value)} placeholder={req.clusterName} className="mono" autoFocus autoComplete="off" spellCheck={false} />
            </Field>
          </>
        )}
      </div>
    </Modal>
  );
}
