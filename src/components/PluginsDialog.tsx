import { useEffect, useRef, useState } from "react";
import { ArrowLeft, Check, Download, ExternalLink, FolderOpen, FolderPlus, Link, PackagePlus, Puzzle, RefreshCw, Search, Settings2, ShieldAlert, Trash2 } from "lucide-react";
import { Kube, Plugins, type CatalogPlugin, type Plugin, type PluginUpdate } from "../mygo";
import { ask, toast } from "../lib/store";
import { errorMessage } from "../lib/k8s";
import { refreshPlugins, usePluginList } from "../plugins/loader";
import { usePlugins } from "../plugins/registry";
import { PluginBoundary } from "../plugins/Slot";
import { loadSettings } from "../plugins/api";
import { Markdown } from "./Markdown";
import { Badge, Button, Empty, ErrorBox, IconButton, Modal, Spinner, Tabs } from "./ui";

const trustMessage =
  "A plugin runs inside Kube Desk, with the same access as the app: it can read your clusters, and ask to change them. Turn on only plugins from people you trust.";

const originText = (o?: string) =>
  !o ? "" : o.startsWith("registry:") ? "from the plugin registry" : o.startsWith("file:") ? "from a file" : "from an address";

/** turnOn asks the user to trust a plugin before it runs. */
async function turnOn(p: Plugin): Promise<boolean> {
  if (p.source !== "dev") {
    const r = await ask({ title: `Turn on ${p.name}?`, message: `${trustMessage}\n\nInstalled ${originText(p.origin)}.`, confirmLabel: "Turn on" });
    if (!r.ok) return false;
  }
  await Plugins.setEnabled(p.id, true);
  await refreshPlugins();
  return true;
}

/** afterInstall offers to turn on a plugin installed from a file or an address. */
async function afterInstall(p: Plugin | null | undefined) {
  if (!p) return;
  await refreshPlugins();
  if (p.error) {
    toast.error(`Installed ${p.name}, which cannot run`, p.error);
    return;
  }
  if (p.enabled) {
    toast.ok(`Updated ${p.name} to ${p.version}`);
    return;
  }
  if (await turnOn(p)) toast.ok(`${p.name} ${p.version} is on`);
  else toast.info(`Installed ${p.name}, turned off`, "Turn it on in Plugins when you want it.");
}

/**
 * installFromRegistry installs a plugin of the registry, or updates it,
 * after the user agreed to trust it, and turns it on. It reports whether
 * it did.
 */
async function installFromRegistry(c: Pick<CatalogPlugin, "id" | "name" | "author" | "installed">, version: string): Promise<boolean> {
  if (!c.installed) {
    const r = await ask({
      title: `Install ${c.name} ${version}?`,
      message: `${c.author ? `By ${c.author}, from` : "From"} Kube Desk's plugin registry.\n\n${trustMessage}`,
      confirmLabel: "Install and turn on",
    });
    if (!r.ok) return false;
  }
  try {
    const p = await Plugins.installFromRegistry(c.id, version);
    if (!p) return false;
    if (p.error) {
      await refreshPlugins();
      toast.error(`Installed ${p.name}, which cannot run`, p.error);
      return true;
    }
    if (!c.installed) await Plugins.setEnabled(p.id, true);
    await refreshPlugins();
    toast.ok(c.installed ? `Updated ${p.name} to ${p.version}` : `${p.name} ${p.version} is installed and on`);
    return true;
  } catch (e) {
    toast.error(`Could not install ${c.name}`, e);
    return false;
  }
}

export interface PluginsDialogProps {
  /** plugin shows the settings of a plugin. */
  plugin?: string;
  /** install shows a plugin of the registry and offers to install it. */
  install?: string;
  version?: string;
  onClose(): void;
}

