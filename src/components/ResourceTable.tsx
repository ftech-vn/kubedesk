import { useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { ArrowDown, ArrowUp } from "lucide-react";
import type { KObj } from "../lib/k8s";
import type { Column } from "../lib/resources";
import { useNow } from "../lib/watch";

const ROW = 36;

export interface RowAction {
  label: string;
  icon: ReactNode;
  run(o: KObj): void;
  show?(o: KObj): boolean;
  danger?: boolean;
}

interface Props {
  items: KObj[];
  columns: Column[];
  defaultSort?: string;
  selectedUid?: string;
  onOpen(o: KObj): void;
  actions?: RowAction[];
  checked?: Set<string>;
  onCheck?(next: Set<string>): void;
  onContextMenu?(o: KObj, x: number, y: number): void;
}

export function ResourceTable({ items, columns, defaultSort, selectedUid, onOpen, actions, checked, onCheck, onContextMenu }: Props) {
  const now = useNow(5000);
  const [sort, setSort] = useState<{ id: string; desc: boolean }>(() =>
    defaultSort?.startsWith("-") ? { id: defaultSort.slice(1), desc: true } : { id: defaultSort ?? "name", desc: false },
  );
  const scroller = useRef<HTMLDivElement>(null);
  const [viewport, setViewport] = useState({ top: 0, height: 600, width: 1200 });

  useLayoutEffect(() => {
    const el = scroller.current!;
    const ro = new ResizeObserver(() => setViewport((v) => ({ ...v, height: el.clientHeight, width: el.clientWidth })));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const visibleCols = useMemo(() => {
    if (viewport.width >= 980) return columns;
    return columns.filter((c) => !c.minor);
  }, [columns, viewport.width]);

  const sorted = useMemo(() => {
    const c = columns.find((x) => x.id === sort.id) ?? columns[0]!;
    const withKeys = items.map((o) => ({ o, k: c.value(o, now) }));
    withKeys.sort((a, b) => {
      let r = typeof a.k === "number" && typeof b.k === "number" ? a.k - b.k : String(a.k).localeCompare(String(b.k), undefined, { numeric: true });
      if (r === 0) r = a.o.metadata.name.localeCompare(b.o.metadata.name);
      return sort.desc ? -r : r;
    });
    return withKeys.map((x) => x.o);
    // Ages sort by timestamps, which do not change with now.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [items, columns, sort]);

  // Keep the selected row in view when it is opened from elsewhere.
  useEffect(() => {
    if (!selectedUid) return;
    const i = sorted.findIndex((o) => o.metadata.uid === selectedUid);
    const el = scroller.current;
    if (i < 0 || !el) return;
    const y = i * ROW;
    if (y < el.scrollTop || y + ROW > el.scrollTop + el.clientHeight - ROW) el.scrollTop = Math.max(0, y - el.clientHeight / 3);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedUid]);

  const template = [onCheck ? "36px" : null, ...visibleCols.map((c) => (typeof c.width === "number" ? `${c.width}px` : (c.width ?? "minmax(80px, 1fr)"))), actions?.length ? `${actions.length * 30 + 8}px` : null]
    .filter(Boolean)
    .join(" ");

  const first = Math.max(0, Math.floor(viewport.top / ROW) - 8);
  const last = Math.min(sorted.length, Math.ceil((viewport.top + viewport.height) / ROW) + 8);
  const allChecked = !!checked && sorted.length > 0 && sorted.every((o) => checked.has(o.metadata.uid));

  const toggle = (uid: string) => {
    if (!checked || !onCheck) return;
    const next = new Set(checked);
    if (next.has(uid)) next.delete(uid);
    else next.add(uid);
    onCheck(next);
  };

  return (
    <div className="table" ref={scroller} onScroll={(e) => {
        const top = e.currentTarget.scrollTop;
        setViewport((v) => ({ ...v, top }));
      }}>
      <div className="table-head" style={{ gridTemplateColumns: template }}>
        {onCheck && (
          <div className="th th-check">
            <input type="checkbox" checked={allChecked} onChange={() => onCheck(allChecked ? new Set() : new Set(sorted.map((o) => o.metadata.uid)))} />
          </div>
        )}
        {visibleCols.map((c) => (
          <button
            key={c.id}
            className={`th ${c.align === "right" ? "right" : ""} ${sort.id === c.id ? "sorted" : ""}`}
            onClick={() => setSort((s) => (s.id === c.id ? { id: c.id, desc: !s.desc } : { id: c.id, desc: false }))}
          >
            <span>{c.title}</span>
            {sort.id === c.id && (sort.desc ? <ArrowDown size={12} /> : <ArrowUp size={12} />)}
          </button>
        ))}
        {actions?.length ? <div className="th" /> : null}
      </div>
      <div className="table-body" style={{ height: sorted.length * ROW }}>
        {sorted.slice(first, last).map((o, i) => {
          const uid = o.metadata.uid;
          return (
            <div
              key={uid || `${o.metadata.namespace}/${o.metadata.name}`}
              className={`tr ${uid === selectedUid ? "selected" : ""} ${checked?.has(uid) ? "checked" : ""}`}
              style={{ gridTemplateColumns: template, transform: `translateY(${(first + i) * ROW}px)` }}
              onClick={() => onOpen(o)}
              onContextMenu={(e) => {
                if (!onContextMenu) return;
                e.preventDefault();
                onContextMenu(o, e.clientX, e.clientY);
              }}
            >
              {onCheck && (
                <div className="td td-check" onClick={(e) => e.stopPropagation()}>
                  <input type="checkbox" checked={checked?.has(uid) ?? false} onChange={() => toggle(uid)} />
                </div>
              )}
              {visibleCols.map((c) => (
                <div key={c.id} className={`td ${c.align === "right" ? "right" : ""} ${c.mono ? "mono" : ""}`}>
                  {c.render ? c.render(o, now) : String(c.value(o, now) || "—")}
                </div>
              ))}
              {actions?.length ? (
                <div className="td row-actions" onClick={(e) => e.stopPropagation()}>
                  {actions.map((a) =>
                    a.show && !a.show(o) ? (
                      <span key={a.label} className="row-action-placeholder" />
                    ) : (
                      <button key={a.label} className={`row-action ${a.danger ? "danger" : ""}`} title={a.label} onClick={() => a.run(o)}>
                        {a.icon}
                      </button>
                    ),
                  )}
                </div>
              ) : null}
            </div>
          );
        })}
      </div>
    </div>
  );
}
