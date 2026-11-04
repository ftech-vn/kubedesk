# Stacks

A stack is a set of components that run together in a namespace, such as
Loki, Alloy and Grafana for logs. The Stacks page of a cluster lists them:
press **Run**, fill in a few settings, and Kube Desk installs the components
in order into the namespace, making it if it is missing.

Kube Desk comes with stacks for logs (Loki + Grafana, the Elastic Stack,
OpenSearch + Fluent Bit), metrics (Prometheus + Grafana), databases
(PostgreSQL with CloudNativePG, Valkey), Kafka, Argo CD, and Traefik with
cert-manager. Their definitions are in [`stacks/`](../stacks).

## Your own stacks

A stack is a YAML file. Yours live in the `stacks` folder of the app's data
(**Stacks → folder icon** opens it), and **New stack** writes one in the app,
checking it as you type. **Duplicate as my stack**, in the menu of a stack,
starts from one that exists. A stack of yours with the id of a built-in one
replaces it.

```yaml
id: my-stack                 # lowercase letters, digits and dashes
name: My stack
category: Logging            # groups the stacks on the page
description: What it runs.
namespace: my-stack          # the default; the user can pick another
requires: About 1 GB of memory.
fields:                      # the settings asked before running
  - key: replicas
    label: Replicas
    type: number             # see the types below
    default: "1"
    required: true           # marked *; the others are marked optional
    section: App             # fields are grouped under their section
    pattern: '^[1-9]$'       # a regular expression the value must match
  - key: password
    label: Password
    type: password
    generate: true           # a random password when left empty
  - key: size
    label: Size
    type: select
    options: [small, large]
  - key: withUI
    label: Add the web UI
    type: bool
  - key: domain
    label: App domain
    type: domain
    section: Domain
    placeholder: app.example.com
  - key: ingressClass
    label: Ingress class
    type: ingressClass       # picked from the cluster's ingress classes
    section: Domain
    showIf: domain           # asked only once the domain is filled in
  - key: tlsIssuer
    label: HTTPS certificate from
    type: clusterIssuer      # picked from the cluster's cert-manager issuers
    section: Domain
    showIf: domain
  - key: storageClass
    label: Storage class
    type: storageClass       # picked from the cluster's storage classes
    advanced: true           # folded under "More settings"
components:                  # installed in this order
  - name: app                # also the Helm release's name
    title: The app
    manifest: |              # applied by Kube Desk; no tool needed
      apiVersion: apps/v1
      kind: Deployment
      ...
  - name: ui
    when: withUI             # only when this bool field is on
    helm:                    # a chart; needs the helm command
      repo: https://charts.example.com     # or chart: oci://…
      chart: ui
      version: 1.2.3
      values: |
        password: {{ quote .password }}
  - name: operator
    namespace: my-operator   # elsewhere than the stack, shared with others
    urls:                    # downloaded and applied by Kube Desk
      - https://example.com/releases/v1.0.0/install.yaml
access:                      # web pages, opened through a port forward
  - label: The app
    service: app
    port: 80
    scheme: http             # or https
    path: /
    user: admin
    password: password       # a password field, or:
    # passwordSecret: secret-name/key
    domain: domain           # the domain field it is published at
    tls: tlsIssuer           # the issuer field that makes it https
    # noOpen: true           # an address for programs, such as where
    #                          servers send logs: copied, not opened, and
    #                          shown only once its domain is filled in
notes: |
  Markdown shown once the stack runs. {{ .Namespace }} works here too.
```

### Fields

| Type | |
|---|---|
| `text` | The default |
| `password` | Hidden, kept in the stack's Secret; `generate: true` makes one when left empty |
| `number`, `bool`, `select` (with `options`) | |
| `domain` | A host name, such as `grafana.example.com`, lower-cased and checked |
| `email` | An email address, checked |
| `ingressClass`, `clusterIssuer`, `storageClass` | Picked from what the cluster has; empty means the cluster's default, or no certificate |

