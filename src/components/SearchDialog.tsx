import { useEffect, useMemo, useRef, useState } from "react";
import { Channel } from "mygo-runtime";
import { Search } from "lucide-react";
import { Kube, type SearchHit } from "../mygo";
import { useApp } from "../lib/store";
import { errorMessage } from "../lib/k8s";
import { Spinner } from "./ui";
import { ClusterTile } from "./ClusterRail";

const examples = ["image:nginx", "is:failing", "app=web", "kind:svc api", "status:CrashLoopBackOff", "ns:kube-system node:"];

/** SearchDialog looks for objects in every cluster at once. */
export function SearchDialog({ initial, onClose }: { initial?: string; onClose(): void }) {
  const clusters = useApp((s) => s.clusters);
  const [text, setText] = useState(initial ?? "");
  const [query, setQuery] = useState(initial ?? "");
  const [hits, setHits] = useState<SearchHit[]>([]);
  const [running, setRunning] = useState(false);
  const [error, setError] = useState("");
  const input = useRef<HTMLInputElement>(null);

  useEffect(() => input.current?.focus(), []);

  useEffect(() => {
    if (!query.trim()) {
      setHits([]);
      return;
    }
    setHits([]);
    setError("");
    setRunning(true);
    const ch = new Channel<SearchHit[]>((batch) => setHits((h) => [...h, ...batch]));
    Kube.search({ text: query }, ch)
      .catch((e) => setError(errorMessage(e)))
      .finally(() => setRunning(false));
    return () => ch.close();
  }, [query]);

  // Lists are cached for a moment, so searching as one types stays cheap.
  useEffect(() => {
    const t = setTimeout(() => setQuery(text), 350);
    return () => clearTimeout(t);
  }, [text]);

  const groups = useMemo(() => {
    const m = new Map<string, SearchHit[]>();
    for (const h of hits) m.set(h.cluster, [...(m.get(h.cluster) ?? []), h]);
    return [...m.entries()];
  }, [hits]);

  const open = (h: SearchHit) => {
    onClose();
    const s = useApp.getState();
    s.activate(h.cluster);
    s.openDetail({ gvr: h.gvr, kind: h.kind, namespace: h.namespace, name: h.name }, true);
  };

  const count = hits.filter((h) => !h.error).length;
  return (
    <div className="modal-backdrop palette-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="palette search-everywhere" onKeyDown={(e) => e.key === "Escape" && onClose()}>
        <div className="palette-input">
          <Search size={16} className="muted" />
          <input ref={input} value={text} onChange={(e) => setText(e.target.value)} onKeyDown={(e) => e.key === "Enter" && setQuery(text)} placeholder="Search every cluster: names, image:, app=web, is:failing…" />
          {running && <Spinner size={14} />}
        </div>
        <div className="palette-list">
          {!query.trim() ? (
            <div className="search-help">
              <div className="faint small">Search all {clusters.filter((c) => !c.hidden).length} clusters at once. Try:</div>
              <div className="chips">
                {examples.map((x) => (
                  <button key={x} className="chip" onClick={() => setText(x)}>
                    <span className="chip-val">{x}</span>
                  </button>
                ))}
              </div>
            </div>
          ) : (
            <>
              {error && <div className="form-error">{error}</div>}
              {!running && count === 0 && !error && <div className="faint pad">Nothing matches in any cluster.</div>}
              {groups.map(([cluster, list]) => {
                const c = clusters.find((x) => x.id === cluster);
                return (
                  <div key={cluster}>
                    <div className="palette-group row gap">
                      {c && <ClusterTile c={c} size={16} />}
                      {list[0]!.clusterName}
                      <span className="faint">{list.filter((h) => !h.error).length}</span>
                    </div>
                    {list.map((h, i) =>
                      h.error ? (
                        <div key={i} className="faint small pad text-bad">
                          {h.error}
                        </div>
                      ) : (
                        <button key={i} className="palette-item" onClick={() => open(h)}>
                          <span className="search-kind">{h.kind}</span>
                          <span className="palette-label">
                            {h.namespace ? <span className="muted">{h.namespace}/</span> : null}
                            {h.name}
                          </span>
                          {h.detail && <span className="palette-hint search-detail">{h.detail}</span>}
                        </button>
                      ),
                    )}
                  </div>
                );
              })}
            </>
          )}
        </div>
        <div className="palette-foot faint small">
          {query.trim() ? `${count} result${count === 1 ? "" : "s"}${running ? ", searching…" : ""}` : "Filters: image: label: (or key=value) kind: ns: node: is:failing status:"}
        </div>
      </div>
    </div>
  );
}
