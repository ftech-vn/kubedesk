# Contributing to Kube Desk

Thanks for helping! Bug reports, ideas and pull requests are all welcome.

## Getting started

You need [Go](https://go.dev/dl/) 1.27+ and [Bun](https://bun.sh).

```sh
bun install
bun run dev
```

`bun run dev` starts Vite on port 5188 and a development build of the app,
`Kube Desk Dev`. Editing a `.go` file rebuilds and restarts the app, and
editing the frontend hot-reloads the page. After you change the exported
methods or types of a bound Go service, `src/mygo.ts` is regenerated
(`bun run generate` does it by hand).

To try the app without touching real clusters, start a throwaway
[k3s](https://k3s.io) cluster in Docker and point the app at it:

```sh
docker run -d --name kd-dev --privileged -p 127.0.0.1:16443:6443 rancher/k3s server --tls-san 127.0.0.1
docker exec kd-dev cat /etc/rancher/k3s/k3s.yaml | sed 's/127.0.0.1:6443/127.0.0.1:16443/' > /tmp/kd-dev.yaml
KUBECONFIG=/tmp/kd-dev.yaml bun run dev
```

## Before you open a pull request

```sh
go vet ./...
go test ./...
bunx tsc --noEmit
```

- Keep the build free of cgo (`CGO_ENABLED=0`): it is what lets one machine
  build for every platform.
- Never write to the user's kubeconfig files. Clusters added in the app
  belong in its data folder.
- Destructive actions (delete, drain, force) always ask for confirmation.
- Match the style of the surrounding code: Go doc comments on exported
  identifiers, since they become the TSDoc of the generated client.
- Screenshots of UI changes help reviews a lot.

## Project layout

| Path | |
|---|---|
| `main.go` | App setup: services, window |
| `clusters.go` | Kubeconfig sources, stored clusters, preferences, connections |
| `kube.go` | Resource operations over the dynamic client |
| `streams.go` | Logs, exec, port forwarding |
| `updates.go` | Auto-updates, application menu |
| `src/lib/resources.tsx` | The resource kinds the sidebar lists, with their table columns. Add a kind here |
| `src/lib/watch.ts` | The shared watch cache behind every live list |
| `src/views/details/` | Detail views of objects |

Adding a resource kind usually means adding one entry to
`src/lib/resources.tsx`, and optionally a detail view in
`src/views/details/Overview.tsx`.

## Releasing (maintainers)

Releases are signed and published to the `kube-desk-releases` bucket of
Cloudflare R2, which installed apps check for updates
(`updates.url` in `mygo.config.ts`).

1. Bump `version` in `mygo.config.ts`.
2. Add a `## <version>` section to `CHANGELOG.md`. It becomes the release
   notes shown in the update window.
3. Run `scripts/release.sh`. It runs the tests, builds and signs, then uploads
   the archive, deltas and disk image, and uploads the manifest last. It needs
   a logged-in `wrangler`. `PLATFORM=darwin/universal` also targets Intel
   Macs; `UPLOAD=0` only builds.

The update signing key lives outside the repository, in
`~/Library/Application Support/mygo/update-keys/mygo-update.key`, or in
`MYGO_UPDATER_PRIVATE_KEY`. Keep a backup: installed apps only accept updates
signed with it.
