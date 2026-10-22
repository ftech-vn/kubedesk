import { useEffect, useMemo, useRef, useState } from "react";
import { Channel } from "mygo-runtime";
import { ArrowDownToLine, Download, Eraser, Pause, Play, Search, WrapText } from "lucide-react";
import { Kube, type LogLine } from "../mygo";
import { errorMessage, type KObj } from "../lib/k8s";
import { run, toast } from "../lib/store";
import { Button, IconButton, Spinner } from "./ui";

const MAX_LINES = 50_000;
// Lines are colored by level up to this many; more render as plain text,
// which stays fast with tens of thousands of lines.
const COLOR_LINES = 5_000;

function levelOf(line: string): string {
  const m = /\b(?:level|lvl|severity)"?\s*[=:]\s*"?(\w+)|\b(ERROR|ERR|FATAL|PANIC|WARN|WARNING)\b|^\S*\s*([EWF])\d{4}\s/i.exec(line);
  const v = (m?.[1] ?? m?.[2] ?? m?.[3] ?? "").toLowerCase();
  if (/^(error|err|fatal|panic|critical|crit|e|f)$/.test(v)) return "lv-error";
  if (/^(warn|warning|w)$/.test(v)) return "lv-warn";
  if (v === "debug" || v === "trace") return "lv-debug";
  return "";
}

/** LogSource is what a logs view shows: one container, or a workload's pods. */
export type LogSource =
  | { kind: "pod"; pod: KObj; initialContainer?: string }
  | { kind: "workload"; name: string; namespace: string; selector: Record<string, string>; containers: string[] };

interface Line {
  pod?: string;
  container?: string;
  time?: string;
  text: string;
  note?: boolean;
}

const podHues = [210, 150, 35, 280, 340, 190, 95, 15, 250, 55];

function hueOf(pod: string) {
  let h = 0;
  for (let i = 0; i < pod.length; i++) h = (h * 31 + pod.charCodeAt(i)) | 0;
  return podHues[Math.abs(h) % podHues.length]!;
}

/** shortPod drops the workload's name from the names of its pods. */
function shortPod(pod: string, workload: string) {
  return pod.startsWith(workload + "-") ? pod.slice(workload.length + 1) : pod;
}

function clock(time?: string) {
  if (!time) return "";
  const d = new Date(time);
  return Number.isNaN(d.getTime()) ? time : d.toLocaleTimeString(undefined, { hour12: false }) + "." + String(d.getMilliseconds()).padStart(3, "0");
}

