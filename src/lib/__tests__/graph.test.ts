import { describe, expect, test } from "bun:test";
import { buildMap, layoutGroup, nodeId } from "../graph";
import type { KObj } from "../k8s";

let uid = 0;
const obj = (kind: string, name: string, extra: Partial<KObj> = {}, owner?: KObj): KObj => ({
  kind,
  ...extra,
  metadata: {
    name,
    namespace: "shop",
    uid: `u${++uid}`,
    creationTimestamp: `2026-10-0${(uid % 9) + 1}T00:00:00Z`,
    ...(extra.metadata ?? {}),
    ...(owner ? { ownerReferences: [{ apiVersion: "v1", kind: owner.kind!, name: owner.metadata.name, uid: owner.metadata.uid, controller: true }] } : {}),
  },
});
const running = { status: { phase: "Running", containerStatuses: [{ ready: true, restartCount: 0, state: { running: {} } }] }, spec: { containers: [{ name: "c" }] } };

describe("buildMap", () => {
  const web = obj("Deployment", "web", { spec: { replicas: 2 }, status: { readyReplicas: 2 } });
  const rs = obj("ReplicaSet", "web-abc", { spec: { replicas: 2 }, status: { replicas: 2, readyReplicas: 2 } }, web);
  const oldRs = obj("ReplicaSet", "web-old", { spec: { replicas: 0 }, status: { replicas: 0 } }, web);
  const pod = (name: string, extra: any = running) =>
    obj("Pod", name, { ...extra, metadata: { labels: { app: "web" } } as any, spec: { ...extra.spec, volumes: [{ configMap: { name: "web-config" } }, { secret: { secretName: "web-tls" } }, { projected: { sources: [{ configMap: { name: "kube-root-ca.crt" } }] } }] } }, rs);
  const pods = [pod("web-1"), pod("web-2")];
  const svc = obj("Service", "web", { spec: { selector: { app: "web" }, type: "ClusterIP" } });
  const lonely = obj("Service", "lonely", { spec: { selector: { app: "nothing" } } });
  const ing = obj("Ingress", "shop", { spec: { rules: [{ host: "shop.example", http: { paths: [{ backend: { service: { name: "web" } } }, { backend: { service: { name: "gone" } } }] } }] } });
  const cm = obj("ConfigMap", "web-config");
  const unused = obj("ConfigMap", "unused");

  test("links traffic, owners and config", () => {
    const groups = buildMap({ Deployment: [web], ReplicaSet: [rs, oldRs], Pod: pods, Service: [svc, lonely], Ingress: [ing], ConfigMap: [cm, unused], Secret: [] });
    const main = groups.find((g) => g.nodes.some((n) => n.name === "web" && n.kind === "Deployment"))!;
    const ids = main.nodes.map((n) => n.id).sort();
    expect(ids).toContain(nodeId("Ingress", "shop", "shop"));
    expect(ids).toContain(nodeId("ConfigMap", "shop", "web-config"));
    expect(ids).not.toContain(nodeId("ReplicaSet", "shop", "web-old"));
    expect(ids).not.toContain(nodeId("ConfigMap", "shop", "kube-root-ca.crt"));
    expect(ids).not.toContain(nodeId("ConfigMap", "shop", "unused"));
    // The service points at the deployment, not at each pod.
    expect(main.edges).toContainEqual({ from: nodeId("Service", "shop", "web"), to: nodeId("Deployment", "shop", "web") });
    expect(main.edges).toContainEqual({ from: nodeId("Deployment", "shop", "web"), to: nodeId("ReplicaSet", "shop", "web-abc") });
    // A service the ingress names, that does not exist.
    const gone = main.nodes.find((n) => n.name === "gone")!;
    expect(gone.missing).toBe(true);
    expect(main.nodes.find((n) => n.kind === "Ingress")!.tone).toBe("bad");
    // Secrets are not listed here: no "missing" guess for them.
    expect(ids).not.toContain(nodeId("Secret", "shop", "web-tls"));
    const alone = groups.find((g) => g.title === "lonely")!;
    expect(alone.nodes[0]!.tone).toBe("warn");
    // Groups with problems come first.
    expect(groups[0]!.worst).toBe("bad");
  });

  test("gathers many pods, and keeps the worst state", () => {
    const many = [1, 2, 3, 4, 5].map((i) => pod(`web-${i}`));
    many.push(pod("web-6", { status: { phase: "Running", containerStatuses: [{ ready: false, restartCount: 9, state: { waiting: { reason: "CrashLoopBackOff" } } }] }, spec: { containers: [{ name: "c" }] } }));
    const [g] = buildMap({ Deployment: [web], ReplicaSet: [rs], Pod: many, Service: [svc] }, { config: false });
    const group = g!.nodes.find((n) => n.kind === "Pod")!;
    expect(group.pods?.length).toBe(6);
    expect(group.tone).toBe("bad");
    expect(group.status).toBe("1 not ready");
    expect(g!.nodes.filter((n) => n.kind === "Pod").length).toBe(1);
  });

  test("follows Gateway API routes", () => {
    const gw = obj("Gateway", "public", { spec: { gatewayClassName: "eg", listeners: [{ protocol: "HTTPS", port: 443 }] }, status: { conditions: [{ type: "Programmed", status: "True" }] } });
    const route = obj("HTTPRoute", "shop", { spec: { parentRefs: [{ name: "public" }], hostnames: ["shop.example"], rules: [{ backendRefs: [{ name: "web", port: 80 }, { name: "nope", port: 80 }] }] } });
    const groups = buildMap({ Gateway: [gw], HTTPRoute: [route], Service: [svc], Deployment: [web], ReplicaSet: [rs], Pod: pods });
    const g = groups.find((x) => x.nodes.some((n) => n.kind === "Gateway"))!;
    expect(g.edges).toContainEqual({ from: nodeId("Gateway", "shop", "public"), to: nodeId("HTTPRoute", "shop", "shop") });
    expect(g.edges).toContainEqual({ from: nodeId("HTTPRoute", "shop", "shop"), to: nodeId("Service", "shop", "web") });
    expect(g.nodes.find((n) => n.name === "nope")!.missing).toBe(true);
    expect(g.nodes.find((n) => n.kind === "HTTPRoute")!.tone).toBe("bad");
    const l = layoutGroup(g);
    const x = (kind: string) => l.placed.find((p) => p.node.kind === kind)!.x;
    expect(x("Gateway")).toBeLessThan(x("HTTPRoute"));
    expect(x("HTTPRoute")).toBeLessThan(x("Service"));
  });

  test("lays out columns without overlaps", () => {
    const [g] = buildMap({ Deployment: [web], ReplicaSet: [rs], Pod: pods, Service: [svc], Ingress: [ing], ConfigMap: [cm] });
    const l = layoutGroup(g!);
    const seen = new Set<string>();
    for (const p of l.placed) {
      const key = `${p.x},${p.y}`;
      expect(seen.has(key)).toBe(false);
      seen.add(key);
    }
    const x = (kind: string) => l.placed.find((p) => p.node.kind === kind)!.x;
    expect(x("Ingress")).toBeLessThan(x("Service"));
    expect(x("Service")).toBeLessThan(x("Deployment"));
    expect(x("Pod")).toBeLessThan(x("ConfigMap"));
  });
});
