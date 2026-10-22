# Changelog

## 0.4.1

- Opening AI agents, or comparing across clusters, no longer leaves the window blank.
- A dialog that fails now shows its error, with a button to close it, instead of blanking the app.

## 0.4.0

**AI agents, with you approving every change.** Kube Desk is now an MCP server, so that Claude Code, Cursor, Codex, Claude Desktop and other MCP clients can work with your clusters. Turn it on under AI agents (the robot in the title bar, or ⌘K), copy the command for your client, and choose for each cluster what agents may do: nothing, read, or propose changes. Agents can list and read resources, read logs and events, see what changed and who changed it, get incident reports, search and compare clusters, and preview what a change would affect. When an agent wants to apply YAML, delete, scale or restart, Kube Desk shows you the change, a diff of each object, what it affects and the agent's reason, and makes it only if you approve. Production clusters ask you to type their name, read-only clusters refuse, and changes carry the agent's name, so the timeline shows who made them. Secret values never reach agents, and tokens and passwords in logs are masked. The server listens on this Mac only, behind a token.

**Search every cluster at once.** ⇧⌘F, or "Search every cluster" in ⌘K, finds objects across all your clusters as you type: by name, `image:nginx`, `app=web`, `kind:svc`, `ns:shop`, `node:…`, `is:failing` or `status:CrashLoopBackOff`. Results come in cluster by cluster; click one to open it there.

**Compare across clusters.** "Compare across clusters" lines up a deployment, stateful set, daemon set, service, config map, secret, ingress, cron job or autoscaler in each cluster: images, replicas, resources, environment, ports, volumes, keys. Fields that differ are highlighted, and a YAML diff shows everything between two clusters. Secret values are compared by fingerprint only.

**See what Save changes.** Saving YAML now shows the diff the cluster will get, and says so when nothing changed.

## 0.3.0

**A timeline of what changed.** In the namespaces you watch, Kube Desk records every change while it runs and keeps it for 7 days: deployments, stateful sets, daemon sets, services, ingresses, config maps, secrets, cron jobs and autoscalers, pods restarting or becoming ready, nodes going down, and warning events. Each change says what changed in plain words ("image api: api:1 → api:2", "replicas 2 → 3", "env DATABASE_URL changed"), who changed it (kubectl, Helm, Argo CD, a controller), and shows a diff of before and after. When Kube Desk starts again, it finds what was changed, created or deleted while it was closed. Find it under Changes in the sidebar, in the History tab of each object, and on the overview. Secret values never reach the timeline: only which keys changed.

**See what a change breaks, before you make it.** Deleting, scaling, restarting and draining now show what else they affect: services left without endpoints, ingress routes that stop working, disruption budgets that would be broken, pods without a controller that would be gone for good, data in emptyDir and volumes that would be lost, config maps and secrets that pods need, pods that would not fit on the other nodes, autoscalers and Helm, Argo CD or Flux that would fight the change. Scaling updates the preview as you type the replicas.

**Incident reports in one click.** For a pod or a workload, Incident report gathers its state, conditions, events, the last lines of logs of every container and those before its last restart, its node, the recent changes of its namespace and what depends on it, into Markdown to paste into Slack, an issue or an AI assistant. Tokens, keys, passwords and credentials in URLs are masked.

## 0.2.0

**Watch namespaces, and hear about problems.** Watch a namespace or a whole cluster from the title bar, the ⌘K palette or the menu bar. Kube Desk keeps watching in the background, also with its window closed, and sends a notification when a pod crash loops, runs out of memory, fails to pull its image or cannot be scheduled, a rollout gets stuck, or a node goes down. A menu bar icon counts the problems and lists them; click one to open it. When many things break at once, you get a single notification.

**Logs of a whole workload.** Deployments, stateful sets, daemon sets, replica sets, jobs and services have a Logs tab with the logs of all their pods, interleaved by time and colored by pod. Pods that start during a rollout join in. Hide pods with a click, and save the logs shown to a file.

**Production guardrails.** Mark a cluster as production: it shows in red, and every change, from deleting to scaling or saving YAML, asks you to type the cluster's name. Read-only clusters refuse all changes and shells; the app enforces this itself, not just its buttons. Clusters whose names say "prod" offer to be protected.

**CPU and memory in lists.** Pods and nodes show what they use, from metrics-server, with a bar against their limits or what the node can allocate.

Also:

- Log lines are colored by level, for JSON, logfmt, klog and bracketed levels.
- The ⌘K palette no longer lists results that do not match.
- Completed pods no longer look unhealthy, long commands are folded, and badges are no longer cut off.

## 0.1.0

The first release of Kube Desk.

- Several clusters: the contexts of `~/.kube/config` and `$KUBECONFIG`, kubeconfig files read where they are, pasted kubeconfigs, and clusters added from a server address and a token. Rename, color, hide, reorder and remove them.
- Switch clusters from the rail, with ⌘1…⌘9, or from the ⌘K palette; each cluster keeps its page, namespace and open object.
- Live lists of nodes, namespaces, events, workloads, network, config, storage, RBAC and custom resources.
- Edit YAML, create resources from templates, delete, scale, restart, cordon, uncordon and drain, run cron jobs now.
- Pod logs, shells in containers and port forwards.
- Light and dark mode, or the system's.
- Updates itself from signed releases.
