import { KeyRound, ShieldCheck } from "lucide-react";
import { app, k8s, registerColumn, registerDetailsSection, registerObjectAction, registerOverviewCard, registerPage, registerSettings, settings, ui, type KObj } from "kubedesk";
import { useState } from "react";

// Certificates of cert-manager: a page of them, when they expire, and a card
// on the overview of the ones that need attention.

const certificates = { group: "cert-manager.io", version: "v1", resource: "certificates" };
const secrets = { group: "", version: "v1", resource: "secrets" };

const DAY = 24 * 3600e3;

function warnDays(): number {
  const n = Number(settings.get().warnDays);
  return Number.isFinite(n) && n > 0 ? n : 14;
}

function ready(c: KObj): { ok: boolean; reason: string; message: string } {
  const cond = (c.status?.conditions ?? []).find((x: { type: string }) => x.type === "Ready");
  return { ok: cond?.status === "True", reason: cond?.reason ?? "Unknown", message: cond?.message ?? "" };
}

/** expiresIn is the time left before a certificate expires, in ms; NaN when unknown. */
function expiresIn(c: KObj): number {
  return c.status?.notAfter ? Date.parse(c.status.notAfter) - Date.now() : NaN;
}

function humanize(ms: number): string {
  if (!Number.isFinite(ms)) return "—";
  const days = Math.round(Math.abs(ms) / DAY);
  const text = days >= 1 ? `${days} day${days === 1 ? "" : "s"}` : `${Math.round(Math.abs(ms) / 3600e3)} h`;
  return ms < 0 ? `expired ${text} ago` : `in ${text}`;
}

function toneOf(c: KObj): "ok" | "warn" | "bad" {
  const left = expiresIn(c);
  if (!ready(c).ok || left < 0) return "bad";
  if (left < warnDays() * DAY) return "warn";
  return "ok";
}

function Expires({ c }: { c: KObj }) {
  const left = expiresIn(c);
  return <ui.Badge tone={toneOf(c)}>{humanize(left)}</ui.Badge>;
}

const open = (cluster: string, c: KObj) => app.openObject(cluster, { gvr: certificates, kind: "Certificate", namespace: c.metadata.namespace ?? "", name: c.metadata.name });

function NotInstalled() {
  return (
    <ui.Empty icon={<ShieldCheck size={28} />} title="cert-manager is not in this cluster">
      Its Certificate resource is not served here.
    </ui.Empty>
  );
}

function CertificatesPage({ cluster, namespace }: { cluster: string; namespace: string }) {
  const { items, loading, error } = k8s.useList(cluster, certificates, namespace);
  const [problems, setProblems] = useState(false);
  settings.useSettings();
  if (error) return /not found|could not find|no matches/i.test(error) ? <NotInstalled /> : <ui.ErrorBox message={error} />;
  const shown = problems ? items.filter((c) => toneOf(c) !== "ok") : items;
  return (
    <ui.Page
      title="Certificates"
      subtitle={`${items.length} in ${namespace || "all namespaces"} · warns ${warnDays()} days before they expire`}
      actions={
        <ui.Button size="sm" onClick={() => setProblems(!problems)}>
          {problems ? "Show all" : "Only problems"}
        </ui.Button>
      }
    >
      {loading ? (
        <ui.Spinner />
      ) : shown.length === 0 ? (
        <ui.Empty title={problems ? "No certificate needs attention" : "No certificates"} />
      ) : (
        <ui.Table
          items={shown}
          defaultSort="-expires"
          columns={[
            { id: "name", title: "Name", value: (c) => c.metadata.name },
            { id: "namespace", title: "Namespace", value: (c) => c.metadata.namespace ?? "" },
            { id: "ready", title: "Ready", value: (c) => ready(c).reason, render: (c) => <ui.Badge tone={ready(c).ok ? "ok" : "bad"}>{ready(c).ok ? "Ready" : ready(c).reason}</ui.Badge> },
            { id: "dns", title: "DNS names", value: (c) => (c.spec?.dnsNames ?? []).join(", ") },
            { id: "issuer", title: "Issuer", value: (c) => `${c.spec?.issuerRef?.kind ?? "Issuer"}/${c.spec?.issuerRef?.name ?? ""}` },
            // Sorting descending on the negated time puts the soonest first.
            { id: "expires", title: "Expires", value: (c) => -(expiresIn(c) || 0), render: (c) => <Expires c={c} /> },
          ]}
          onOpen={(c) => open(cluster, c)}
        />
      )}
    </ui.Page>
  );
}

