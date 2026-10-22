import { defineConfig } from "mygo-cli";

export default defineConfig({
  name: "Kube Desk",
  identifier: "dev.hoangnm.kubedesk",
  version: "0.4.0",
  // `mygo dev` runs devCommand and loads devUrl; `mygo build` runs
  // buildCommand and embeds frontendDist into the app.
  devUrl: "http://localhost:5188",
  devCommand: "bun run dev:web",
  buildCommand: "bun run build:web",
  frontendDist: "dist",
  bindings: "src/mygo.ts",
  out: "build",
  // Signed updates, published to the kube-desk-releases bucket of
  // Cloudflare R2 by scripts/release.sh. The secret key stays out of the
  // repository: release.sh reads it from MYGO_UPDATER_PRIVATE_KEY or
  // ~/Library/Application Support/mygo/update-keys/mygo-update.key.
  updates: {
    publicKey: "ICSza+qIl97FPUzLv4w+RDBpnwbylYRJIghPQ+9Wico=",
    url: "https://pub-e3b2030c705649ef87d35925ea3331eb.r2.dev",
  },
});