export function PluginsDialog({ plugin, install, version, onClose }: PluginsDialogProps) {
  const [tab, setTab] = useState<"installed" | "catalog">(install ? "catalog" : "installed");
  const [settingsOf, setSettingsOf] = useState<string | undefined>(plugin);
  const list = usePluginList((s) => s.list);
  const shown = settingsOf ? list.find((p) => p.id === settingsOf) : undefined;

  useEffect(() => {
    refreshPlugins();
  }, []);

  if (shown) {
    return (
      <Modal
        title={
          <span className="plugin-settings-title">
            <IconButton label="Back" onClick={() => setSettingsOf(undefined)}>
              <ArrowLeft size={16} />
            </IconButton>
            {shown.name} settings
          </span>
        }
        onClose={onClose}
        width={640}
      >
        <PluginSettings plugin={shown} />
      </Modal>
    );
  }

  return (
    <Modal title="Plugins" onClose={onClose} width={780} className="plugins-dialog">
      <Tabs
        tabs={[
          { id: "installed", label: "Installed" },
          { id: "catalog", label: "Find plugins" },
        ]}
        value={tab}
        onChange={(t) => setTab(t as "installed" | "catalog")}
      />
      {tab === "installed" ? <Installed onSettings={setSettingsOf} onFind={() => setTab("catalog")} /> : <Catalog install={install} version={version} />}
    </Modal>
  );
}

function PluginIcon({ src }: { src?: string }) {
  const [broken, setBroken] = useState(false);
  if (src && !broken) return <img className="plugin-icon" src={src} alt="" onError={() => setBroken(true)} />;
  return (
    <div className="plugin-icon">
      <Puzzle size={18} />
    </div>
  );
}

function Installed({ onSettings, onFind }: { onSettings(id: string): void; onFind(): void }) {
  const { list, loaded } = usePluginList();
  const [updates, setUpdates] = useState<PluginUpdate[]>([]);
  const [checking, setChecking] = useState(false);
  const [busy, setBusy] = useState("");

  const installFile = async () => {
    try {
      await afterInstall(await Plugins.installFile());
    } catch (e) {
      toast.error("Could not install the plugin", e);
    }
  };
  const installURL = async () => {
    const r = await ask({
      title: "Install a plugin from an address",
      message: "The address of the plugin's archive (.tgz or .zip), over HTTPS. Add its SHA-256 after a space to check it.",
      input: { label: "Address", value: "", placeholder: "https://example.com/my-plugin-1.0.0.tgz" },
      confirmLabel: "Install",
    });
    if (!r.ok || !r.value.trim()) return;
    const [url, sha = ""] = r.value.trim().split(/\s+/);
    setBusy("url");
    try {
      await afterInstall(await Plugins.installURL(url!, sha));
    } catch (e) {
      toast.error("Could not install the plugin", e);
    } finally {
      setBusy("");
    }
  };
  const loadDev = async () => {
    try {
      const p = await Plugins.addDevFolder();
      if (p) {
        await refreshPlugins();
        toast.ok(`Loaded ${p.name}`, p.error ?? "It reloads each time its build changes.");
      }
    } catch (e) {
      toast.error("Could not load the plugin", e);
    }
  };
  const checkUpdates = async () => {
    setChecking(true);
    try {
      const u = await Plugins.checkUpdates();
      setUpdates(u);
      if (!u.length) toast.info("Your plugins are up to date");
    } catch (e) {
      toast.error("Could not check for updates", e);
    } finally {
      setChecking(false);
    }
  };

  return (
    <div className="plugins">
      <div className="plugins-toolbar">
        <Button size="sm" icon={<PackagePlus size={14} />} onClick={installFile}>
          Install from file…
        </Button>
        <Button size="sm" icon={<Link size={14} />} onClick={installURL} busy={busy === "url"}>
          From address…
        </Button>
        <Button size="sm" icon={<FolderPlus size={14} />} onClick={loadDev} title="Load a plugin you are writing, from its folder; it reloads as you rebuild it">
          Load from folder…
        </Button>
        <div className="spacer" />
        <Button size="sm" variant="ghost" icon={<RefreshCw size={14} />} onClick={checkUpdates} busy={checking}>
          Updates
        </Button>
        <IconButton label="Open the plugins folder" onClick={() => Plugins.revealFolder().catch((e) => toast.error("Could not open the folder", e))}>
          <FolderOpen size={15} />
        </IconButton>
      </div>
      {!loaded ? (
        <div className="center-pad">
          <Spinner />
        </div>
      ) : list.length === 0 ? (
        <Empty icon={<Puzzle size={28} />} title="No plugins yet">
          Plugins add pages, tabs, columns, actions and commands to Kube Desk.{" "}
          <button className="link" onClick={onFind}>
            Find plugins
          </button>{" "}
          or install one from a file.
        </Empty>
      ) : (
        <div className="plugin-list">
          {list.map((p) => (
            <PluginRow key={`${p.source}:${p.path}`} p={p} update={updates.find((u) => u.id === p.id && p.source === "installed")} onSettings={() => onSettings(p.id)} onUpdated={() => setUpdates((u) => u.filter((x) => x.id !== p.id))} />
          ))}
        </div>
      )}
      <p className="faint small plugins-note">
        <ShieldAlert size={13} /> {trustMessage}
      </p>
    </div>
  );
}

