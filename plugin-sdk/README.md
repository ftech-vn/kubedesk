# Writing Kube Desk plugins

A plugin adds to Kube Desk's pages: a page in the sidebar, a tab or a section in an object's details, an item in
its menu, a column in a list, a command in ⌘K, a button in the title bar, a card on the cluster's overview, and
its own settings. It is a React component bundle that Kube Desk loads at runtime, from Kube Desk's plugin
registry, an archive, or a folder you are working in.

```sh
scripts/new-plugin.sh ../my-plugin       # from plugin-sdk/template
cd ../my-plugin
bun install
bun run dev                               # rebuilds dist/main.js as you edit
```

In Kube Desk, open **Plugins** (⌘K → Plugins…, or Kube Desk → Plugins…), choose **Load from folder…** and pick
the folder. Kube Desk reloads the plugin each time `dist/main.js` changes, without reloading the app.

## A plugin

```
my-plugin/
  package.json       name, version, engines.kubedesk, kubedesk.main
  src/index.tsx      the source
  dist/main.js       the build Kube Desk runs
  kubedesk.d.ts      the API's types
```

```json
{
  "name": "kubedesk-plugin-my-plugin",
  "version": "0.1.0",
  "keywords": ["kubedesk-plugin"],
  "engines": { "kubedesk": ">=0.8.0" },
  "kubedesk": { "displayName": "My plugin", "main": "dist/main.js" }
}
```

- `name` is the plugin's ID. Use the `kubedesk-plugin-` prefix, or your scope (`@acme/kubedesk-ops`).
- `engines.kubedesk` is the range of Kube Desk versions the plugin works with, as npm writes ranges. Kube Desk
  refuses to install a plugin outside it, and does not run one that an update leaves outside it.
- `kubedesk.main` is the build, `dist/main.js` when left out; `displayName` is the name Kube Desk shows.

`dist/main.js` is a CommonJS module that requires `react`, `react/jsx-runtime` and `kubedesk`, which Kube Desk
provides; everything else is bundled into it. The template's `bun run build` makes it so:

```sh
bun build src/index.tsx --outfile dist/main.js --format cjs --target browser \
  --external react --external 'react/*' --external react-dom --external kubedesk --production
```

Kube Desk calls its `activate` export, or the default export when it is a function, after running the module.
What `activate` returns, if a function, runs when the plugin is turned off or reloaded, as does a `deactivate`
export. Registering at the top of the module works too.

```tsx
import { app, k8s, registerObjectAction, registerPage, ui } from "kubedesk";

const pods = { group: "", version: "v1", resource: "pods" };

function Failing({ cluster, namespace }: { cluster: string; namespace: string }) {
  const { items } = k8s.useList(cluster, pods, namespace);
  const bad = items.filter((p) => k8s.podStatus(p).tone === "bad");
  return (
    <ui.Page title="Failing pods">
      <ui.Table items={bad} columns={[{ id: "name", title: "Name", value: (p) => p.metadata.name }]} />
    </ui.Page>
  );
}

export function activate() {
  registerPage({ id: "failing", title: "Failing pods", component: Failing });
  registerObjectAction({ id: "hello", label: "Say hello", kinds: ["Pod"], run: ({ object }) => app.toast.ok(`Hello, ${object.metadata.name}`) });
}
```

## The API

`kubedesk.d.ts` is the reference; this is the map.

| Function | Adds | Headlamp's counterpart |
|---|---|---|
| `registerPage({ id, title, icon, component })` | A page, under Plugins in every cluster's sidebar | `registerRoute` + `registerSidebarEntry` |
| `registerDetailsTab({ id, label, kinds, when, component })` | A tab in the details of objects | `registerDetailsViewSection` |
| `registerDetailsSection({ id, title, kinds, when, component })` | A card under an object's Overview | `registerDetailsViewSection` |
| `registerObjectAction({ id, label, icon, kinds, when, run })` | An item in objects' menus, and the details header | `registerDetailsViewHeaderAction` |
| `registerColumn({ id, title, kinds, value, render })` | A column in the lists of some kinds, custom ones too | `registerResourceTableColumnsProcessor` |
| `registerCommand({ id, label, keywords, icon, when, run })` | A command in ⌘K | |
| `registerTitleBarAction({ id, component })` | A component in the title bar | `registerAppBarAction` |
| `registerOverviewCard({ id, title, wide, component })` | A card on the cluster overview | `registerOverviewChartsProcessor` |
| `registerSettings(component)` | The plugin's settings, in Plugins | `registerPluginSettings` |
| `onEvent(listener)` | Told when the cluster, namespace, page or open object changes | `registerHeadlampEventCallback` |

