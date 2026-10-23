import { create } from "zustand";
import { Channel } from "mygo-runtime";
import { Assistant, type AssistantInfo, type ChatEvent, type ChatView } from "../mygo";
import { errorMessage } from "./k8s";
import { useApp } from "./store";

export type Part =
  | { kind: "text"; text: string }
  | { kind: "thinking"; text: string }
  | { kind: "tool"; id: string; tool: string; input: string; status: "running" | "ok" | "error"; result: string; write: boolean }
  | { kind: "note"; text: string; error: boolean };

export type Turn =
  | { role: "user"; text: string; view: string }
  | { role: "assistant"; parts: Part[]; done: boolean; error?: string; tokens?: { input: number; cached: number; output: number } };

interface AssistantStore {
  open: boolean;
  chatId: string;
  turns: Turn[];
  running: boolean;
  info: AssistantInfo | null;
  channel: Channel<ChatEvent> | null;
  toggle(open?: boolean): void;
  loadInfo(): Promise<void>;
  send(text: string): Promise<void>;
  stop(): void;
  reset(): void;
}

const newId = () => crypto.randomUUID();

/** currentView is what the user looks at, for the assistant. */
function currentView(): { view: ChatView; label: string } {
  const s = useApp.getState();
  if (!s.active) return { view: {}, label: "All clusters" };
  const v = s.views[s.active];
  const c = s.clusters.find((x) => x.id === s.active);
  const top = v?.details[v.details.length - 1];
  const view: ChatView = { cluster: s.active, namespace: v?.namespace || undefined, section: v?.section, kind: top?.kind, name: top ? (top.namespace ? `${top.namespace}/${top.name}` : top.name) : undefined };
  const label = [c?.name, v?.namespace || "all namespaces", top ? `${top.kind} ${top.name}` : null].filter(Boolean).join(" · ");
  return { view, label };
}

export const useAssistant = create<AssistantStore>((set, get) => ({
  open: false,
  chatId: newId(),
  turns: [],
  running: false,
  info: null,
  channel: null,

  toggle(open) {
    const next = open ?? !get().open;
    set({ open: next });
    if (next && !get().info) get().loadInfo();
  },

  async loadInfo() {
    try {
      set({ info: await Assistant.info() });
    } catch {
      /* shown when sending */
    }
  },

  async send(text) {
    const q = text.trim();
    if (!q || get().running) return;
    const { view, label } = currentView();
    const assistant: Turn = { role: "assistant", parts: [], done: false };
    set((s) => ({ turns: [...s.turns, { role: "user", text: q, view: label }, assistant], running: true }));

    // Each event updates the last turn, which is the answer being written.
    const update = (f: (t: Extract<Turn, { role: "assistant" }>) => void) =>
      set((s) => {
        const turns = s.turns.slice();
        const last = turns[turns.length - 1];
        if (!last || last.role !== "assistant") return s;
        const copy = { ...last, parts: last.parts.slice() };
        f(copy);
        turns[turns.length - 1] = copy;
        return { turns };
      });
    const append = (kind: "text" | "thinking", text: string) =>
      update((t) => {
        const prev = t.parts[t.parts.length - 1];
        if (prev && prev.kind === kind) t.parts[t.parts.length - 1] = { ...prev, text: prev.text + text };
        else t.parts.push({ kind, text });
      });

    const ch = new Channel<ChatEvent>((e) => {
      switch (e.type) {
        case "text":
          append("text", e.text ?? "");
          break;
        case "thinking":
          append("thinking", e.text ?? "");
          break;
        case "tool_start":
          update((t) => t.parts.push({ kind: "tool", id: e.toolId ?? "", tool: e.tool ?? "", input: "", status: "running", result: "", write: !!e.write }));
          break;
        case "tool_end":
          update((t) => {
            const i = t.parts.findIndex((p) => p.kind === "tool" && p.id === e.toolId);
            const part: Part = { kind: "tool", id: e.toolId ?? "", tool: e.tool ?? "", input: e.input ?? "", status: e.isError ? "error" : "ok", result: e.text ?? "", write: !!e.write };
            if (i >= 0) t.parts[i] = part;
            else t.parts.push(part);
          });
          break;
        case "note":
          update((t) => t.parts.push({ kind: "note", text: e.text ?? "", error: !!e.isError }));
          break;
        case "done":
          update((t) => {
            t.done = true;
            t.tokens = { input: e.inputTokens ?? 0, cached: e.cachedTokens ?? 0, output: e.outputTokens ?? 0 };
          });
          break;
      }
    });
    set({ channel: ch });
    try {
      await Assistant.send(get().chatId, q, view, ch);
    } catch (e) {
      update((t) => {
        t.error = errorMessage(e);
      });
    } finally {
      update((t) => {
        t.done = true;
        // Tools cut off by a stop never finished.
        t.parts = t.parts.map((p) => (p.kind === "tool" && p.status === "running" ? { ...p, status: "error", result: "stopped" } : p));
      });
      set({ running: false, channel: null });
    }
  },

  stop() {
    get().channel?.close();
  },

  reset() {
    get().channel?.close();
    Assistant.reset(get().chatId).catch(() => {});
    set({ chatId: newId(), turns: [], running: false, channel: null });
  },
}));
