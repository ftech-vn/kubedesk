import { useCallback, useEffect, useMemo, useState } from "react";
import { Activity, Boxes, Copy, Database, ExternalLink, FileCode2, FolderOpen, KeyRound, Layers, Loader2, MessagesSquare, MoreHorizontal, Network, Pencil, Play, Plus, RefreshCw, ScrollText, Search, Trash2, Workflow, X } from "lucide-react";
import { Stacks, type InstalledStack, type Stack, type StackJob } from "../mygo";
import { age, errorMessage, type Tone } from "../lib/k8s";
import { readOnly, typeToConfirm } from "../lib/actions";
import { ask, run, toast, useApp } from "../lib/store";
import { categoryOrder, copySource, useStacks } from "../lib/stacks";
import { useNow } from "../lib/watch";
import { Badge, Button, Empty, ErrorBox, IconButton, Spinner } from "../components/ui";
import { ContextMenu, type MenuItem } from "../components/Menu";

const categoryIcons: Record<string, React.ComponentType<{ size?: number }>> = {
  Logging: ScrollText,
  Monitoring: Activity,
  Database: Database,
  Messaging: MessagesSquare,
  Networking: Network,
  "CI/CD": Workflow,
};

export const stackIcon = (category: string, size = 18) => {
  const Icon = categoryIcons[category] ?? Boxes;
  return <Icon size={size} />;
};

export const stackStatusTone = (s: string): Tone => (s === "installed" ? "ok" : s === "failed" ? "bad" : s === "installing" ? "info" : "muted");

