# A Kube Desk plugin

```sh
bun install
bun run dev        # rebuilds dist/main.js as you change src/
```

In Kube Desk, open **Plugins** (⌘K → Plugins…), choose **Load from folder…** and pick this folder. Kube Desk
reloads the plugin each time `dist/main.js` changes.

To share it, [send it](https://github.com/ftech-vn/kubedesk/issues/new?title=Plugin%3A%20&labels=plugin) for
Kube Desk's marketplace, where it shows in **Find plugins** once reviewed. Or hand out the archive of
`bun run package`, which people install with **Install from file…**.
