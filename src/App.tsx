import { useEffect, useMemo, useState } from "react";
import { RefreshCw, ShieldAlert } from "lucide-react";
import { Clusters, type Cluster } from "./mygo";
import { run, useActiveView, useApp } from "./lib/store";
import { byKey, customResourceDef, type ResourceDef } from "./lib/resources";
import { useWatch } from "./lib/watch";
import { errorMessage } from "./lib/k8s";
import { TitleBar } from "./components/TitleBar";
import { ClusterRail, reconnect } from "./components/ClusterRail";
import { Sidebar } from "./components/Sidebar";
import { AskDialog, Dialogs, Toasts } from "./components/Overlays";
import { Button, Empty, ErrorBox, Spinner } from "./components/ui";
import { ErrorBoundary } from "./components/ErrorBoundary";
import { useAlerts } from "./lib/alerts";
import { ApprovalDialog, useApprovals } from "./components/ApprovalDialog";
import { AssistantPanel } from "./components/AssistantPanel";
import { useAssistant } from "./lib/assistant";
import { ClustersHome } from "./views/ClustersHome";
import { ClusterOverview } from "./views/ClusterOverview";
import { ChangesView } from "./views/ChangesView";
import { ResourceList } from "./views/ResourceList";
import { DetailsPanel } from "./views/details/DetailsPanel";

export function App() {
  const { loaded, active, load } = useApp();
  const [error, setError] = useState("");

  useEffect(() => {
    load()
      .then(() => {
        useAlerts.getState().init();
        useApprovals.getState().init();
      })
      .catch((e) => setError(errorMessage(e)));
  }, [load]);

  useHotkeys();

  if (error) {
    return (
      <div className="app">
        <TitleBar />
        <div className="page">
          <ErrorBox title="Could not start" message={error} />
        </div>
      </div>
    );
  }

  return (
    <div className="app">
      <TitleBar />
      <div className="app-body">
        <ClusterRail />
        <main className="workspace-host">
          {!loaded ? (
            <div className="center-pad">
              <Spinner />
            </div>
          ) : (
            <>
              {active === null ? (
                <ClustersHome />
              ) : (
                // Each cluster keeps its page, namespace and open object in the
                // store, and its lists stay watched for a while, so switching
                // back to a cluster shows it as it was at once.
                <div key={active} className="workspace">
                  <Workspace cluster={active} />
                </div>
              )}
            </>
          )}
        </main>
        <ErrorBoundary resetKey="assistant">
          <AssistantPanel />
        </ErrorBoundary>
      </div>
      <OverlayBoundary>
        <Dialogs />
      </OverlayBoundary>
      <OverlayBoundary>
        <AskDialog />
      </OverlayBoundary>
      <OverlayBoundary>
        <ApprovalDialog />
      </OverlayBoundary>
      <Toasts />
    </div>
  );
}

/** OverlayBoundary keeps a dialog that crashes from blanking the app: it
 * shows the error, and closing it brings the app back. */
function OverlayBoundary({ children }: { children: React.ReactNode }) {
  const dialog = useApp((s) => s.dialog);
  return (
    <ErrorBoundary
      resetKey={JSON.stringify(dialog)}
      fallback={(error, reset) => (
        <div className="modal-backdrop">
          <div className="modal" style={{ width: 560 }}>
            <ErrorBox title="This dialog crashed" message={error.stack ?? error.message} />
            <div className="modal-footer">
              <Button
                variant="primary"
                onClick={() => {
                  useApp.getState().openDialog(null);
                  reset();
                }}
              >
                Close
              </Button>
            </div>
          </div>
        </div>
      )}
    >
      {children}
    </ErrorBoundary>
  );
}

function Workspace({ cluster }: { cluster: string }) {
  const view = useApp((s) => s.views[cluster]);
  const status = useApp((s) => s.status[cluster]);
  const cl = useApp((s) => s.clusters.find((c) => c.id === cluster));
  if (!view || !cl) return null;
  return (
    <>
      <Sidebar cluster={cluster} />
      <section className="content">
        {status?.error && !status.ok && !status.checking && (
          <div className="offline-bar">
            <span>
              Cannot reach <b>{cl.name}</b>: {status.error}
            </span>
            <Button size="sm" icon={<RefreshCw size={13} />} onClick={() => reconnect(cluster)}>
              Retry
            </Button>
          </div>
        )}
        <ProductionHint cluster={cl} />
        <ErrorBoundary resetKey={view.section}>
          <Section cluster={cluster} section={view.section} />
        </ErrorBoundary>
      </section>
      {view.details.length > 0 && (
        <ErrorBoundary resetKey={JSON.stringify(view.details[view.details.length - 1])}>
          <DetailsPanel cluster={cluster} />
        </ErrorBoundary>
      )}
    </>
  );
}

