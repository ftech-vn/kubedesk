import { useEffect, useState } from "react";
import { Bot, Copy, Eye, EyeOff, RefreshCw } from "lucide-react";
import { Agents, events, type AgentAccess, type AgentActivity, type AgentInfo } from "../mygo";
import { toast, useApp } from "../lib/store";
import { age } from "../lib/k8s";
import { Badge, Button, Modal, Spinner, Tabs } from "./ui";
import { ClusterTile } from "./ClusterRail";

type Client = "claude" | "claude-stdio" | "json" | "codex";

function quote(s: string) {
  return /[\s"'$]/.test(s) ? `"${s.replace(/(["\\$`])/g, "\\$1")}"` : s;
}

function snippet(info: AgentInfo, client: Client, showToken: boolean): string {
  const token = showToken ? info.token : "••••••••";
  const cmd = [info.command, ...info.args];
  switch (client) {
    case "claude":
      return `claude mcp add --transport http kubedesk ${info.url} \\\n  --header "Authorization: Bearer ${token}"`;
    case "claude-stdio":
      return `claude mcp add kubedesk -- ${cmd.map(quote).join(" ")}`;
    case "json":
      return JSON.stringify({ mcpServers: { kubedesk: { command: info.command, args: info.args } } }, null, 2);
    case "codex":
      return `[mcp_servers.kubedesk]\ncommand = ${JSON.stringify(info.command)}\nargs = ${JSON.stringify(info.args)}`;
  }
}

const clientHelp: Record<Client, string> = {
  claude: "Run in a terminal. Claude Code connects over HTTP with the token.",
  "claude-stdio": "Or let Claude Code start Kube Desk's bridge, which reads the token from Kube Desk's data folder.",
  json: "For Cursor (~/.cursor/mcp.json), Claude Desktop (claude_desktop_config.json) and other clients that start servers as processes.",
  codex: "For Codex, in ~/.codex/config.toml.",
};

/** AgentsDialog sets up the MCP server through which AI agents reach clusters. */
export function AgentsDialog({ onClose }: { onClose(): void }) {
  const clusters = useApp((s) => s.clusters.filter((c) => !c.hidden));
  const refresh = useApp((s) => s.refresh);
  const [info, setInfo] = useState<AgentInfo | null>(null);
  const [busy, setBusy] = useState(false);
  const [client, setClient] = useState<Client>("claude");
  const [showToken, setShowToken] = useState(false);
  const [activity, setActivity] = useState<AgentActivity[]>([]);

  useEffect(() => {
    Agents.info().then(setInfo, (e) => toast.error("Could not read the agent settings", e));
    Agents.activity().then(setActivity, () => {});
    return events.agentsActivity.on(setActivity);
  }, []);

  const toggle = async (on: boolean) => {
    setBusy(true);
    try {
      setInfo(await Agents.setEnabled(on));
    } catch (e) {
      toast.error("Could not change the MCP server", e);
    } finally {
      setBusy(false);
    }
  };

  const setAccess = async (id: string, access: AgentAccess) => {
    try {
      await Agents.setAccess(id, access);
      await refresh();
    } catch (e) {
      toast.error("Could not change the access", e);
    }
  };

  const copy = (text: string, what: string) =>
    navigator.clipboard.writeText(text).then(
      () => toast.ok(`${what} copied`),
      (e) => toast.error("Could not copy", e),
    );

  return (
    <Modal
      title={
        <span className="row gap">
          <Bot size={18} /> AI agents (MCP)
        </span>
      }
      onClose={onClose}
      width={720}
    >
      {!info ? (
        <div className="center-pad">
          <Spinner />
        </div>
      ) : (
        <div className="agents">
          <p className="muted small agents-intro">
            Let Claude Code, Cursor, Codex and other MCP clients read the clusters you choose, and propose changes. Every change waits for you here, with a diff and what it affects.
            Secret values and tokens in logs are always masked.
          </p>
          <label className="toggle">
            <input type="checkbox" checked={info.enabled} disabled={busy} onChange={(e) => toggle(e.target.checked)} />
            <span>
              <b>MCP server</b>
              <span className="faint small">
                {info.enabled ? (info.running ? `Listening on ${info.url}, for this Mac only` : (info.error ?? "Starting…")) : "Off: no agent can reach your clusters through Kube Desk"}
              </span>
            </span>
          </label>

          {info.enabled && (
            <section>
              <div className="field-label">Connect an agent</div>
              <Tabs<Client>
                tabs={[
                  { id: "claude", label: "Claude Code" },
                  { id: "claude-stdio", label: "Claude Code (stdio)" },
                  { id: "json", label: "Cursor / Claude Desktop" },
                  { id: "codex", label: "Codex" },
                ]}
                value={client}
                onChange={setClient}
              />
              <p className="faint small">{clientHelp[client]}</p>
              <div className="snippet">
                <pre className="selectable">{snippet(info, client, showToken)}</pre>
                <div className="snippet-actions">
                  {client === "claude" && (
                    <button className="icon-btn" title={showToken ? "Hide the token" : "Show the token"} onClick={() => setShowToken(!showToken)}>
                      {showToken ? <EyeOff size={14} /> : <Eye size={14} />}
                    </button>
                  )}
                  <button className="icon-btn" title="Copy" onClick={() => copy(snippet(info, client, true), "Command")}>
                    <Copy size={14} />
                  </button>
                </div>
              </div>
              <button
                className="link small"
                onClick={async () => {
                  setInfo(await Agents.regenerateToken());
                  toast.info("New token", "Agents set up with the old token must be added again.");
                }}
              >
                <RefreshCw size={12} /> New token
              </button>
            </section>
          )}

          <section>
            <div className="field-label">What agents may do, by cluster</div>
            <div className="access-list">
              {clusters.map((c) => (
                <div key={c.id} className="access-row">
                  <ClusterTile c={c} size={22} />
                  <span className="access-name">
                    {c.name}
                    {c.production && <span className="env-pill prod">PRODUCTION</span>}
                    {c.readOnly && <span className="env-pill ro">READ-ONLY</span>}
                  </span>
                  <div className="segmented">
                    {([
                      ["", "Off"],
                      ["read", "Read"],
                      ["write", "Propose changes"],
                    ] as [AgentAccess, string][]).map(([v, label]) => (
                      <button key={v} className={c.agent === v ? "on" : ""} onClick={() => setAccess(c.id, v)} disabled={v === "write" && c.readOnly} title={v === "write" && c.readOnly ? "Read-only clusters take no changes" : undefined}>
                        {label}
                      </button>
                    ))}
                  </div>
                </div>
              ))}
            </div>
          </section>

          <section>
            <div className="field-label">Recent activity</div>
            {activity.length === 0 ? (
              <div className="faint small">No agent has called Kube Desk yet.</div>
            ) : (
              <div className="activity">
                {activity.slice(0, 60).map((a, i) => (
                  <div key={i} className="activity-row">
                    <span className="faint small mono">{age(a.time)}</span>
                    <span className="small">{a.client}</span>
                    <span className="mono small">{a.tool}</span>
                    <span className="small muted activity-detail" title={a.detail}>
                      {a.cluster ? `${a.cluster} · ` : ""}
                      {a.detail}
                    </span>
                    <Badge tone={a.outcome === "ok" || a.outcome === "approved" ? "ok" : a.outcome === "rejected" || a.outcome === "expired" ? "warn" : "bad"}>{a.outcome}</Badge>
                  </div>
                ))}
              </div>
            )}
          </section>
        </div>
      )}
    </Modal>
  );
}