Each returns a function that removes what it added; turning the plugin off removes everything.

- **`k8s`**: `useList` watches a resource as the app's lists do; `list`, `get`, `resources`; `request` reads any
  API path with GET; `apply` and `remove` change the cluster after showing the user what changes and asking, as
  the app does, with the production cluster's name to type and read-only clusters refusing; `can` asks RBAC;
  helpers such as `podStatus` and `formatBytes`.
- **`app`**: where the user is (`useContext`, `useCluster`), `openObject`, `openPage`, `toast`, `confirm`,
  `openURL`, `copy`.
- **`settings`**: `get`, `useSettings` and `save`, kept by Kube Desk.
- **`ui`**: the app's components, which follow its light and dark themes: `Page`, `Table`, `Card`, `KV`, `Badge`,
  `Button`, `Tabs`, `Modal`, `Field`, `Markdown`, `Empty`, `ErrorBox`, `Spinner`…

`apiVersion` is 1. It changes only when plugins written for it would break; what Kube Desk adds meanwhile is
added to version 1.

## Sharing it

- **The marketplace**: plugins in Kube Desk's registry show in **Find plugins** and on
  [kubedesk.cloud/plugins.html](https://kubedesk.cloud/plugins.html), whose **Install in Kube Desk** opens the app.
  [Open an issue](https://github.com/ftech-vn/kubedesk/issues/new?title=Plugin%3A%20&labels=plugin) with a link to
  the plugin's source; once it is reviewed, the maintainers publish it. Give it a `README.md`, a square
  `icon.png` as `kubedesk.icon`, and a few `kubedesk.features`: the marketplace shows them.
- **An archive**: `bun run package` makes `<name>-<version>.tgz` with `npm pack`. People install it with
  **Install from file…**, or **From address…** with an HTTPS address and, optionally, its SHA-256: for plugins
  of your own team.

Plugins go to `plugins/` in Kube Desk's data folder (on macOS, `~/Library/Application Support/Kube Desk/plugins`).
A folder loaded in development wins over an installed plugin with the same name.

## The registry

Kube Desk's registry is a folder of static files on R2 (`kube-desk-releases/plugins/`):

```
index.json                        the index, signed: {"schema": 1, "signed": "<the index>", "signature": "…"}
<id>/<id>-<version>.tgz           each version, as npm pack makes it; never replaced
<id>/README.md, <id>/icon.png     of the newest version, for the marketplace
```

The index lists each plugin and its versions, with the Kube Desk versions each works with, its archive, size and
SHA-256. Kube Desk installs the newest version that works with it, and checks the signature with the registry's
public key, which it is built with (`plugin_registry.go`).

Maintainers publish with the registry's secret key, made once with `go run ./cmd/plugin-registry keygen`:

```sh
scripts/publish-plugin.sh plugin-sdk/examples/cert-manager    # build, pack, add, sign, upload
DRY_RUN=1 OUT=/tmp/registry scripts/publish-plugin.sh …       # write the files without uploading
scripts/publish-plugin.sh --remove kubedesk-plugin-x@1.0.0     # take a version out of the index
go run ./cmd/plugin-registry list                               # what the registry serves
```

A version is published once: publishing it again with another archive is refused. To test the app against
another registry, set `KUBEDESK_PLUGIN_REGISTRY` (and `KUBEDESK_PLUGIN_REGISTRY_PUBLIC_KEY` if another key signs
it); the marketplace page takes `?registry=<url>`.

## Trust

A plugin runs inside Kube Desk, in the page, with the same access as the app. Kube Desk does not sandbox it, as
Headlamp does not. So:

- a plugin that is installed is off until the user turns it on, after Kube Desk says what that means;
- the API changes clusters only through Kube Desk's confirmations, so a plugin written against it cannot change a
  production cluster without the user typing its name, nor a read-only one at all, and gives no access to the
  assistant's key or the MCP token;
- the registry signs its index with a key of its own, and Kube Desk installs only what a signed index lists,
  checking each archive's SHA-256 and size; archives cannot write outside their folder.

None of this stops a plugin that means harm, which can reach past the API: install plugins from people you trust.

## Examples

- [`examples/cert-manager`](examples/cert-manager): a page of certificates, an overview card of the ones that
  expire soon, a column, a details section, an action and settings.
- [`examples/kubectl-helper`](examples/kubectl-helper): "Copy kubectl command" for any object, a ⌘K command, a
  Resources tab for pods, settings.

Build one with `bun run build` in its folder, and load the folder in Kube Desk.
