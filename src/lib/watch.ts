import { useCallback, useEffect, useState, useSyncExternalStore } from "react";
import { Channel } from "mygo-runtime";
import { Kube, type GVR, type WatchEvent } from "../mygo";
import { errorMessage, gvrKey, type KObj } from "./k8s";

export interface WatchState {
  items: KObj[];
  loading: boolean;
  error?: string;
}

interface Entry {
  cluster: string;
  gvr: GVR;
  namespace: string;
  items: Map<string, KObj>;
  pending: WatchEvent[];
  snapshot: WatchState;
  listeners: Set<() => void>;
  refs: number;
  channel: Channel<WatchEvent> | null;
  flushTimer: ReturnType<typeof setTimeout> | null;
  gcTimer: ReturnType<typeof setTimeout> | null;
  retryTimer: ReturnType<typeof setTimeout> | null;
}

// Lists stay watched for a while after the last view of them goes away, so
// that coming back to a page, or to a cluster, shows it at once.
const KEEP_ALIVE_MS = 90_000;
const entries = new Map<string, Entry>();
const EMPTY: WatchState = { items: [], loading: true };

const objKey = (o: KObj) => o.metadata.uid || `${o.metadata.namespace}/${o.metadata.name}`;

function start(e: Entry) {
  const ch = new Channel<WatchEvent>((ev) => {
    e.pending.push(ev);
    if (!e.flushTimer) e.flushTimer = setTimeout(() => flush(e), 60);
  });
  e.channel = ch;
  Kube.watch(e.cluster, e.gvr, e.namespace, ch)
    .catch((err) => {
      if (e.channel !== ch) return;
      e.snapshot = { ...e.snapshot, loading: false, error: errorMessage(err) };
      notify(e);
    })
    .finally(() => {
      if (e.channel !== ch) return;
      e.channel = null;
      // The watch ends only on failures it cannot retry: try again later.
      if (e.refs > 0) e.retryTimer = setTimeout(() => e.refs > 0 && !e.channel && start(e), 5000);
    });
}

function flush(e: Entry) {
  e.flushTimer = null;
  let error = e.snapshot.error;
  let changed = false;
  for (const ev of e.pending) {
    switch (ev.type) {
      case "SYNC":
        e.items = new Map((ev.items as KObj[] | undefined ?? []).map((o) => [objKey(o), o]));
        error = undefined;
        changed = true;
        break;
      case "ADDED":
      case "MODIFIED":
        if (ev.object) e.items.set(objKey(ev.object as KObj), ev.object as KObj);
        changed = true;
        break;
      case "DELETED":
        if (ev.object) e.items.delete(objKey(ev.object as KObj));
        changed = true;
        break;
      case "ERROR":
        error = ev.error;
        break;
    }
  }
  e.pending = [];
  const synced = changed || !e.snapshot.loading;
  e.snapshot = {
    items: changed ? [...e.items.values()] : e.snapshot.items,
    loading: !synced && !error,
    error,
  };
  notify(e);
}

function notify(e: Entry) {
  for (const l of e.listeners) l();
}

function stop(e: Entry, key: string) {
  e.channel?.close();
  e.channel = null;
  if (e.flushTimer) clearTimeout(e.flushTimer);
  if (e.retryTimer) clearTimeout(e.retryTimer);
  entries.delete(key);
}

function acquire(key: string, cluster: string, gvr: GVR, namespace: string): Entry {
  let e = entries.get(key);
  if (!e) {
    e = {
      cluster,
      gvr,
      namespace,
      items: new Map(),
      pending: [],
      snapshot: EMPTY,
      listeners: new Set(),
      refs: 0,
      channel: null,
      flushTimer: null,
      gcTimer: null,
      retryTimer: null,
    };
    entries.set(key, e);
  }
  if (e.gcTimer) {
    clearTimeout(e.gcTimer);
    e.gcTimer = null;
  }
  e.refs++;
  if (!e.channel && !e.retryTimer) start(e);
  return e;
}

function release(key: string) {
  const e = entries.get(key);
  if (!e) return;
  e.refs--;
  if (e.refs <= 0) e.gcTimer = setTimeout(() => stop(e, key), KEEP_ALIVE_MS);
}

/** dropCluster stops every watch of a cluster, after it reconnects. */
export function dropCluster(cluster: string) {
  for (const [key, e] of entries) {
    if (e.cluster !== cluster) continue;
    e.channel?.close();
    e.channel = null;
    if (e.refs > 0) {
      e.snapshot = { ...e.snapshot, loading: true, error: undefined };
      start(e);
      notify(e);
    } else stop(e, key);
  }
}

/**
 * useWatch keeps a live list of a resource: every object of it in namespace,
 * or in all namespaces with "". A null gvr watches nothing.
 */
export function useWatch(cluster: string | null, gvr: GVR | null, namespace = ""): WatchState {
  const key = cluster && gvr ? `${cluster}|${gvrKey(gvr)}|${namespace}` : "";
  const subscribe = useCallback(
    (cb: () => void) => {
      if (!key) return () => {};
      const e = acquire(key, cluster!, gvr!, namespace);
      e.listeners.add(cb);
      return () => {
        e.listeners.delete(cb);
        release(key);
      };
    },
    // The key names cluster, gvr and namespace.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [key],
  );
  const state = useSyncExternalStore(subscribe, () => (key ? (entries.get(key)?.snapshot ?? EMPTY) : EMPTY));
  return state;
}

/** usePoll lists a resource every interval, for APIs without watches (metrics). */
export function usePoll(cluster: string | null, gvr: GVR | null, namespace = "", intervalMs = 15000): WatchState {
  const [state, setState] = useState<WatchState>(EMPTY);
  const key = cluster && gvr ? `${cluster}|${gvrKey(gvr)}|${namespace}` : "";
  useEffect(() => {
    if (!key) return;
    let alive = true;
    const tick = () =>
      Kube.list(cluster!, gvr!, namespace)
        .then((items) => alive && setState({ items: items as KObj[], loading: false }))
        .catch((err) => alive && setState((s) => ({ ...s, loading: false, error: errorMessage(err) })));
    tick();
    const t = setInterval(tick, intervalMs);
    return () => {
      alive = false;
      clearInterval(t);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, intervalMs]);
  return state;
}

/** useNow re-renders every interval, for ages that tick. */
export function useNow(intervalMs = 5000) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(t);
  }, [intervalMs]);
  return now;
}
