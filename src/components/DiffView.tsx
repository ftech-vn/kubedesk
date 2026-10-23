import { useMemo, useState } from "react";
import { diffLines, hunks } from "../lib/diff";
import type { ObjectDiff } from "../mygo";

/** DiffView shows the changes between two YAML texts, as a unified diff. */
export function DiffView({ before, after }: { before: string; after: string }) {
  const [all, setAll] = useState(false);
  const lines = useMemo(() => diffLines(before, after), [before, after]);
  const parts = useMemo(() => (all ? [{ lines }] : hunks(lines)), [lines, all]);
  const added = lines.filter((l) => l.kind === "add").length;
  const removed = lines.filter((l) => l.kind === "del").length;
  if (!added && !removed) return <div className="faint small pad">No difference in what the object asks for.</div>;
  return (
    <div className="diff">
      <div className="diff-head">
        <span className="diff-add">+{added}</span>
        <span className="diff-del">−{removed}</span>
        <span className="spacer" />
        <button className="link small" onClick={() => setAll(!all)}>
          {all ? "Changes only" : "Whole spec"}
        </button>
      </div>
      <pre className="diff-body">
        {parts.map((h, i) =>
          "skipped" in h ? (
            <div key={i} className="diff-skip">
              ⋯ {h.skipped} unchanged line{h.skipped === 1 ? "" : "s"}
            </div>
          ) : (
            h.lines.map((l, j) => (
              <div key={`${i}-${j}`} className={`diff-line ${l.kind}`}>
                <span className="diff-no">{l.a ?? ""}</span>
                <span className="diff-no">{l.b ?? ""}</span>
                <span className="diff-mark">{l.kind === "add" ? "+" : l.kind === "del" ? "−" : " "}</span>
                <span className="diff-text">{l.text || " "}</span>
              </div>
            ))
          ),
        )}
      </pre>
    </div>
  );
}

/** ObjectDiffs shows what applying a document does to each of its objects. */
export function ObjectDiffs({ diffs }: { diffs: ObjectDiff[] }) {
  return (
    <>
      {diffs.map((d, i) => (
        <div key={i} className="approval-diff">
          <div className="approval-diff-head">
            <span className={`badge tone-${d.action === "create" ? "ok" : d.action === "unchanged" ? "muted" : "info"}`}>{d.action}</span>
            <span className="mono small">
              {d.kind} {d.namespace ? `${d.namespace}/` : ""}
              {d.name}
            </span>
          </div>
          {d.action !== "unchanged" && <DiffView before={d.before} after={d.after} />}
        </div>
      ))}
    </>
  );
}
