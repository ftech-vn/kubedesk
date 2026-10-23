import { useEffect, useMemo, useRef, useState } from "react";
import { ArrowRightLeft, Bot, Bell, Sparkles, BellOff, BellRing, Check, ChevronDown, Eye, ExternalLink, LayoutGrid, Monitor, Moon, Search, Square, Sun } from "lucide-react";
import { Alerts, type Alert } from "../mygo";
import { isWatched, openRequest, toggleWatch, useAlerts } from "../lib/alerts";
import { useApprovals } from "./ApprovalDialog";
import { useAssistant } from "../lib/assistant";
import { age } from "../lib/k8s";
import type { Theme } from "../mygo";
import { Kube } from "../mygo";
import { usePortForwards } from "../lib/actions";
import { toast, useActiveCluster, useActiveView, useApp } from "../lib/store";
import { useWatch } from "../lib/watch";
import { clusterColor } from "../lib/clusterLook";
import { Popover } from "./Menu";
import { IconButton, Kbd, mod } from "./ui";

export function TitleBar() {
  const cluster = useActiveCluster();
  const status = useApp((s) => (cluster ? s.status[cluster.id] : undefined));
  const openDialog = useApp((s) => s.openDialog);
  return (
    <header className={`titlebar ${cluster?.production ? "is-prod" : ""}`}>
      <div className="titlebar-left">
        <span className="app-name">Kube Desk</span>
        {cluster && (
          <>
            <span className="crumb-sep">/</span>
            <button className="crumb cluster-crumb" onClick={() => openDialog({ kind: "palette" })} title="Switch cluster">
              <span className="crumb-dot" style={{ background: clusterColor(cluster) }} />
              <span className="crumb-text">{cluster.name}</span>
              {status?.error && !status.checking && <span className="text-bad small">offline</span>}
              <ChevronDown size={13} className="muted" />
            </button>
            {cluster.production && <span className="env-pill prod" title="Production: changes ask you to type the cluster's name">PRODUCTION</span>}
            {cluster.readOnly && <span className="env-pill ro" title="Kube Desk refuses changes to this cluster">READ-ONLY</span>}
            <span className="crumb-sep">/</span>
            <NamespacePicker cluster={cluster.id} />
            <WatchToggle cluster={cluster.id} />
          </>
        )}
      </div>
      <button className="palette-trigger" onClick={() => openDialog({ kind: "palette" })}>
        <Search size={13} />
        <span>Jump to cluster, resource or namespace…</span>
        <Kbd>{mod}K</Kbd>
      </button>
      <div className="titlebar-right">
        <PortForwardsButton />
        <AssistantButton />
        <AgentsButton />
        <AlertsButton />
        <ThemeSwitch />
      </div>
    </header>
  );
}

function NamespacePicker({ cluster }: { cluster: string }) {
  const view = useActiveView();
  const setNamespace = useApp((s) => s.setNamespace);
  const { items, error } = useWatch(cluster, { group: "", version: "v1", resource: "namespaces" });
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState("");
  const anchor = useRef<HTMLButtonElement>(null);
  const names = useMemo(() => items.map((n) => n.metadata.name).sort(), [items]);
  const shown = names.filter((n) => n.includes(q.toLowerCase()));
  const ns = view?.namespace ?? "";

  const choose = (n: string) => {
    setNamespace(n);
    setOpen(false);
    setQ("");
  };

  return (
    <>
      <button ref={anchor} className="crumb" onClick={() => setOpen(!open)} title="Namespace">
        <LayoutGrid size={13} className="muted" />
        <span className="crumb-text">{ns || "All namespaces"}</span>
        <ChevronDown size={13} className="muted" />
      </button>
      {open && (
        <Popover anchor={anchor.current} onClose={() => setOpen(false)} width={280}>
          <div className="picker">
            <input
              autoFocus
              className="picker-search"
              placeholder="Find or type a namespace"
              value={q}
              onChange={(e) => setQ(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") choose(shown[0] ?? q.trim());
              }}
            />
            <div className="picker-list">
              <button className={`picker-item ${ns === "" ? "on" : ""}`} onClick={() => choose("")}>
                {ns === "" ? <Check size={13} /> : <span className="w13" />} All namespaces
              </button>
              {shown.map((n) => (
                <button key={n} className={`picker-item ${ns === n ? "on" : ""}`} onClick={() => choose(n)}>
                  {ns === n ? <Check size={13} /> : <span className="w13" />} {n}
                </button>
              ))}
              {error && !names.length && <div className="faint small pad">Cannot list namespaces; type one and press Enter.</div>}
            </div>
          </div>
        </Popover>
      )}
    </>
  );
}