const HINT_KEY = "production-hint-dismissed";

/** ProductionHint offers to protect clusters whose names say production. */
function ProductionHint({ cluster }: { cluster: Cluster }) {
  const [dismissed, setDismissed] = useState(() => {
    try {
      return (JSON.parse(localStorage.getItem(HINT_KEY) ?? "[]") as string[]).includes(cluster.id);
    } catch {
      return false;
    }
  });
  const looksProd = /(^|[^a-z])(prod|prd|production|live)([^a-z]|$)/i.test(`${cluster.name} ${cluster.context}`);
  if (!looksProd || cluster.production || dismissed) return null;
  const dismiss = () => {
    setDismissed(true);
    try {
      const list = JSON.parse(localStorage.getItem(HINT_KEY) ?? "[]") as string[];
      localStorage.setItem(HINT_KEY, JSON.stringify([...list, cluster.id]));
    } catch {
      /* storage unavailable */
    }
  };
  const mark = async () => {
    await run("Could not update the cluster", () =>
      Clusters.update(cluster.id, { alias: cluster.name !== cluster.context ? cluster.name : undefined, color: cluster.color, hidden: cluster.hidden, production: true, readOnly: cluster.readOnly }),
    );
    await useApp.getState().refresh();
  };
  return (
    <div className="hint-bar">
      <ShieldAlert size={15} />
      <span>
        <b>{cluster.name}</b> looks like a production cluster. Protect it, so that every change asks you to type its name?
      </span>
      <Button size="sm" variant="primary" onClick={mark}>
        Mark as production
      </Button>
      <Button size="sm" variant="ghost" onClick={dismiss}>
        It isn't
      </Button>
    </div>
  );
}

function Section({ cluster, section }: { cluster: string; section: string }) {
  if (section === "overview") return <ClusterOverview cluster={cluster} />;
  if (section === "changes") return <ChangesView cluster={cluster} />;
  const def = byKey.get(section);
  if (def) return <ResourceList key={def.key} cluster={cluster} def={def} />;
  if (section.startsWith("cr:")) return <CustomResourceSection cluster={cluster} section={section} />;
  return <Empty title="Unknown page" />;
}

function CustomResourceSection({ cluster, section }: { cluster: string; section: string }) {
  const crds = useWatch(cluster, byKey.get("crds")!.gvr);
  const def = useMemo<ResourceDef | undefined>(() => {
    for (const crd of crds.items) {
      const d = customResourceDef(crd);
      if (d.key === section) return d;
    }
    return undefined;
  }, [crds.items, section]);
  if (!def) return crds.loading ? <div className="center-pad"><Spinner /></div> : <Empty title="This custom resource is no longer defined" />;
  return <ResourceList key={def.key} cluster={cluster} def={def} />;
}

function useHotkeys() {
  const view = useActiveView();
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const s = useApp.getState();
      const modKey = e.metaKey || e.ctrlKey;
      if (modKey && e.key.toLowerCase() === "j") {
        e.preventDefault();
        useAssistant.getState().toggle();
        return;
      }
      if (modKey && e.shiftKey && e.key.toLowerCase() === "f") {
        e.preventDefault();
        s.openDialog({ kind: "search" });
        return;
      }
      if (modKey && e.key.toLowerCase() === "k") {
        e.preventDefault();
        s.openDialog(s.dialog?.kind === "palette" ? null : { kind: "palette" });
        return;
      }
      if (modKey && /^[1-9]$/.test(e.key)) {
        const visible = s.clusters.filter((c) => !c.hidden);
        const c = visible[Number(e.key) - 1];
        if (c) {
          e.preventDefault();
          s.activate(c.id);
        }
        return;
      }
      if (modKey && e.key === "0") {
        e.preventDefault();
        s.activate(null);
        return;
      }
      if (modKey && e.key === "f" && !document.querySelector(".modal-backdrop")) {
        const input = document.querySelector<HTMLInputElement>(".list-header .search-input input");
        if (input && !(e.target as HTMLElement).closest(".cm-editor, .xterm, .logs")) {
          e.preventDefault();
          input.focus();
          input.select();
        }
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [view]);
}
