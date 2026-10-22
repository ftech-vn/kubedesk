import type { Cluster } from "../mygo";

export const palette = ["#3b82f6", "#8b5cf6", "#ec4899", "#ef4444", "#f97316", "#eab308", "#22c55e", "#14b8a6", "#06b6d4", "#64748b"];

function hash(s: string) {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) | 0;
  return Math.abs(h);
}

export function clusterColor(c: Pick<Cluster, "id" | "color">): string {
  return c.color || palette[hash(c.id) % palette.length]!;
}

/** initials are the letters on a cluster's tile: "gke-prod-eu" → "GP". */
export function initials(name: string): string {
  const clean = name.replace(/^(arn:aws:eks:[^/]+\/|gke_[^_]+_[^_]+_)/, "");
  const words = clean.split(/[^a-zA-Z0-9]+/).filter(Boolean);
  if (words.length >= 2) return (words[0]![0]! + words[1]![0]!).toUpperCase();
  return (words[0] ?? name).slice(0, 2).toUpperCase();
}

/** shortName drops the long prefixes of managed clusters' context names. */
export function shortName(name: string): string {
  const eks = /^arn:aws:eks:[^:]+:\d+:cluster\/(.+)$/.exec(name);
  if (eks) return eks[1]!;
  const gke = /^gke_[^_]+_[^_]+_(.+)$/.exec(name);
  if (gke) return gke[1]!;
  return name;
}

export function sourceLabel(c: Cluster): string {
  switch (c.sourceKind) {
    case "default":
      return "kubeconfig";
    case "file":
      return c.source.split(/[\\/]/).pop() ?? c.source;
    case "stored":
      return "added in app";
  }
}