function PortForwardsButton() {
  const { list, refresh, stop } = usePortForwards();
  const clusters = useApp((s) => s.clusters);
  const [open, setOpen] = useState(false);
  const anchor = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    refresh().catch(() => {});
    const t = setInterval(() => refresh().catch(() => {}), 10000);
    return () => clearInterval(t);
  }, [refresh]);
  if (!list.length) return null;
  return (
    <>
      <button ref={anchor} className="crumb pf-button" onClick={() => setOpen(!open)} title="Port forwards">
        <ArrowRightLeft size={13} />
        <span>{list.length}</span>
      </button>
      {open && (
        <Popover anchor={anchor.current} onClose={() => setOpen(false)} align="right" width={360}>
          <div className="picker">
            <div className="picker-title">Port forwards</div>
            {list.map((pf) => (
              <div key={pf.id} className="pf-item">
                <div>
                  <div className="mono small">
                    localhost:{pf.localPort} → {pf.remotePort}
                  </div>
                  <div className="faint small">
                    {clusters.find((c) => c.id === pf.cluster)?.name} · {pf.namespace}/{pf.pod}
                  </div>
                  {pf.error && <div className="text-bad small">{pf.error}</div>}
                </div>
                <div className="row">
                  <IconButton label="Open in browser" onClick={() => Kube.openURL(`http://localhost:${pf.localPort}`)}>
                    <ExternalLink size={14} />
                  </IconButton>
                  <IconButton label="Stop" onClick={() => stop(pf.id)}>
                    <Square size={13} />
                  </IconButton>
                </div>
              </div>
            ))}
          </div>
        </Popover>
      )}
    </>
  );
}

const themes: { id: Theme; label: string; icon: React.ReactNode }[] = [
  { id: "system", label: "Match the system", icon: <Monitor size={13} /> },
  { id: "light", label: "Light", icon: <Sun size={13} /> },
  { id: "dark", label: "Dark", icon: <Moon size={13} /> },
];

function ThemeSwitch() {
  const theme = useApp((s) => s.theme);
  const setTheme = useApp((s) => s.setTheme);
  return (
    <div className="theme-switch" role="radiogroup" aria-label="Appearance">
      {themes.map((t) => (
        <button key={t.id} role="radio" aria-checked={theme === t.id} className={theme === t.id ? "on" : ""} title={t.label} onClick={() => setTheme(t.id)}>
          {t.icon}
        </button>
      ))}
    </div>
  );
}

/** WatchToggle watches the namespace shown, or the whole cluster. */
function WatchToggle({ cluster }: { cluster: string }) {
  const ns = useActiveView()?.namespace ?? "";
  const watched = useAlerts((s) => isWatched(s.state, cluster, ns));
  const what = ns ? `namespace ${ns}` : "the whole cluster";
  return (
    <button
      className={`crumb watch-toggle ${watched ? "on" : ""}`}
      onClick={() => toggleWatch(cluster, ns)}
      title={watched ? `Stop watching ${what}` : `Watch ${what}: get a notification when pods crash, run out of memory or fail to pull images, rollouts get stuck${ns ? "" : " or nodes go down"}`}
    >
      {watched ? <BellRing size={13} /> : <Eye size={13} />}
      <span>{watched ? "Watching" : "Watch"}</span>
    </button>
  );
}

