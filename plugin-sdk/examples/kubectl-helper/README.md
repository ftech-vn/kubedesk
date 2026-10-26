# kubectl helper for Kube Desk

For the moments you want the terminal.

- **Copy kubectl command**, in the menu of any object: `kubectl --context <context> get <resource>/<name> -n <namespace> -o yaml`.
- **Copy kubectl use-context for this cluster**, in ⌘K.
- **Resources**, a tab of pods: the CPU and memory requests and limits of each container, and a warning when a
  container has no memory limit.

## Settings

The command (`kubectl`, `k`, `oc`…) and the output (`yaml`, `json`, `wide`).

Needs Kube Desk 0.8 or later.