/** StacksView lists the stacks that can run in the cluster, and those it has. */
export function StacksView({ cluster }: { cluster: string }) {
  const { catalog, jobs, installedTick, loadCatalog } = useStacks();
  const openDialog = useApp((s) => s.openDialog);
  const [installed, setInstalled] = useState<InstalledStack[] | null>(null);
  const [error, setError] = useState("");
  const [filter, setFilter] = useState("");
  const [category, setCategory] = useState("");
  const [menu, setMenu] = useState<{ x: number; y: number; items: MenuItem[] } | null>(null);
  const now = useNow(10000);

  const load = useCallback(() => {
    Stacks.installed(cluster).then(
      (list) => {
        setInstalled(list);
        setError("");
      },
      (e) => setError(errorMessage(e)),
    );
  }, [cluster]);

  useEffect(() => {
    loadCatalog().catch((e) => toast.error("Could not read the stacks", e));
  }, [loadCatalog]);
  useEffect(() => {
    load();
    const t = setInterval(load, 20000);
    return () => clearInterval(t);
  }, [load, installedTick]);

  const running = jobs.filter((j) => j.cluster === cluster && j.state === "running");
  const stacks = catalog?.stacks ?? [];
  const categories = useMemo(() => {
    const set = [...new Set(stacks.map((s) => s.category))];
    const rank = (c: string) => (categoryOrder.includes(c) ? categoryOrder.indexOf(c) : 100);
    return set.sort((a, b) => rank(a) - rank(b) || a.localeCompare(b));
  }, [stacks]);
  const f = filter.toLowerCase();
  const shown = stacks
    .filter((s) => (!category || s.category === category) && (!f || `${s.name} ${s.description} ${s.category} ${(s.tags ?? []).join(" ")} ${s.components.map((c) => c.title).join(" ")}`.toLowerCase().includes(f)))
    .sort((a, b) => categories.indexOf(a.category) - categories.indexOf(b.category));

  const runStack = (s: Stack, namespace?: string) => {
    if (readOnly(cluster)) return;
    openDialog({ kind: "stack", cluster, stack: s.id, namespace });
  };

  const stackMenu = (e: React.MouseEvent, s: Stack) => {
    const ids = stacks.map((x) => x.id);
    const items: MenuItem[] = [
      { label: "Run…", icon: <Play size={14} />, run: () => runStack(s) },
      s.source === "user"
        ? { label: "Edit", icon: <Pencil size={14} />, run: () => openDialog({ kind: "stack-editor", id: s.id }) }
        : { label: "View definition", icon: <FileCode2 size={14} />, run: () => openDialog({ kind: "stack-editor", id: s.id, readOnly: true }) },
      {
        label: "Duplicate as my stack",
        icon: <Copy size={14} />,
        run: async () => {
          const src = await run("Could not read the stack", () => Stacks.source(s.id));
          if (src !== undefined) openDialog({ kind: "stack-editor", source: copySource(src, s.id, ids) });
        },
      },
    ];
    if (s.source === "user")
      items.push({
        label: "Delete",
        icon: <Trash2 size={14} />,
        danger: true,
        separatorBefore: true,
        run: async () => {
          const ok = await ask({ title: `Delete the stack ${s.name}?`, message: "Its definition goes; what it installed in clusters stays.", confirmLabel: "Delete", danger: true });
          if (!ok.ok) return;
          await run("Could not delete the stack", () => Stacks.delete(s.id), `Deleted ${s.name}`);
          loadCatalog();
        },
      });
    setMenu({ x: e.clientX, y: e.clientY, items });
  };

  return (
    <div className="page stacks-page">
      <div className="page-header">
        <div>
          <h1>Stacks</h1>
          <div className="muted small">Ready-made sets of apps — logs, metrics, databases… — installed into a namespace in one go.</div>
        </div>
        <div className="row gap">
          <div className="search-input">
            <Search size={14} />
            <input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="Filter stacks…" />
            {filter && (
              <button className="clear" onClick={() => setFilter("")}>
                <X size={13} />
              </button>
            )}
          </div>
          <IconButton label="Open the folder of your stacks" onClick={() => run("Could not open the folder", () => Stacks.reveal())}>
            <FolderOpen size={15} />
          </IconButton>
          <IconButton
            label="Reload"
            onClick={() => {
              loadCatalog();
              load();
            }}
          >
            <RefreshCw size={15} />
          </IconButton>
          <Button icon={<Plus size={14} />} onClick={() => openDialog({ kind: "stack-editor" })}>
            New stack
          </Button>
        </div>
      </div>

      {catalog && catalog.errors.length > 0 && (
        <div className="hint-bar stacks-hint">
          <FileCode2 size={15} />
          <span>
            Some of your stacks could not be read: <span className="mono small">{catalog.errors.join(" · ")}</span>
          </span>
        </div>
      )}
      {catalog && !catalog.helmInstalled && stacks.some((s) => s.needsHelm) && (
        <div className="hint-bar stacks-hint">
          <Layers size={15} />
          <span>
            Stacks marked <b>Helm</b> install charts with the helm command, which is not installed: <span className="mono">brew install helm</span>. The others need nothing.
          </span>
        </div>
      )}

      {(running.length > 0 || (installed && installed.length > 0)) && (
        <section className="stacks-section">
          <div className="section-title">In this cluster</div>
          <div className="installed-list">
            {running.map((j) => (
              <RunningRow key={j.id} job={j} onOpen={() => openDialog({ kind: "stack", cluster, stack: j.stack, namespace: j.namespace, job: j.id })} />
            ))}
            {installed
              ?.filter((is) => !running.some((j) => j.stack === is.id && j.namespace === is.namespace))
              .map((is) => (
                <InstalledRow
                  key={`${is.namespace}/${is.id}`}
                  cluster={cluster}
                  is={is}
                  now={now}
                  category={stacks.find((s) => s.id === is.id)?.category ?? ""}
                  onRun={() => {
                    const s = stacks.find((x) => x.id === is.id);
                    if (s) runStack(s, is.namespace);
                  }}
                  onChanged={load}
                />
              ))}
          </div>
        </section>
      )}
      {error && <ErrorBox title="Could not read the stacks of this cluster" message={error} action={<Button onClick={load}>Retry</Button>} />}

      <section className="stacks-section">
        <div className="stacks-tabs">
          <button className={!category ? "on" : ""} onClick={() => setCategory("")}>
            All
          </button>
          {categories.map((c) => (
            <button key={c} className={category === c ? "on" : ""} onClick={() => setCategory(c)}>
              {c}
            </button>
          ))}
        </div>
        {!catalog ? (
          <div className="center-pad">
            <Spinner />
          </div>
        ) : !shown.length ? (
          <Empty icon={<Boxes size={28} />} title="No stack matches">
            Make your own with <b>New stack</b>.
          </Empty>
        ) : (
          <div className="stack-grid">
            {shown.map((s) => {
              const count = installed?.filter((i) => i.id === s.id && i.status === "installed").length ?? 0;
              return (
                <div key={s.id} className="stack-card" onContextMenu={(e) => (e.preventDefault(), stackMenu(e, s))}>
                  <div className="stack-card-head">
                    <div className={`stack-icon cat-${s.category.replace(/\W/g, "").toLowerCase()}`}>{stackIcon(s.category)}</div>
                    <div className="stack-card-title">
                      <b>{s.name}</b>
                      <div className="row gap-sm">
                        <span className="faint small">{s.category}</span>
                        {s.source === "user" && <Badge tone="info">Yours</Badge>}
                        {s.needsHelm && <Badge tone="muted">Helm</Badge>}
                        {count > 0 && <Badge tone="ok">{count === 1 ? "Installed" : `Installed ×${count}`}</Badge>}
                      </div>
                    </div>
                    <IconButton label="More" onClick={(e) => stackMenu(e, s)}>
                      <MoreHorizontal size={15} />
                    </IconButton>
                  </div>
                  <p className="stack-desc">{s.description}</p>
                  <div className="chips">
                    {s.components.map((c) => (
                      <span key={c.name} className="chip" title={c.helm ? `Helm chart ${c.helm.chart}` : c.urls?.length ? "Manifests from URLs" : "Manifest"}>
                        <span className="chip-val">{(c.title ?? c.name).replace(/\s*\(.*\)$/, "")}</span>
                      </span>
                    ))}
                  </div>
                  <div className="stack-card-foot">
                    <span className="faint small" title={s.requires}>
                      {s.requires}
                    </span>
                    <Button size="sm" variant="primary" icon={<Play size={13} />} onClick={() => runStack(s)}>
                      Run
                    </Button>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </section>
      {menu && <ContextMenu x={menu.x} y={menu.y} items={menu.items} onClose={() => setMenu(null)} />}
    </div>
  );
}

function RunningRow({ job, onOpen }: { job: StackJob; onOpen(): void }) {
  const done = job.steps.filter((s) => s.status === "done" || s.status === "skipped").length;
  const current = job.steps.find((s) => s.status === "running");
  return (
    <button className="installed-row running" onClick={onOpen}>
      <Loader2 size={16} className="spin" />
      <div className="installed-main">
        <div>
          <b>{job.name}</b> <span className="muted small">{job.action === "install" ? "installing into" : "being removed from"}</span> <span className="mono small">{job.namespace}</span>
        </div>
        <div className="faint small">
          Step {Math.min(done + 1, job.steps.length)} of {job.steps.length}
          {current ? `: ${current.title}` : ""}
        </div>
      </div>
      <div className="stack-progress">
        <div style={{ width: `${(done / job.steps.length) * 100}%` }} />
      </div>
    </button>
  );
}

function InstalledRow({ cluster, is, now, category, onRun, onChanged }: { cluster: string; is: InstalledStack; now: number; category: string; onRun(): void; onChanged(): void }) {
  const s = useApp.getState();
  const openNamespace = () => {
    s.setNamespace(is.namespace);
    s.setSection("pods");
  };
  const uninstall = async () => {
    if (readOnly(cluster)) return;
    const helm = is.components.filter((c) => c.kind === "helm").map((c) => c.name);
    const r = await ask({
      title: `Uninstall ${is.name} from ${is.namespace}?`,
      message:
        `Its ${is.components.length} components go, last first${helm.length ? ` (Helm releases ${helm.join(", ")})` : ""}. ` +
        "Parts that another installed stack also has stay, and so do custom resource definitions.",
      checkbox: `Also delete namespace ${is.namespace}, with everything in it and its volumes`,
      confirmLabel: "Uninstall",
      danger: true,
      typeToConfirm: typeToConfirm(cluster),
    });
    if (!r.ok) return;
    const job = await run("Could not uninstall", () => Stacks.uninstall(cluster, is.namespace, is.id, r.checked));
    if (job) useApp.getState().openDialog({ kind: "stack", cluster, stack: is.id, namespace: is.namespace, job });
    onChanged();
  };
  return (
    <div className="installed-row">
      <div className={`stack-icon small cat-${category.replace(/\W/g, "").toLowerCase()}`}>{stackIcon(category, 15)}</div>
      <div className="installed-main">
        <div className="row gap-sm">
          <b>{is.name}</b>
          <button className="link mono small" onClick={openNamespace} title="Show the pods of the namespace">
            {is.namespace}
          </button>
          <Badge tone={stackStatusTone(is.status)}>{is.status}</Badge>
        </div>
        <div className="faint small" title={is.error || is.components.map((c) => `${c.name}: ${c.source}`).join("\n")}>
          {is.error ? <span className="tc tone-bad">{is.error}</span> : `${is.components.length} components · updated ${age(is.updated, now)} ago`}
        </div>
      </div>
      <div className="installed-actions">
        {is.access.map((a, i) => (
          <AccessButtons key={i} cluster={cluster} namespace={is.namespace} stack={is.id} index={i} label={a.label} hasPassword={!!(a.password || a.passwordSecret)} />
        ))}
        {is.inCatalog && (
          <IconButton label="Change its settings and run again" onClick={onRun}>
            <Pencil size={14} />
          </IconButton>
        )}
        <IconButton label="Uninstall" onClick={uninstall}>
          <Trash2 size={14} />
        </IconButton>
      </div>
    </div>
  );
}

/** AccessButtons open a page of a stack and copy its password. */
export function AccessButtons({ cluster, namespace, stack, index, label, hasPassword }: { cluster: string; namespace: string; stack: string; index: number; label: string; hasPassword: boolean }) {
  const [busy, setBusy] = useState(false);
  const open = async () => {
    setBusy(true);
    const url = await run(`Could not open ${label}`, () => Stacks.open(cluster, namespace, stack, index));
    setBusy(false);
    if (url) toast.ok(`${label} is at ${url}`, "Through a port forward, listed in the title bar.");
  };
  const copy = async () => {
    const p = await run("Could not read the password", () => Stacks.password(cluster, namespace, stack, index));
    if (p !== undefined) navigator.clipboard.writeText(p).then(() => toast.ok(`Copied the password of ${label}`));
  };
  return (
    <span className="access-buttons">
      <Button size="sm" busy={busy} icon={<ExternalLink size={13} />} onClick={open}>
        {label}
      </Button>
      {hasPassword && (
        <IconButton label={`Copy the password of ${label}`} onClick={copy}>
          <KeyRound size={14} />
        </IconButton>
      )}
    </span>
  );
}
