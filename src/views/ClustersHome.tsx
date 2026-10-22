import { useEffect, useState } from "react";
import { AlertTriangle, Eye, MoreHorizontal, Plus, RefreshCw, Search } from "lucide-react";
import { AppInfo, Clusters, type About, type Cluster } from "../mygo";
import { sourceLabel } from "../lib/clusterLook";
import { run, useApp } from "../lib/store";
import { Badge, Button, Empty, IconButton, Kbd, mod } from "../components/ui";
import { ClusterTile, clusterMenu } from "../components/ClusterRail";
import { ContextMenu } from "../components/Menu";

export function ClustersHome() {
  const { clusters, status, sourceErrors, activate, openDialog, checkStatus, refresh } = useApp();
  const [filter, setFilter] = useState("");
  const [menu, setMenu] = useState<{ c: Cluster; x: number; y: number } | null>(null);
  const f = filter.toLowerCase();
  const visible = clusters.filter((c) => !c.hidden && (!f || `${c.name} ${c.context} ${c.server}`.toLowerCase().includes(f)));
  const hidden = clusters.filter((c) => c.hidden);

  return (
    <div className="page home">
      <header className="page-header">
        <div>
          <h1>Clusters</h1>
          <div className="muted small">
            {clusters.filter((c) => !c.hidden).length} clusters · switch with <Kbd>{mod}1</Kbd>…<Kbd>{mod}9</Kbd> or <Kbd>{mod}K</Kbd>
          </div>
        </div>
        <div className="row gap">
          <div className="search-input">
            <Search size={14} />
            <input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="Filter clusters" />
          </div>
          <Button icon={<RefreshCw size={14} />} onClick={() => clusters.forEach((c) => !c.hidden && checkStatus(c.id))}>
            Check all
          </Button>
          <Button variant="primary" icon={<Plus size={14} />} onClick={() => openDialog({ kind: "add" })}>
            Add cluster
          </Button>
        </div>
      </header>

      {sourceErrors.map((e) => (
        <div key={e.source} className="list-warning">
          <AlertTriangle size={14} /> Could not read <span className="mono">{e.source}</span>: {e.error}
        </div>
      ))}

      {clusters.length === 0 ? (
        <Empty title="No clusters yet" icon={<Plus size={28} />}>
          <p>Add a kubeconfig file, paste one, or connect with a server address and a token.</p>
          <Button variant="primary" onClick={() => openDialog({ kind: "add" })}>
            Add a cluster
          </Button>
        </Empty>
      ) : (
        <div className="cluster-grid">
          {visible.map((c) => {
            const st = status[c.id];
            return (
              <div
                key={c.id}
                className="cluster-card"
                role="button"
                tabIndex={0}
                onClick={() => activate(c.id)}
                onKeyDown={(e) => e.key === "Enter" && activate(c.id)}
                onContextMenu={(e) => {
                  e.preventDefault();
                  setMenu({ c, x: e.clientX, y: e.clientY });
                }}
              >
                <div className="cluster-card-head">
                  <ClusterTile c={c} size={40} />
                  <div className="cluster-card-title">
                    <div className="cluster-card-name" title={c.name}>
                      {c.name}
                    </div>
                    <div className="faint small mono" title={c.server}>
                      {c.server}
                    </div>
                  </div>
                  <IconButton
                    label="More"
                    onClick={(e) => {
                      e.stopPropagation();
                      setMenu({ c, x: e.clientX, y: e.clientY });
                    }}
                  >
                    <MoreHorizontal size={16} />
                  </IconButton>
                </div>
                <div className="cluster-card-meta">
                  {st?.checking ? (
                    <Badge tone="muted">Checking…</Badge>
                  ) : st?.ok ? (
                    <Badge tone="ok" dot>
                      {st.version}
                    </Badge>
                  ) : st?.error ? (
                    <Badge tone="bad" dot>
                      Unreachable
                    </Badge>
                  ) : (
                    <Badge tone="muted">Not checked</Badge>
                  )}
                  {c.production && <span className="env-pill prod">PRODUCTION</span>}
                  {c.readOnly && <span className="env-pill ro">READ-ONLY</span>}
                  <Badge tone="muted">{sourceLabel(c)}</Badge>
                  {c.current && <Badge tone="info">current context</Badge>}
                  {c.name !== c.context && <span className="faint small mono" title="Context">{c.context}</span>}
                </div>
                {st?.error && !st.checking && (
                  <div className="cluster-card-error" title={st.error}>
                    <span>{st.error}</span>
                  </div>
                )}
              </div>
            );
          })}
          <button className="cluster-card add-card" onClick={() => openDialog({ kind: "add" })}>
            <Plus size={22} />
            <span>Add cluster</span>
          </button>
        </div>
      )}

      {hidden.length > 0 && (
        <section className="hidden-clusters">
          <h3 className="muted">Hidden ({hidden.length})</h3>
          {hidden.map((c) => (
            <div key={c.id} className="candidate">
              <div className="row gap">
                <ClusterTile c={c} size={24} />
                <div>
                  <div>{c.name}</div>
                  <div className="faint small mono">{c.server}</div>
                </div>
              </div>
              <Button
                size="sm"
                icon={<Eye size={14} />}
                onClick={async () => {
                  await run("Could not show", () => Clusters.setHidden(c.id, false));
                  await refresh();
                  checkStatus(c.id);
                }}
              >
                Show
              </Button>
            </div>
          ))}
        </section>
      )}
      <AboutFooter />
      {menu && <ContextMenu x={menu.x} y={menu.y} items={clusterMenu(menu.c)} onClose={() => setMenu(null)} />}
    </div>
  );
}

function AboutFooter() {
  const [about, setAbout] = useState<About | null>(null);
  useEffect(() => {
    AppInfo.about().then(setAbout, () => {});
  }, []);
  if (!about) return null;
  return (
    <footer className="about-footer faint small">
      Kube Desk {about.version}
      {" · "}
      {about.canUpdate ? (
        <button className="link" onClick={() => AppInfo.checkForUpdates()}>
          Check for updates
        </button>
      ) : (
        <span title="Development builds, and apps in a folder they cannot write to, do not update themselves">updates off in this build</span>
      )}
    </footer>
  );
}