export function LogsView(props: { cluster: string; pod: KObj; initialContainer?: string } | { cluster: string; source: LogSource }) {
  const source: LogSource = "source" in props ? props.source : { kind: "pod", pod: props.pod, initialContainer: props.initialContainer };
  const cluster = props.cluster;
  const workload = source.kind === "workload";
  const containers: string[] =
    source.kind === "pod" ? [...(source.pod.spec?.initContainers ?? []), ...(source.pod.spec?.containers ?? [])].map((c: any) => c.name) : source.containers;
  const [container, setContainer] = useState(source.kind === "pod" ? (source.initialContainer ?? source.pod.spec?.containers?.[0]?.name ?? "") : "");
  const [tail, setTail] = useState(workload ? 200 : 500);
  const [follow, setFollow] = useState(true);
  const [previous, setPrevious] = useState(false);
  const [timestamps, setTimestamps] = useState(false);
  const [wrap, setWrap] = useState(false);
  const [filter, setFilter] = useState("");
  const [hiddenPods, setHiddenPods] = useState<Set<string>>(new Set());
  const [lines, setLines] = useState<Line[]>([]);
  const [status, setStatus] = useState<"loading" | "streaming" | "ended" | "error">("loading");
  const [error, setError] = useState("");
  const [paused, setPaused] = useState(false);
  const [stick, setStick] = useState(true);
  const buffer = useRef<Line[]>([]);
  const scroller = useRef<HTMLDivElement>(null);
  const pausedRef = useRef(paused);
  pausedRef.current = paused;

  const podKey = source.kind === "pod" ? `${source.pod.metadata.namespace}/${source.pod.metadata.name}` : `${source.namespace}/${JSON.stringify(source.selector)}`;

  useEffect(() => {
    buffer.current = [];
    setLines([]);
    setStatus("loading");
    setError("");
    let flushTimer: ReturnType<typeof setTimeout> | null = null;
    const flush = () => {
      flushTimer = null;
      if (!pausedRef.current) setLines(buffer.current.slice());
    };
    const push = (batch: Line[]) => {
      setStatus("streaming");
      const b = buffer.current;
      for (const l of batch) b.push(l);
      if (b.length > MAX_LINES) b.splice(0, b.length - MAX_LINES);
      if (!flushTimer) flushTimer = setTimeout(flush, 50);
    };
    const opts = { container, tailLines: tail, follow, previous, timestamps };
    let ch: Channel<string[]> | Channel<LogLine[]>;
    let done: Promise<void>;
    if (source.kind === "pod") {
      const c = new Channel<string[]>((batch) => push(batch.map((text) => ({ text }))));
      ch = c;
      done = Kube.logs(cluster, source.pod.metadata.namespace ?? "", source.pod.metadata.name, opts, c);
    } else {
      const c = new Channel<LogLine[]>((batch) => push(batch));
      ch = c;
      done = Kube.workloadLogs(cluster, source.namespace, source.selector, container, { ...opts, timestamps: true }, c);
    }
    done
      .then(() => {
        flush();
        setStatus("ended");
      })
      .catch((e) => {
        setStatus("error");
        setError(errorMessage(e));
      });
    return () => {
      ch.close();
      if (flushTimer) clearTimeout(flushTimer);
    };
    // podKey names the source.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cluster, podKey, container, tail, follow, previous, timestamps]);

  useEffect(() => {
    if (!paused) setLines(buffer.current.slice());
  }, [paused]);

  const pods = useMemo(() => {
    if (!workload) return [];
    return [...new Set(lines.filter((l) => l.pod && !l.note).map((l) => l.pod!))].sort();
  }, [lines, workload]);

  const shown = useMemo(() => {
    let out = lines;
    if (hiddenPods.size) out = out.filter((l) => !l.pod || !hiddenPods.has(l.pod));
    if (filter) {
      const f = filter.toLowerCase();
      out = out.filter((l) => l.text.toLowerCase().includes(f));
    }
    return out;
  }, [lines, filter, hiddenPods]);

  useEffect(() => {
    const el = scroller.current;
    if (el && stick) el.scrollTop = el.scrollHeight;
  }, [shown, stick]);

  const onScroll = () => {
    const el = scroller.current;
    if (!el) return;
    setStick(el.scrollHeight - el.scrollTop - el.clientHeight < 30);
  };

  const name = source.kind === "pod" ? source.pod.metadata.name : source.name;
  const prefix = (l: Line) => (workload && l.pod ? `[${shortPod(l.pod, name)}${containers.length > 1 && l.container ? `/${l.container}` : ""}] ` : "");
  const asText = (l: Line) => `${workload && timestamps && l.time ? `${l.time} ` : ""}${prefix(l)}${l.text}`;

  const download = async () => {
    const path = await run("Could not save the logs", () => Kube.saveText(`${name}.log`, shown.map(asText).join("\n") + "\n"));
    if (path) toast.ok("Logs saved", path);
  };

  const togglePod = (p: string) => {
    const next = new Set(hiddenPods);
    if (next.has(p)) next.delete(p);
    else next.add(p);
    setHiddenPods(next);
  };

  return (
    <div className="logs">
      <div className="logs-toolbar">
        {(containers.length > 1 || workload) && (
          <select value={container} onChange={(e) => setContainer(e.target.value)} className="select-sm">
            {workload && <option value="">All containers</option>}
            {containers.map((c) => (
              <option key={c}>{c}</option>
            ))}
          </select>
        )}
        <select value={tail} onChange={(e) => setTail(Number(e.target.value))} className="select-sm" title={workload ? "Lines to start with, of each container" : "Lines to start with"}>
          {[100, 200, 500, 1000, 5000, 0].map((n) => (
            <option key={n} value={n}>
              {n === 0 ? "All lines" : `Last ${n}${workload ? " each" : ""}`}
            </option>
          ))}
        </select>
        <label className="check">
          <input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} /> Follow
        </label>
        <label className="check">
          <input type="checkbox" checked={timestamps} onChange={(e) => setTimestamps(e.target.checked)} /> Timestamps
        </label>
        <label className="check">
          <input type="checkbox" checked={previous} onChange={(e) => setPrevious(e.target.checked)} /> Previous
        </label>
        <div className="search-input small">
          <Search size={13} />
          <input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="Filter lines" />
        </div>
        <div className="spacer" />
        {status === "loading" && <Spinner size={14} />}
        {status === "streaming" && follow && <span className="live-dot" title="Streaming" />}
        <IconButton label={wrap ? "Don't wrap lines" : "Wrap lines"} className={wrap ? "on" : ""} onClick={() => setWrap(!wrap)}>
          <WrapText size={15} />
        </IconButton>
        <IconButton label={paused ? "Resume" : "Pause"} onClick={() => setPaused(!paused)}>
          {paused ? <Play size={15} /> : <Pause size={15} />}
        </IconButton>
        <IconButton label="Save the lines shown…" onClick={download} disabled={!shown.length}>
          <Download size={15} />
        </IconButton>
        <IconButton
          label="Clear"
          onClick={() => {
            buffer.current = [];
            setLines([]);
          }}
        >
          <Eraser size={15} />
        </IconButton>
      </div>
      {workload && pods.length > 0 && (
        <div className="pod-chips">
          {pods.map((p) => (
            <button
              key={p}
              className={`pod-chip ${hiddenPods.has(p) ? "off" : ""}`}
              style={{ "--hue": hueOf(p) } as React.CSSProperties}
              onClick={() => togglePod(p)}
              title={hiddenPods.has(p) ? `Show ${p}` : `Hide ${p}`}
            >
              <span className="pod-chip-dot" />
              {shortPod(p, name)}
            </button>
          ))}
          {hiddenPods.size > 0 && (
            <button className="link small" onClick={() => setHiddenPods(new Set())}>
              Show all
            </button>
          )}
        </div>
      )}
      <div className="logs-body" ref={scroller} onScroll={onScroll}>
        {status === "error" ? (
          <pre className="logs-error">{error}</pre>
        ) : (
          <pre className={`logs-text ${wrap ? "wrap" : ""}`}>
            {filter || shown.length <= COLOR_LINES ? shown.map((l, i) => <LineView key={i} line={l} term={filter} workload={workload} name={name} timestamps={timestamps} showContainer={containers.length > 1} />) : shown.map(asText).join("\n")}
            {status === "ended" && <div className="logs-end">— end of logs —</div>}
            {status === "streaming" && workload && shown.length === 0 && <div className="logs-end">waiting for lines…</div>}
          </pre>
        )}
      </div>
      {!stick && (
        <Button className="logs-jump" size="sm" icon={<ArrowDownToLine size={14} />} onClick={() => setStick(true)}>
          Latest
        </Button>
      )}
    </div>
  );
}

function LineView({ line, term, workload, name, timestamps, showContainer }: { line: Line; term: string; workload: boolean; name: string; timestamps: boolean; showContainer: boolean }) {
  if (line.note) return <div className="logs-note">— {line.text} —</div>;
  const cls = levelOf(line.text);
  const text = line.text;
  const i = term ? text.toLowerCase().indexOf(term.toLowerCase()) : -1;
  return (
    <div className={cls}>
      {workload && timestamps && line.time && <span className="log-time">{clock(line.time)} </span>}
      {workload && line.pod && (
        <span className="log-pod" style={{ "--hue": hueOf(line.pod) } as React.CSSProperties}>
          {shortPod(line.pod, name)}
          {showContainer && line.container ? `/${line.container}` : ""}
        </span>
      )}
      {i < 0 ? (
        text || " "
      ) : (
        <>
          {text.slice(0, i)}
          <mark>{text.slice(i, i + term.length)}</mark>
          {text.slice(i + term.length)}
        </>
      )}
    </div>
  );
}