function OverviewCard({ cluster, namespace }: { cluster: string; namespace: string }) {
  const { items, loading, error } = k8s.useList(cluster, certificates, namespace);
  settings.useSettings();
  if (error) return <div className="muted small">cert-manager is not in this cluster.</div>;
  if (loading) return <ui.Spinner size={14} />;
  const bad = items.filter((c) => toneOf(c) === "bad");
  const soon = items.filter((c) => toneOf(c) === "warn");
  return (
    <div>
      <ui.KV
        items={[
          ["Certificates", items.length],
          ["Not ready or expired", bad.length ? <ui.Badge tone="bad">{bad.length}</ui.Badge> : 0],
          [`Expiring within ${warnDays()} days`, soon.length ? <ui.Badge tone="warn">{soon.length}</ui.Badge> : 0],
        ]}
      />
      {[...bad, ...soon].slice(0, 5).map((c) => (
        <button key={c.metadata.uid} className="mini-row clickable" style={{ gridTemplateColumns: "minmax(0, 1fr) auto" }} onClick={() => open(cluster, c)}>
          <span className="mono">{c.metadata.namespace}/{c.metadata.name}</span>
          <Expires c={c} />
        </button>
      ))}
      <div style={{ marginTop: 8 }}>
        <ui.Button size="sm" variant="ghost" onClick={() => app.openPage("certificates")}>
          All certificates
        </ui.Button>
      </div>
    </div>
  );
}

function CertificateSection({ object: c }: { object: KObj }) {
  const r = ready(c);
  return (
    <ui.KV
      items={[
        ["Ready", <ui.Badge tone={r.ok ? "ok" : "bad"}>{r.ok ? "Ready" : r.reason}</ui.Badge>],
        ["Message", r.message],
        ["Expires", c.status?.notAfter ? <span>{new Date(c.status.notAfter).toLocaleString()} <Expires c={c} /></span> : undefined],
        ["Renews", c.status?.renewalTime ? new Date(c.status.renewalTime).toLocaleString() : undefined],
        ["Secret", c.spec?.secretName],
      ]}
    />
  );
}

function Settings({ settings: s, save }: { settings: Record<string, unknown>; save(next: Record<string, unknown>): Promise<void> }) {
  const [days, setDays] = useState(String(s.warnDays ?? 14));
  return (
    <div>
      <ui.Field label="Warn before a certificate expires, in days" hint="Certificates closer than this to their expiry show in orange.">
        <input type="number" min={1} value={days} onChange={(e) => setDays(e.target.value)} />
      </ui.Field>
      <div style={{ marginTop: 12 }}>
        <ui.Button variant="primary" onClick={() => save({ ...s, warnDays: Math.max(1, Number(days) || 14) })}>
          Save
        </ui.Button>
      </div>
    </div>
  );
}

export function activate() {
  registerPage({ id: "certificates", title: "Certificates", icon: ShieldCheck, component: CertificatesPage });
  registerOverviewCard({ id: "certificates", title: "Certificates", component: OverviewCard });
  registerColumn({ id: "expires", title: "Expires", kinds: ["Certificate"], value: (c) => -(expiresIn(c) || 0), render: (c) => <Expires c={c} /> });
  registerDetailsSection({ id: "certificate", title: "cert-manager", kinds: ["Certificate"], component: CertificateSection });
  registerObjectAction({
    id: "open-secret",
    label: "Open its secret",
    icon: KeyRound,
    kinds: ["Certificate"],
    when: ({ object }) => !!object.spec?.secretName,
    run: ({ cluster, object }) => app.openObject(cluster, { gvr: secrets, kind: "Secret", namespace: object.metadata.namespace ?? "", name: object.spec.secretName }),
  });
  registerSettings(Settings);
}
