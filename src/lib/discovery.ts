import { useEffect, useState } from "react";
import { Kube, type APIResource } from "../mygo";
import { byKind, genericDef, type ResourceDef } from "./resources";

const cache = new Map<string, Promise<APIResource[]>>();

/** apiResources returns the kinds a cluster serves, asked once per cluster. */
export function apiResources(cluster: string): Promise<APIResource[]> {
  let p = cache.get(cluster);
  if (!p) {
    p = Kube.resources(cluster);
    p.catch(() => cache.delete(cluster));
    cache.set(cluster, p);
  }
  return p;
}

export function forgetResources(cluster: string) {
  cache.delete(cluster);
}

export function useApiResources(cluster: string | null): APIResource[] | null {
  const [res, setRes] = useState<APIResource[] | null>(null);
  useEffect(() => {
    if (!cluster) return;
    let alive = true;
    apiResources(cluster)
      .then((r) => alive && setRes(r))
      .catch(() => alive && setRes([]));
    return () => {
      alive = false;
    };
  }, [cluster]);
  return res;
}

/** findDef finds the list of a kind in a cluster, for links between objects. */
export async function findDef(cluster: string, kind: string, apiVersion?: string): Promise<ResourceDef | null> {
  const known = byKind.get(kind);
  const group = apiVersion?.includes("/") ? apiVersion.split("/")[0] : "";
  if (known && (apiVersion === undefined || known.gvr.group === group)) return known;
  const all = await apiResources(cluster).catch(() => [] as APIResource[]);
  const r = all.find((x) => x.kind === kind && (apiVersion === undefined || x.group === group));
  return r ? genericDef(r) : null;
}