`required: true` refuses an empty value; `pattern` checks the value against a
regular expression; `showIf` names a field that must be on, or filled in, for
this one to be asked; a field that is not asked is not checked either.
`section` groups fields under a heading, `placeholder` shows an example, and
`advanced: true` folds a field under "More settings".

### Domains

A page that people should reach from outside the cluster takes a `domain`
field, usually in a section of its own with an `ingressClass` and a
`clusterIssuer` field that only show once the domain is filled in. The stack's
values or manifest then make the Ingress, for example:

```yaml
{{- if .domain }}
ingress:
  enabled: true
  {{- if .ingressClass }}
  ingressClassName: {{ .ingressClass }}
  {{- end }}
  {{- if .tlsIssuer }}
  annotations:
    cert-manager.io/cluster-issuer: {{ .tlsIssuer }}
  tls:
    - secretName: app-tls
      hosts: [{{ .domain }}]
  {{- end }}
  hosts: [{{ .domain }}]
{{- end }}
```

In the Run dialog, the section of the domain fields says where each page will
be, and where to point their DNS: the external addresses of the cluster's
ingress controllers. It warns when the cluster has no ingress controller, or
no issuer for HTTPS. An access entry with `domain` (and `tls`) links to its
page at the domain once the stack runs. One with `noOpen: true` is an address
for programs rather than people, such as the push endpoint the Loki stack
publishes for servers outside the cluster: Kube Desk offers to copy it, and its
password, instead of opening it.

### Components

Each component is one of:

- `manifest`: Kubernetes objects written in the stack. Kube Desk applies them
  server side, custom resource definitions first, and waits for the kinds
  they define to be served before applying objects of those kinds.
- `urls`: manifests downloaded from those addresses, applied the same way.
- `helm`: a Helm chart from a repository or an `oci://` reference, installed
  with `helm upgrade --install --wait`. This needs the helm command.

Objects without a namespace go into the component's namespace: the stack's,
or the component's own `namespace`.

Once applied, Kube Desk waits for the component to be ready: deployments,
stateful sets and daemon sets with all their pods ready, jobs finished,
definitions established, and custom resources whose status says `Ready`, a
`health` of green or yellow, or a ready `phase`. `noWait: true` goes on at
once; `timeout: 20m` waits longer than the 15 minutes of the default.

### Templates

Values, manifests, URLs and component namespaces are
[Go templates](https://pkg.go.dev/text/template) of the fields:

| | |
|---|---|
| `{{ .key }}` | The value of a field; bool fields are true or false |
| `{{ .Namespace }}`, `{{ .Stack }}` | The namespace, and the stack's id |
| `{{ quote .key }}` | The value as a quoted string, safe in YAML |
| `{{ if .flag }}…{{ else }}…{{ end }}` | Parts for some settings only |
| `{{ b64enc .key }}`, `{{ toJson .key }}` | Base64, JSON |
| `{{ default "x" .key }}`, `lower`, `upper`, `trim` | |
| `{{ .key \| indent 4 }}`, `nindent` | Indented text |

A manifest that has `{{ }}` of its own, such as Prometheus rules, takes
`noTemplate: true`.

## What the cluster keeps

A stack installed in a namespace has a ConfigMap there,
`kubedesk-stack-<id>`, that records its components, the objects it applied
and its settings, and a Secret of the same name with its password fields.
The objects it applied carry the label `kubedesk.cloud/stack`.

Running a stack again in the same namespace updates it, with the settings it
has. Objects a component applied before and no longer has, such as the
Ingress of a domain taken away, are deleted, and so are components turned
off or gone from the stack. **Uninstall** removes its components, last first: Helm releases with
`helm uninstall`, and the objects it applied. Parts that another installed
stack also has stay, such as a shared operator, and so do custom resource
definitions, since deleting one deletes every object of its kind. The
namespace and its volumes go only when you ask.

Installing goes on in the background when its dialog is closed; the Stacks
page shows its progress, and the app says when it ends.