function AlertsButton() {
  const state = useAlerts((s) => s.state);
  const [open, setOpen] = useState(false);
  const anchor = useRef<HTMLButtonElement>(null);
  const clusters = useApp((s) => s.clusters);
  if (!state.targets.length) return null;
  const n = state.alerts.length;
  const critical = state.alerts.some((a) => a.severity === "critical");
  const paused = !!state.pausedUntil;
  const go = (a: Alert) => {
    setOpen(false);
    openRequest({ cluster: a.cluster, kind: a.kind, namespace: a.namespace, name: a.name });
  };
  return (
    <>
      <button ref={anchor} className={`crumb alerts-button ${n ? (critical ? "bad" : "warn") : ""}`} onClick={() => setOpen(!open)} title={n ? `${n} problems in watched namespaces` : "Watched namespaces are healthy"}>
        {paused ? <BellOff size={14} /> : <Bell size={14} />}
        {n > 0 && <span className="alerts-count">{n}</span>}
      </button>
      {open && (
        <Popover anchor={anchor.current} onClose={() => setOpen(false)} align="right" width={420}>
          <div className="picker">
            <div className="picker-title row">
              <span className="spacer">{n ? `${n} problem${n === 1 ? "" : "s"}` : "All healthy"}</span>
              {paused ? (
                <button className="link small" onClick={() => Alerts.resume()}>
                  Resume notifications
                </button>
              ) : (
                <button className="link small" onClick={() => Alerts.pause(60)}>
                  Pause for 1 hour
                </button>
              )}
            </div>
            <div className="picker-list alerts-list">
              {state.alerts.map((a) => (
                <div key={a.id} className={`alert-item ${a.severity}`}>
                  <button className="alert-main" onClick={() => go(a)}>
                    <div className="alert-head">
                      <span className="alert-reason">{a.reason}</span>
                      <span className="faint small">
                        {a.clusterName} · {age(a.since)} ago
                      </span>
                    </div>
                    <div className="mono small">
                      {a.kind.toLowerCase()}/{a.namespace ? `${a.namespace}/` : ""}
                      {a.name}
                      {a.container ? ` (${a.container})` : ""}
                    </div>
                    {a.message && <div className="alert-message">{a.message}</div>}
                  </button>
                  <button className="link small alert-mute" onClick={() => Alerts.mute(a.id)} title="Hide until it is fixed">
                    Hide
                  </button>
                </div>
              ))}
              {!n && <div className="faint small pad">Nothing is crash looping, out of memory, failing to pull images or stuck in the namespaces you watch.</div>}
              {Object.entries(state.errors).map(([id, err]) => (
                <div key={id} className="alert-item warning">
                  <div className="alert-main">
                    <div className="alert-head">
                      <span className="alert-reason">Cannot watch {clusters.find((c) => c.id === id)?.name ?? id}</span>
                    </div>
                    <div className="alert-message">{err}</div>
                  </div>
                </div>
              ))}
            </div>
            <div className="alerts-foot">
              <span className="faint small">
                Watching{" "}
                {state.targets
                  .map((t) => `${clusters.find((c) => c.id === t.cluster)?.name ?? "?"}${t.namespace ? `/${t.namespace}` : ""}`)
                  .join(", ")}
              </span>
              <button className="link small" onClick={() => Alerts.testNotification().then(() => toast.ok("Notification sent"), (e) => toast.error("Could not show a notification", e))}>
                Test notification
              </button>
            </div>
          </div>
        </Popover>
      )}
    </>
  );
}

function AgentsButton() {
  const waiting = useApprovals((s) => s.pending.length);
  const openDialog = useApp((s) => s.openDialog);
  return (
    <button className={`crumb agents-button ${waiting ? "waiting" : ""}`} onClick={() => openDialog({ kind: "agents" })} title={waiting ? `${waiting} change${waiting === 1 ? "" : "s"} waiting for your approval` : "AI agents (MCP)"}>
      <Bot size={14} />
      {waiting > 0 && <span className="alerts-count">{waiting}</span>}
    </button>
  );
}

function AssistantButton() {
  const open = useAssistant((s) => s.open);
  const running = useAssistant((s) => s.running);
  return (
    <button className={`crumb assistant-button ${open ? "on" : ""}`} onClick={() => useAssistant.getState().toggle()} title="Assistant (⌘J)">
      <Sparkles size={14} className={running ? "pulse" : ""} />
      <span>Assistant</span>
    </button>
  );
}