function PluginRow({ p, update, onSettings, onUpdated }: { p: Plugin; update?: PluginUpdate; onSettings(): void; onUpdated(): void }) {
  const state = usePlugins((s) => s.states[p.id]);
  const hasSettings = usePlugins((s) => s.settings.some((c) => c.plugin === p.id));
  const [busy, setBusy] = useState(false);
  const shadowed = p.source === "installed" && !!p.error && p.error.includes("in development");

  const toggle = async (on: boolean) => {
    setBusy(true);
    try {
      if (on) await turnOn(p);
      else {
        await Plugins.setEnabled(p.id, false);
        await refreshPlugins();
      }
    } catch (e) {
      toast.error(`Could not turn ${on ? "on" : "off"} ${p.name}`, e);
    } finally {
      setBusy(false);
    }
  };
  const remove = async () => {
    const dev = p.source === "dev";
    const r = await ask({
      title: dev ? `Stop loading ${p.name}?` : `Uninstall ${p.name}?`,
      message: dev ? `Kube Desk stops loading the plugin from ${p.path}. The folder stays as it is.` : "Kube Desk removes the plugin and its settings.",
      confirmLabel: dev ? "Stop loading" : "Uninstall",
      danger: !dev,
    });
    if (!r.ok) return;
    try {
      await Plugins.uninstall(p.id);
      await refreshPlugins();
    } catch (e) {
      toast.error(`Could not remove ${p.name}`, e);
    }
  };
  const doUpdate = async () => {
    setBusy(true);
    if (await installFromRegistry({ id: p.id, name: p.name, author: p.author, installed: p.version }, update!.latest)) onUpdated();
    setBusy(false);
  };

  const error = p.error || (p.enabled && state?.status === "failed" ? `Failed to load: ${state.error}` : "");
  return (
    <div className={`plugin-row ${p.enabled && !p.error ? "on" : ""}`}>
      <PluginIcon />
      <div className="plugin-main">
        <div className="plugin-name">
          <b>{p.name}</b>
          <span className="faint small mono">{p.version}</span>
          {p.source === "dev" && <Badge tone="info">Development</Badge>}
          {p.enabled && state?.status === "loading" && <Spinner size={12} />}
        </div>
        {p.description && <div className="muted small">{p.description}</div>}
        <div className="faint small plugin-meta">
          {p.id !== p.name && <span className="mono">{p.id}</span>}
          {p.author && <span>by {p.author}</span>}
          {p.source === "dev" ? <span className="mono" title={p.path}>{p.path}</span> : p.origin && <span>{originText(p.origin)}</span>}
          {p.homepage && (
            <button className="link" onClick={() => Kube.openURL(p.homepage!)}>
              Homepage <ExternalLink size={11} />
            </button>
          )}
        </div>
        {error && <div className={`small ${shadowed ? "faint" : "text-bad"}`}>{error}</div>}
      </div>
      <div className="plugin-actions">
        {update && (
          <Button size="sm" variant="primary" icon={<Download size={13} />} onClick={doUpdate} busy={busy}>
            {update.latest}
          </Button>
        )}
        {hasSettings && p.enabled && (
          <IconButton label="Settings" onClick={onSettings}>
            <Settings2 size={15} />
          </IconButton>
        )}
        <IconButton label={p.source === "dev" ? "Stop loading" : "Uninstall"} onClick={remove}>
          <Trash2 size={15} />
        </IconButton>
        <label className="switch" title={p.enabled ? "Turn off" : "Turn on"}>
          <input type="checkbox" checked={p.enabled} disabled={busy || (!p.enabled && !!p.error)} onChange={(e) => toggle(e.target.checked)} />
          <span />
        </label>
      </div>
    </div>
  );
}

