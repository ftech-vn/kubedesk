import { useEffect, useMemo, useRef, useState } from "react";
import { marked } from "marked";
import DOMPurify from "dompurify";
import { ArrowUp, Brain, ChevronDown, ChevronRight, Clock, Loader2, Plus, Settings2, Sparkles, Square, Trash2, Wrench, X, XCircle, CheckCircle2, ShieldQuestion } from "lucide-react";
import { Kube } from "../mygo";
import { useAssistant, type Part, type Turn } from "../lib/assistant";
import { useApp } from "../lib/store";
import { age } from "../lib/k8s";
import { Button, IconButton } from "./ui";
import { Popover } from "./Menu";

const suggestions = [
  "Summarize the health of this cluster",
  "Which pods are failing, and why?",
  "What changed in the last hour?",
  "Check the object I have open",
];

const escapeHtml = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");

// Answers are Markdown only: HTML in them shows as text, never as markup.
// DOMPurify then sanitizes what Markdown produced, as a second guard.
marked.use({ gfm: true, breaks: true, renderer: { html: ({ text }) => escapeHtml(text) } });

function Markdown({ text }: { text: string }) {
  const html = useMemo(() => DOMPurify.sanitize(marked.parse(text, { async: false }) as string), [text]);
  return (
    <div
      className="md"
      dangerouslySetInnerHTML={{ __html: html }}
      onClick={(e) => {
        const a = (e.target as HTMLElement).closest("a");
        if (a?.href) {
          e.preventDefault();
          Kube.openURL(a.href).catch(() => {});
        }
      }}
    />
  );
}

const toolLabels: Record<string, string> = {
  cluster_overview: "Looked at the cluster",
  helm_releases: "Read Helm releases",
  list_clusters: "Listed clusters",
  list_resources: "Listed",
  get_resource: "Read",
  get_logs: "Read logs",
  get_events: "Read events",
  get_changes: "Read recent changes",
  incident_report: "Gathered an incident report",
  search: "Searched clusters",
  compare: "Compared clusters",
  preview_impact: "Checked the impact",
  apply_manifest: "Apply",
  delete_resource: "Delete",
  scale: "Scale",
  restart: "Restart",
};

function ToolPart({ p }: { p: Extract<Part, { kind: "tool" }> }) {
  const [open, setOpen] = useState(false);
  const icon =
    p.status === "running" ? (
      p.write ? <ShieldQuestion size={13} className="text-warn" /> : <Loader2 size={13} className="spin" />
    ) : p.status === "ok" ? (
      <CheckCircle2 size={13} className="text-ok" />
    ) : (
      <XCircle size={13} className="text-bad" />
    );
  return (
    <div className={`chat-tool ${p.write ? "write" : ""} ${p.status}`}>
      <button className="chat-tool-head" onClick={() => setOpen(!open)}>
        {icon}
        <Wrench size={12} className="faint" />
        <span className="chat-tool-name">{toolLabels[p.tool] ?? p.tool}</span>
        <span className="chat-tool-input mono">{p.input}</span>
        {p.status === "running" && p.write && <span className="text-warn small">waiting for your approval</span>}
        {p.result && (open ? <ChevronDown size={12} /> : <ChevronRight size={12} />)}
      </button>
      {open && p.result && <pre className="chat-tool-result selectable">{p.result}</pre>}
    </div>
  );
}

function Thinking({ text, live }: { text: string; live: boolean }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="chat-thinking">
      <button className="link small" onClick={() => setOpen(!open)}>
        <Brain size={12} /> {live ? "Thinking…" : "Thought"} {open ? <ChevronDown size={11} /> : <ChevronRight size={11} />}
      </button>
      {open && <div className="chat-thinking-text">{text}</div>}
    </div>
  );
}

function AssistantTurn({ t, last }: { t: Extract<Turn, { role: "assistant" }>; last: boolean }) {
  const live = last && !t.done;
  return (
    <div className="chat-turn assistant">
      {t.parts.map((p, i) =>
        p.kind === "text" ? (
          <Markdown key={i} text={p.text} />
        ) : p.kind === "thinking" ? (
          <Thinking key={i} text={p.text} live={live && i === t.parts.length - 1} />
        ) : p.kind === "tool" ? (
          <ToolPart key={i} p={p} />
        ) : (
          <div key={i} className={`chat-note ${p.error ? "error" : ""}`}>
            {p.text}
          </div>
        ),
      )}
      {live && t.parts.length === 0 && (
        <div className="faint small row gap">
          <Loader2 size={13} className="spin" /> Working…
        </div>
      )}
      {t.error && <div className="chat-note error">{t.error}</div>}
      {t.done && t.tokens && t.tokens.input > 0 && (
        <div className="chat-usage faint">
          {Math.round(t.tokens.input / 100) / 10}k in{t.tokens.cached ? ` (${Math.round((t.tokens.cached / t.tokens.input) * 100)}% cached)` : ""} · {Math.round(t.tokens.output / 100) / 10}k out
        </div>
      )}
    </div>
  );
}

