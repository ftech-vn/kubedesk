# kubedesk.cloud

The Kube Desk website: plain HTML, CSS and JavaScript, with no build step.

| Page | |
|---|---|
| `index.html` | Home: what Kube Desk does, with a working preview of the app (`home.js`) |
| `download.html` | Download, install, connecting AI agents, building from source |
| `changelog.html` | Release notes, from `../CHANGELOG.md` |
| `plugins.html` | The plugin marketplace (`plugins.js`), read live from the plugin registry on R2 |
| `404.html` | Not found |

`styles.css` and `site.js` (top bar, ⌘K palette, copy buttons) are shared by every page.

## Preview

```sh
cd web
python3 -m http.server 4321   # or: bunx serve .
```

Then open http://localhost:4321.

## Deploy

kubedesk.cloud runs on the `azure-k3s` cluster, namespace `kubedesk-web`: nginx in a container
(`Dockerfile`, `nginx.conf`), behind Traefik, with a Let's Encrypt certificate from cert-manager
(`k8s/kubedesk-web.yaml`). DNS is on Cloudflare: `kubedesk.cloud` and `www` point at the cluster
(`172.173.76.191`), DNS only, and `www` redirects to the apex.

Pushing changes under `web/` to `main` builds the image and pushes it to
`ghcr.io/ftech-vn/kubedesk-web:sha-<commit>` and `:latest` (`.github/workflows/web.yml`). Then roll it out:

```sh
kubectl --context azure-k3s -n kubedesk-web set image deployment/kubedesk-web \
  web=ghcr.io/ftech-vn/kubedesk-web:sha-<commit>
```

To build and push by hand (needs a token with `write:packages`):

```sh
docker build --platform linux/amd64 -t ghcr.io/ftech-vn/kubedesk-web:<tag> web
docker push ghcr.io/ftech-vn/kubedesk-web:<tag>
```

First setup only: the namespace needs the `ghcr` pull secret, copied from another namespace that has it:

```sh
kubectl --context azure-k3s -n shipez get secret ghcr -o json \
  | jq '{apiVersion, kind, type, data, metadata: {name: "ghcr", namespace: "kubedesk-web"}}' \
  | kubectl --context azure-k3s apply -f -
kubectl --context azure-k3s apply -f web/k8s/kubedesk-web.yaml
```

## The plugin marketplace

`plugins.html` reads `plugins/index.json` of the `kube-desk-releases` bucket from the browser, so publishing a plugin
(`../scripts/publish-plugin.sh`, see `../plugin-sdk/README.md`) needs no new image. The bucket must let the site read
it across origins, once:

```sh
wrangler r2 bucket cors set kube-desk-releases --file web/r2-cors.json
```

To preview with a local registry, such as the `OUT` of `DRY_RUN=1 ../scripts/publish-plugin.sh …` copied into
`plugins/` here: `http://localhost:4321/plugins.html?registry=/plugins`.

## When releasing a new version

- Update the version and date in `index.html` (hero badge, "At a glance", final download button, JSON-LD),
  `download.html` (download card) and `changelog.html` (add the release on top, move the "Latest" badge).
- The download links point at `Kube-Desk-latest-arm64.dmg`, so they need no change.
- Screenshots in `assets/screens/` are WebP copies of `../docs/screenshots/`, 1600 px wide:
  `cwebp -q 82 -resize 1600 0 ../docs/screenshots/logs.png -o assets/screens/logs.webp`