/** InstallButton installs, updates, or says the plugin is installed or needs a newer Kube Desk. */
function InstallButton({ c, onDone, size = "sm" }: { c: CatalogPlugin; onDone(): void; size?: "sm" | "md" }) {
  const [busy, setBusy] = useState(false);
  if (!c.version) return <span className="faint small">Needs Kube Desk {c.engine.replace(/^>=\s*/, "")}</span>;
  if (c.installed === c.version)
    return (
      <Badge tone="ok">
        <Check size={11} /> Installed
      </Badge>
    );
  const run = async () => {
    setBusy(true);
    if (await installFromRegistry(c, c.version)) onDone();
    setBusy(false);
  };
  return (
    <Button size={size} variant={c.installed ? "primary" : "default"} icon={<Download size={13} />} busy={busy} onClick={run}>
      {c.installed ? `Update to ${c.version}` : "Install"}
    </Button>
  );
}

function Catalog({ install, version }: { install?: string; version?: string }) {
  const [q, setQ] = useState("");
  const [results, setResults] = useState<CatalogPlugin[] | null>(null);
  const [error, setError] = useState("");
  const [open, setOpen] = useState<string | undefined>(install);
  const offered = useRef(false);

  const search = (query: string, refresh = false) => {
    setError("");
    return Plugins.catalog(query, refresh).then(
      (r) => {
        setResults(r);
        return r;
      },
      (e) => {
        setError(errorMessage(e));
        return [] as CatalogPlugin[];
      },
    );
  };
  useEffect(() => {
    const t = setTimeout(() => search(q), q ? 200 : 0);
    return () => clearTimeout(t);
  }, [q]);

  // A link from the website: show the plugin, and offer to install it.
  useEffect(() => {
    if (!install || offered.current) return;
    offered.current = true;
    Plugins.catalog("", true).then(async (all) => {
      setResults(all);
      const c = all.find((x) => x.id === install);
      if (!c) return toast.error("The plugin registry has no such plugin", install);
      const v = version || c.version;
      if (!v) return toast.error(`${c.name} needs Kube Desk ${c.engine}`, "Update Kube Desk first.");
      if (c.installed === v) return toast.info(`${c.name} ${v} is already installed`);
      if (await installFromRegistry(c, v)) search(q, true);
    }, (e) => setError(errorMessage(e)));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [install]);

  const shown = open ? results?.find((c) => c.id === open) : undefined;
  if (shown) return <CatalogDetail c={shown} onBack={() => setOpen(undefined)} onChanged={() => search(q, true)} />;

  return (
    <div className="plugins">
      <div className="plugins-toolbar">
        <div className="search-input">
          <Search size={14} />
          <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search plugins" autoFocus />
        </div>
        <div className="spacer" />
        <Button size="sm" variant="ghost" onClick={() => Kube.openURL("https://kubedesk.cloud/plugins.html")} icon={<ExternalLink size={13} />}>
          Marketplace
        </Button>
      </div>
      {error ? (
        <ErrorBox title="Could not read the plugin registry" message={error} action={<Button onClick={() => search(q, true)}>Retry</Button>} />
      ) : !results ? (
        <div className="center-pad">
          <Spinner />
        </div>
      ) : results.length === 0 ? (
        <Empty icon={<Puzzle size={28} />} title={q ? "No plugins match" : "No plugins yet"} />
      ) : (
        <div className="plugin-list">
          {results.map((c) => (
            <div key={c.id} className="plugin-row clickable" onClick={() => setOpen(c.id)}>
              <PluginIcon src={c.icon} />
              <div className="plugin-main">
                <div className="plugin-name">
                  <b>{c.name}</b>
                  <span className="faint small mono">{c.version || c.latest}</span>
                </div>
                {c.description && <div className="muted small">{c.description}</div>}
                <div className="faint small plugin-meta">
                  {c.author && <span>by {c.author}</span>}
                  {c.keywords?.slice(0, 4).map((k) => (
                    <span key={k} className="plugin-tag">
                      {k}
                    </span>
                  ))}
                </div>
              </div>
              <div className="plugin-actions" onClick={(e) => e.stopPropagation()}>
                <InstallButton c={c} onDone={() => search(q, true)} />
              </div>
            </div>
          ))}
        </div>
      )}
      <p className="faint small plugins-note">
        <ShieldAlert size={13} /> Plugins come from Kube Desk's registry, signed, and are checked before they are installed. {trustMessage}
      </p>
    </div>
  );
}

function CatalogDetail({ c, onBack, onChanged }: { c: CatalogPlugin; onBack(): void; onChanged(): void }) {
  const [readme, setReadme] = useState<string | null>(c.hasReadme ? null : "");
  useEffect(() => {
    if (c.hasReadme) Plugins.readme(c.id).then(setReadme, () => setReadme(""));
  }, [c.id, c.hasReadme]);
  const kb = Math.max(1, Math.round(c.size / 1024));
  return (
    <div className="plugins plugin-detail">
      <div className="plugin-detail-head">
        <IconButton label="Back" onClick={onBack}>
          <ArrowLeft size={16} />
        </IconButton>
        <PluginIcon src={c.icon} />
        <div className="plugin-main">
          <div className="plugin-name">
            <b>{c.name}</b>
            <span className="faint small mono">{c.version || c.latest}</span>
          </div>
          <div className="faint small plugin-meta">
            {c.author && <span>by {c.author}</span>}
            <span className="mono">{c.id}</span>
            {c.license && <span>{c.license}</span>}
            <span>{kb} KB</span>
            {c.published && <span>published {c.published.slice(0, 10)}</span>}
            {c.homepage && (
              <button className="link" onClick={() => Kube.openURL(c.homepage!)}>
                Homepage <ExternalLink size={11} />
              </button>
            )}
          </div>
        </div>
        <InstallButton c={c} onDone={onChanged} size="md" />
      </div>
      {c.description && <p className="plugin-detail-desc">{c.description}</p>}
      {!!c.features?.length && (
        <ul className="plugin-features">
          {c.features.map((f) => (
            <li key={f}>{f}</li>
          ))}
        </ul>
      )}
      <div className="plugin-readme">{readme === null ? <Spinner /> : readme ? <Markdown text={readme} /> : null}</div>
    </div>
  );
}

function PluginSettings({ plugin }: { plugin: Plugin }) {
  const contribution = usePlugins((s) => s.settings.find((c) => c.plugin === plugin.id));
  const [settings, setSettings] = useState<Record<string, unknown> | null>(null);

  useEffect(() => {
    Plugins.settings(plugin.id).then(setSettings, (e) => toast.error("Could not read the settings", e));
  }, [plugin.id]);

  if (!contribution) return <Empty title="This plugin has no settings" />;
  if (!settings)
    return (
      <div className="center-pad">
        <Spinner />
      </div>
    );
  const save = async (next: Record<string, unknown>) => {
    await Plugins.saveSettings(plugin.id, next);
    await loadSettings(plugin.id);
    setSettings(next);
    toast.ok("Settings saved");
  };
  const C = contribution.component;
  return (
    <PluginBoundary plugin={plugin.id}>
      <C settings={settings} save={save} />
    </PluginBoundary>
  );
}