/** AssistantPanel is the chat with the assistant, beside the app. */
export function AssistantPanel() {
  const { open, turns, running, info, chats, chatId, toggle, send, stop, reset, openChat, deleteChat } = useAssistant();
  const openDialog = useApp((s) => s.openDialog);
  const [text, setText] = useState("");
  const [history, setHistory] = useState<HTMLElement | null>(null);
  const scroller = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    if (open) setTimeout(() => input.current?.focus(), 50);
  }, [open]);
  useEffect(() => {
    const el = scroller.current;
    if (el && el.scrollHeight - el.scrollTop - el.clientHeight < 160) el.scrollTop = el.scrollHeight;
  }, [turns]);

  if (!open) return null;
  const submit = (q = text) => {
    if (!q.trim() || running) return;
    setText("");
    send(q);
  };
  const configured = info?.configured;

  return (
    <aside className="assistant">
      <header className="assistant-head">
        <Sparkles size={16} className="assistant-mark" />
        <span className="assistant-title">Assistant</span>
        {info && <span className="faint small mono">{info.settings.deployment}</span>}
        <div className="spacer" />
        <IconButton label="Past chats" onClick={(e) => setHistory(history ? null : e.currentTarget)} disabled={!chats.length}>
          <Clock size={15} />
        </IconButton>
        <IconButton label="New chat" onClick={reset} disabled={!turns.length}>
          <Plus size={16} />
        </IconButton>
        <IconButton label="Assistant settings" onClick={() => openDialog({ kind: "assistant-settings" })}>
          <Settings2 size={15} />
        </IconButton>
        <IconButton label="Close (⌘J)" onClick={() => toggle(false)}>
          <X size={16} />
        </IconButton>
      </header>
      {history && (
        <Popover anchor={history} onClose={() => setHistory(null)} align="right" width={320}>
          <div className="chat-history">
            {chats.map((c) => (
              <div key={c.id} className={`chat-history-item ${c.id === chatId ? "on" : ""}`}>
                <button
                  className="chat-history-open"
                  onClick={() => {
                    setHistory(null);
                    openChat(c.id);
                  }}
                >
                  <span className="chat-history-title">{c.title || "Untitled chat"}</span>
                  <span className="faint small">{age(c.updated)} ago</span>
                </button>
                <IconButton label="Delete this chat" onClick={() => deleteChat(c.id)}>
                  <Trash2 size={13} />
                </IconButton>
              </div>
            ))}
          </div>
        </Popover>
      )}
      <div className="assistant-body" ref={scroller}>
        {!configured && info ? (
          <div className="assistant-empty">
            <Sparkles size={26} className="assistant-mark" />
            <div className="empty-title">Connect the assistant to Claude</div>
            <p className="muted small">
              The assistant uses Claude in Microsoft Foundry, with your Azure API key, or in Google Vertex AI, with your gcloud login. It reads every cluster in Kube Desk, and changes them only after you approve each change.
            </p>
            <Button variant="primary" onClick={() => openDialog({ kind: "assistant-settings" })}>
              Set up
            </Button>
          </div>
        ) : turns.length === 0 ? (
          <div className="assistant-empty">
            <Sparkles size={26} className="assistant-mark" />
            <div className="empty-title">Ask about your clusters</div>
            <p className="muted small">It sees what you have open. Every change it proposes waits for your approval, with a diff and its impact.</p>
            <div className="assistant-suggestions">
              {suggestions.map((s) => (
                <button key={s} className="assistant-suggestion" onClick={() => submit(s)}>
                  {s}
                </button>
              ))}
            </div>
          </div>
        ) : (
          turns.map((t, i) =>
            t.role === "user" ? (
              <div key={i} className="chat-turn user">
                <div className="chat-user-text">{t.text}</div>
                <div className="chat-view faint">{t.view}</div>
              </div>
            ) : (
              <AssistantTurn key={i} t={t} last={i === turns.length - 1} />
            ),
          )
        )}
      </div>
      <div className="assistant-input">
        <textarea
          ref={input}
          value={text}
          rows={Math.min(8, Math.max(2, text.split("\n").length))}
          placeholder={configured ? "Ask anything about your clusters…  (Enter to send, Shift+Enter for a new line)" : "Set up the assistant first"}
          disabled={!configured}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
              e.preventDefault();
              submit();
            }
          }}
        />
        {running ? (
          <button className="assistant-send stop" title="Stop" onClick={stop}>
            <Square size={13} />
          </button>
        ) : (
          <button className="assistant-send" title="Send" onClick={() => submit()} disabled={!text.trim() || !configured}>
            <ArrowUp size={16} />
          </button>
        )}
      </div>
    </aside>
  );
}
