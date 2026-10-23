import { useEffect, useState } from "react";
import { CheckCircle2, KeyRound, Sparkles } from "lucide-react";
import { Assistant, Kube, type AssistantSettings } from "../mygo";
import { useAssistant } from "../lib/assistant";
import { errorMessage } from "../lib/k8s";
import { toast } from "../lib/store";
import { Button, Field, Modal, Spinner } from "./ui";

/** AssistantSettingsDialog connects the assistant to Claude in Microsoft Foundry. */
export function AssistantSettingsDialog({ onClose }: { onClose(): void }) {
  const [s, setS] = useState<AssistantSettings | null>(null);
  const [hasKey, setHasKey] = useState(false);
  const [key, setKey] = useState("");
  const [busy, setBusy] = useState(false);
  const [test, setTest] = useState<{ ok: boolean; text: string } | null>(null);

  useEffect(() => {
    Assistant.info().then(
      (i) => {
        setS({ ...i.settings, fallback: i.settings.fallback ?? "" });
        setHasKey(i.hasKey);
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

  const set = (k: keyof AssistantSettings) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setS({ ...s, [k]: e.target.value });

  const save = async (andTest: boolean) => {
    setBusy(true);
    setTest(null);
    try {
      const info = await Assistant.save(s, key);
      setHasKey(info.hasKey);
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
          <Sparkles size={17} className="assistant-mark" /> Assistant · Claude in Microsoft Foundry
        </span>
      }
      onClose={onClose}
      width={600}
      footer={
        <>
          <Button variant="ghost" onClick={() => save(true)} busy={busy} disabled={!s.endpoint || (!hasKey && !key)}>
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
        <div className="form-row">
          <Field label="Deployment" hint="Its name in Foundry, the model by default">
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
        <Field label="Fallback deployment" hint="Answers when the main deployment declines a request, as Claude sometimes does with security questions. Deploy it in the same resource; leave empty for none.">
          <input value={s.fallback} onChange={set("fallback")} placeholder="claude-opus-4-8" className="mono" spellCheck={false} />
        </Field>
        {test && (
          <div className={test.ok ? "test-ok" : "form-error"}>
            {test.ok && <CheckCircle2 size={14} />} {test.text}
          </div>
        )}
        {hasKey && (
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
