import { useEffect, useState } from "react";
import { AlertTriangle, CheckCircle2, KeyRound, Sparkles } from "lucide-react";
import { Assistant, Kube, type AssistantSettings } from "../mygo";
import { useAssistant } from "../lib/assistant";
import { errorMessage } from "../lib/k8s";
import { toast } from "../lib/store";
import { Button, Field, Modal, Spinner } from "./ui";

/** AssistantSettingsDialog connects the assistant to Claude, in Microsoft Foundry or Google Vertex AI. */
export function AssistantSettingsDialog({ onClose }: { onClose(): void }) {
  const [s, setS] = useState<AssistantSettings | null>(null);
  const [google, setGoogle] = useState<{ who?: string; error?: string }>({});
  const [hasKey, setHasKey] = useState(false);
  const [key, setKey] = useState("");
  const [busy, setBusy] = useState(false);
  const [test, setTest] = useState<{ ok: boolean; text: string } | null>(null);

  useEffect(() => {
    Assistant.info().then(
      (i) => {
        setS({ ...i.settings, fallback: i.settings.fallback ?? "", provider: i.settings.provider || "foundry", project: i.settings.project ?? "", region: i.settings.region ?? "" });
        setHasKey(i.hasKey);
        setGoogle({ who: i.google, error: i.googleError });
      },
      (e) => toast.error("Could not read the settings", e),
    );
  }, []);

  if (!s)
    return (
      <Modal title="Assistant" onClose={onClose} width={560}>
        <div className="center-pad">
          <Spinner />
        </div>
      </Modal>
    );

  const vertex = s.provider === "vertex";
  const ready = vertex ? !google.error : !!s.endpoint && (hasKey || !!key);
  const set = (k: keyof AssistantSettings) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setS({ ...s, [k]: e.target.value });

  const save = async (andTest: boolean) => {
    setBusy(true);
    setTest(null);
    try {
      const info = await Assistant.save(s, key);
      setHasKey(info.hasKey);
      setGoogle({ who: info.google, error: info.googleError });
      setKey("");
      useAssistant.setState({ info });
      if (andTest) {
        const reply = await Assistant.test();
        setTest({ ok: true, text: reply });
      } else {
        toast.ok("Assistant settings saved");
        onClose();
      }
    } catch (e) {
      setTest({ ok: false, text: errorMessage(e) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      title={
        <span className="row gap">
          <Sparkles size={17} className="assistant-mark" /> Assistant settings
        </span>
      }
      onClose={onClose}
      width={600}
      footer={
        <>
          <Button variant="ghost" onClick={() => save(true)} busy={busy} disabled={!ready}>
            Save and test
          </Button>
          <div className="spacer" />
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" onClick={() => save(false)} busy={busy}>
            Save
          </Button>
        </>
      }
    >
      <div className="add-body">
        <Field label="Claude through">
          <div className="segmented">
            <button className={!vertex ? "on" : ""} onClick={() => setS({ ...s, provider: "foundry", endpoint: vertex ? "" : s.endpoint })}>
              Microsoft Foundry (Azure)
            </button>
            <button className={vertex ? "on" : ""} onClick={() => setS({ ...s, provider: "vertex", endpoint: vertex ? s.endpoint : "", region: s.region || "global" })}>
              Google Vertex AI
            </button>
          </div>
        </Field>
        {vertex ? (
          <>
            <p className="muted small">
              Enable the Claude model in{" "}
              <button className="link" onClick={() => Kube.openURL("https://console.cloud.google.com/vertex-ai/model-garden")}>
                Vertex AI Model Garden
              </button>
              . Kube Desk signs in with your gcloud credentials: run <code>gcloud auth application-default login</code> in a terminal once.
            </p>
            {google.error ? (
              <div className="form-error">
                <AlertTriangle size={14} /> {google.error}
              </div>
            ) : google.who ? (
              <div className="test-ok">
                <KeyRound size={14} /> Signs in as {google.who}
              </div>
            ) : (
              <div className="faint small">Save and test checks your gcloud credentials.</div>
            )}
            <div className="form-row">
              <Field label="Project" hint="The Google Cloud project ID">
                <input value={s.project ?? ""} onChange={set("project")} placeholder="my-project" className="mono" spellCheck={false} />
              </Field>
              <Field label="Region" hint="global, us-east5, europe-west1…">
                <input value={s.region ?? ""} onChange={set("region")} placeholder="global" className="mono" spellCheck={false} />
              </Field>
            </div>
          </>
        ) : (
          <>
            <p className="muted small">
              In the{" "}
              <button className="link" onClick={() => Kube.openURL("https://ai.azure.com/")}>
                Foundry portal
              </button>
              , open your Claude deployment under <b>Build → Models</b>: its <b>Details</b> tab has the Target URI and the Key.
            </p>
            <Field label="Foundry resource or Target URI" hint="The resource name (my-resource), or the URL: https://my-resource.services.ai.azure.com/anthropic/v1/messages">
              <input value={s.endpoint} onChange={set("endpoint")} placeholder="my-resource" className="mono" autoFocus spellCheck={false} />
            </Field>
            <Field
              label="API key"
              hint={
                hasKey ? (
                  <span className="text-ok">
                    <KeyRound size={12} /> Kept in your macOS keychain. Leave empty to keep it.
                  </span>
                ) : (
                  "Kept in your macOS keychain, never in a file."
                )
              }
            >
              <input type="password" value={key} onChange={(e) => setKey(e.target.value)} placeholder={hasKey ? "••••••••••••••••" : "Key from the deployment's Details tab"} autoComplete="off" />
            </Field>
          </>
        )}
        <div className="form-row">
          <Field label={vertex ? "Model" : "Deployment"} hint={vertex ? "The model ID in Vertex AI" : "Its name in Foundry, the model by default"}>
            <input value={s.deployment} onChange={set("deployment")} placeholder="claude-opus-5-5" className="mono" spellCheck={false} />
          </Field>
          <Field label="Effort" hint="Higher thinks more, costs more">
            <select value={s.effort} onChange={set("effort")}>
              <option value="low">Low</option>
              <option value="medium">Medium</option>
              <option value="high">High (recommended)</option>
              <option value="xhigh">Extra high</option>
              <option value="max">Max</option>
            </select>
          </Field>
        </div>
        <Field
          label={vertex ? "Fallback model" : "Fallback deployment"}
          hint={vertex ? "Answers when the main model declines a request, as Claude sometimes does with security questions. Enable it in Model Garden too; leave empty for none." : "Answers when the main deployment declines a request, as Claude sometimes does with security questions. Deploy it in the same resource; leave empty for none."}
        >
          <input value={s.fallback} onChange={set("fallback")} placeholder="claude-opus-4-8" className="mono" spellCheck={false} />
        </Field>
        {test && (
          <div className={test.ok ? "test-ok" : "form-error"}>
            {test.ok && <CheckCircle2 size={14} />} {test.text}
          </div>
        )}
        {vertex && (
          <Field label="Endpoint (optional)" hint="Only for a private endpoint, such as Private Service Connect; empty uses Google's">
            <input value={s.endpoint} onChange={set("endpoint")} placeholder="https://…" className="mono" spellCheck={false} />
          </Field>
        )}
        {hasKey && !vertex && (
          <button
            className="link small"
            style={{ alignSelf: "flex-start" }}
            onClick={async () => {
              await Assistant.forgetKey();
              setHasKey(false);
              useAssistant.setState({ info: null });
              toast.info("API key removed from the keychain");
            }}
          >
            Remove the key from the keychain
          </button>
        )}
      </div>
    </Modal>
  );
}
