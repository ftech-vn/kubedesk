<div align="center">

# Kube Desk

**A fast, native-feeling Kubernetes desktop client for people who juggle many clusters.**

Browse, debug and operate every cluster in your kubeconfig, and the ones you
paste in, from one small app. Switch clusters in a keystroke, watch resources
change live, tail logs, open a shell, edit YAML.

[Download for macOS](https://pub-e3b2030c705649ef87d35925ea3331eb.r2.dev/Kube-Desk-latest-arm64.dmg) ·
[Features](#features) ·
[Screenshots](#screenshots) ·
[Build from source](#build-from-source) ·
[Changelog](CHANGELOG.md) ·
[Tiếng Việt](README.vi.md)

![License: MIT](https://img.shields.io/badge/license-MIT-blue)
![Platform: macOS](https://img.shields.io/badge/platform-macOS%2012%2B-lightgrey)
![Go](https://img.shields.io/badge/Go-1.27-00ADD8)
![Built with MyGo](https://img.shields.io/badge/built%20with-MyGo-8b5cf6)

<img src="docs/screenshots/hero.png" alt="Kube Desk in light and dark mode" width="900">

</div>

## Why

Kube Desk takes the parts of [Headlamp](https://headlamp.dev) and
[Lens](https://k8slens.dev) you use every day and makes switching between
clusters the core of the app:

- **One app for every cluster.** It reads every context in `~/.kube/config` and
  `$KUBECONFIG`. Add more by picking a file, pasting a kubeconfig, or entering a
  server and a token.
- **Switching is instant.** Each cluster keeps its own page, namespace and open
  object, and its data stays live for a while after you leave. Coming back
  shows it as you left it.
- **Small and light.** One Go binary, about 47 MB, built with
  [MyGo](https://github.com/egoist/mygo). It uses the system's WebView instead
  of bundling Chromium.
- **Your kubeconfig is never modified.** Clusters you add in the app live in
  the app's own data folder.

## Features

**Clusters**
- Contexts from `~/.kube/config` and `$KUBECONFIG`, plus kubeconfig files read
  in place, so they pick up changes when `gcloud`, `aws` or `az` rewrite them
- Add a cluster by pasting a kubeconfig or entering an API server and bearer
  token (custom CA, or skip TLS verification)
- Rename, recolor, hide, drag to reorder and remove clusters; health and
  version at a glance
- Switch with the cluster rail, <kbd>⌘1</kbd>…<kbd>⌘9</kbd>, or the
  <kbd>⌘K</kbd> command palette. <kbd>⌘0</kbd> goes back to all clusters
- Exec credential plugins (GKE, EKS, AKS/kubelogin, OIDC) work even when the
  app is opened from Finder, because it loads your login shell's `PATH`

**Resources**, all updated live through watches
- Cluster: Nodes, Namespaces, Events
- Workloads: Pods, Deployments, StatefulSets, DaemonSets, ReplicaSets, Jobs, CronJobs
- Network: Services, Ingresses, EndpointSlices, NetworkPolicies
- Config: ConfigMaps, Secrets (reveal and copy values), HPAs, PodDisruptionBudgets
- Storage: PersistentVolumeClaims, PersistentVolumes, StorageClasses
- Access control: ServiceAccounts, Roles, RoleBindings, ClusterRoles, ClusterRoleBindings
- Custom resources: every CRD, with the columns it declares (`additionalPrinterColumns`)

**Watchlist and notifications**
- Watch a namespace, or a whole cluster, from the title bar or <kbd>⌘K</kbd>
- Kube Desk keeps watching in the background, also with its window closed,
  and sends a macOS notification when a pod crash loops, runs out of memory,
  fails to pull its image or cannot be scheduled, a rollout gets stuck, or a
  node goes down. When many things break at once, you get one notification
- A menu bar icon counts the problems and lists them. Click a problem, or its
  notification, to open the object; pause notifications for an hour

**Timeline of changes**
- In watched namespaces, every change is recorded for 7 days: what changed in
  plain words, who changed it (kubectl, Helm, Argo CD, a controller), and a
  diff of before and after. Restarts, readiness and warning events too
- Changes made while Kube Desk was closed are found when it starts again
- Secret values never reach the timeline, only which keys changed

**Before and after a problem**
- Deleting, scaling, restarting or draining shows first what else it breaks:
  services without endpoints, ingress routes, disruption budgets, pods with no
  controller, data in emptyDir or volumes, pods that would not fit elsewhere
- **Incident report**: a pod's or workload's state, events, logs (and those
  before the last restart), node and recent changes in one Markdown report,
  with tokens and passwords masked

**Production guardrails**
- Mark a cluster as production: it shows in red, and every change (delete,
  scale, restart, drain, saving YAML…) asks you to type the cluster's name
- Read-only clusters refuse every change and shell. The Go side enforces it,
  so it holds for every action, not just the buttons that are hidden
- Clusters whose names look like production offer to be protected

**Working with them**
- Cluster overview: node health, pod status, CPU and memory requested and used
  (with metrics-server), unhealthy pods and recent warnings
- Detail views for each kind: containers, ports, probes, conditions, owned
  pods, events, linked objects
- YAML editor with highlighting, folding and search; <kbd>⌘S</kbd> applies
- Create resources from YAML or templates, including several documents at once
- Delete (also in bulk), scale, rollout restart, cordon, uncordon and drain,
  run a CronJob now, suspend and resume
- **Logs**: live tail, container picker, previous container, timestamps,
  filter, wrap, coloring by level, save to a file
- **Logs of a whole workload**: every pod of a Deployment, StatefulSet,
  DaemonSet, Job or Service, interleaved by time and colored by pod; pods
  that start during a rollout join in
- **CPU and memory** of pods and nodes in their lists (with metrics-server)
- **Shell** into any container (bash, with sh as fallback)
- **Port forwarding** from pods and services, listed in the title bar
- Filter lists by text or `label=value`, sort any column, select rows for bulk
  actions

**App**
- Light, dark, or follow the system
- Updates itself: signed releases, delta updates, a Sparkle-style update window

## Screenshots

| | |
|---|---|
| ![Clusters](docs/screenshots/clusters.png) **All your clusters**, with health and version | ![Command palette](docs/screenshots/palette.png) **⌘K**: jump to any cluster, page, namespace or pod |
| ![Logs](docs/screenshots/logs.png) **Live logs**, colored by level | ![Shell](docs/screenshots/shell.png) **Shell** into a container |
| ![Deployment](docs/screenshots/deployment.png) **Details** of every kind, with the pods it owns | ![YAML](docs/screenshots/yaml.png) **Edit YAML** and apply with ⌘S |
| ![Overview](docs/screenshots/overview-light.png) **Cluster overview** | ![Add a cluster](docs/screenshots/add-cluster.png) **Add clusters** from files, pasted kubeconfigs or tokens |

## Install

### macOS

1. Download **[Kube-Desk-latest-arm64.dmg](https://pub-e3b2030c705649ef87d35925ea3331eb.r2.dev/Kube-Desk-latest-arm64.dmg)**
   (Apple silicon, macOS 12 or later) and drag Kube Desk to Applications.
2. Releases are not notarized yet. The first time, right-click the app and
   choose **Open**, or run:

   ```sh
   xattr -dr com.apple.quarantine "/Applications/Kube Desk.app"
   ```

After that, Kube Desk checks for updates daily. You can also check from
**Kube Desk → Check for Updates…**.

### Linux and Windows

The code is cross-platform (MyGo uses WebKitGTK on Linux and WebView2 on
Windows), but releases are only built for macOS so far. You can
[build it yourself](#build-from-source).

## Keyboard shortcuts

| Shortcut | Action |
|---|---|
| <kbd>⌘K</kbd> | Command palette: clusters, pages, namespaces, objects, theme, updates |
| <kbd>⌘1</kbd> … <kbd>⌘9</kbd> | Switch to the nth cluster |
| <kbd>⌘0</kbd> | All clusters |
| <kbd>⌘F</kbd> | Filter the current list |
| <kbd>⌘S</kbd> | Apply the YAML being edited |
| <kbd>Esc</kbd> | Close the details panel |

Right-click a row or a cluster for more actions.

## Build from source

Requirements: [Go](https://go.dev/dl/) 1.27 or later and [Bun](https://bun.sh).
No C toolchain is needed; the build never uses cgo.

```sh
git clone https://github.com/ftech-vn/kubedesk.git
cd kubedesk
bun install
bun run dev      # the app with live reload (Vite on port 5188)
bun run build    # build/darwin-arm64/Kube Desk.app and a .dmg
```

Cross-compile with `bun run build -- -platform darwin/universal,linux/amd64,windows/amd64`.

Tests:

```sh
go test ./...                 # unit tests
K8S_LIVE=1 go test ./...      # also reads (never changes) your current context
K8S_DEMO=1 KUBECONFIG=throwaway.yaml go test -run TestDemoCluster ./...  # end-to-end on a throwaway cluster
bunx tsc --noEmit             # type-check the frontend
```

## How it works

```
┌──────────────────────── Kube Desk (one Go binary) ────────────────────────┐
│                                                                           │
│   System WebView (WKWebView / WebKitGTK / WebView2)                       │
│   ┌─────────────────────────────────────────────────────────────────┐     │
│   │  React + TypeScript UI: shared watch cache, CodeMirror, xterm.js │     │
│   └───────────────▲─────────────────────────────────┬───────────────┘     │
│                   │ typed events / channels         │ typed calls         │
│   ┌───────────────┴─────────────────────────────────▼───────────────┐     │
│   │  Go services (generated TypeScript client via MyGo)             │     │
│   │  Clusters: kubeconfig sources, prefs, connection cache          │     │
│   │  Kube: list/watch/apply/delete, logs, exec, port-forward        │     │
│   └───────────────────────────────┬─────────────────────────────────┘     │
└───────────────────────────────────┼───────────────────────────────────────┘
                                    │ client-go (dynamic client, SPDY/WebSocket)
                                    ▼
                         Kubernetes API servers
```

| Path | |
|---|---|
| `clusters.go` | Kubeconfig sources, stored clusters, preferences, connection cache keyed by file modification times |
| `kube.go` | Generic list/watch over the dynamic client, apply, delete, scale, restart, cordon/drain, CronJobs |
| `streams.go` | Logs (batched), logs of whole workloads, exec with a TTY, port forwarding |
| `shellenv.go` | Loads `PATH`/`KUBECONFIG` from the login shell for credential plugins |
| `alerts.go` | Watchlist: informers per cluster, problem rules, notifications, menu bar |
| `timeline.go`, `recorder.go` | The timeline: describing changes, storage, finding changes made while closed |
| `impact.go` | What a delete, scale, restart or drain would affect |
| `report.go` | Incident reports, and masking secrets in logs |
| `updates.go` | Auto-updates and the application menu |
| `src/lib/` | Store (zustand), shared watch cache, resource definitions and columns |
| `src/views/`, `src/components/` | The UI |
| `scripts/release.sh` | Signed release build, published to Cloudflare R2 |

Each list in the UI goes through one shared watch per (cluster, resource,
namespace). The Go side lists, then watches from the list's resource version,
and starts over when the watch expires. The page batches changes and renders
them. Several views of the same list share one watch, and a watch stays open
for 90 seconds after its last view closes.

## Privacy and security

- Kube Desk only talks to the API servers in your kubeconfigs and to the
  update server. There is no telemetry.
- It never writes to your kubeconfig files. Pasted kubeconfigs and tokens are
  stored with `0600` permissions in
  `~/Library/Application Support/Kube Desk/kubeconfigs/`.
- Updates are signed with Ed25519, and the app verifies the signature before
  installing.

## Roadmap

- [ ] Notarized macOS builds, and Linux and Windows releases
- [x] Pod and node metrics in lists (0.2)
- [x] Logs of all the pods of a workload (0.2)
- [x] Watchlist with notifications and a menu bar icon (0.2)
- [x] Production and read-only clusters (0.2)
- [x] A local timeline of changes, also across restarts of the app (0.3)
- [x] One-click incident reports (0.3)
- [x] The impact of a change before deleting, scaling or draining (0.3)
- [ ] Compare a workload across clusters, and search every cluster at once
- [ ] An MCP server for AI agents, with approval of each change in the app
- [ ] Helm releases
- [ ] Resource diff before apply
- [ ] Plugins

## Contributing

Issues and pull requests are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md).

## Acknowledgements

- [MyGo](https://github.com/egoist/mygo) by [@egoist](https://github.com/egoist), the desktop framework
- [client-go](https://github.com/kubernetes/client-go), [CodeMirror](https://codemirror.net), [xterm.js](https://xtermjs.org), [Lucide](https://lucide.dev)
- [Headlamp](https://headlamp.dev) and [Lens](https://k8slens.dev), for the inspiration

## License

[MIT](LICENSE)
