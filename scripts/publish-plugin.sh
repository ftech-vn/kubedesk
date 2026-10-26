#!/usr/bin/env bash
# Publishes plugins to Kube Desk's plugin registry, on R2: builds each, packs
# it, adds it to the signed index, and uploads the archive, its README and
# icon, then the index.
#
#   scripts/publish-plugin.sh plugin-sdk/examples/cert-manager [more plugin folders or .tgz]
#   DRY_RUN=1 scripts/publish-plugin.sh …     write what would be uploaded, in $OUT
#   scripts/publish-plugin.sh --remove kubedesk-plugin-x[@1.0.0]
#
# The registry's secret key is ~/Library/Application Support/mygo/update-keys/
# kubedesk-plugins.key (go run ./cmd/plugin-registry keygen made it), or
# KUBEDESK_PLUGIN_REGISTRY_KEY.
set -euo pipefail

cd "$(dirname "$0")/.."

BUCKET="${BUCKET:-kube-desk-releases}"
PREFIX="${PREFIX:-plugins}"
export KUBEDESK_PLUGIN_REGISTRY="${KUBEDESK_PLUGIN_REGISTRY:-https://pub-e3b2030c705649ef87d35925ea3331eb.r2.dev/$PREFIX}"
OUT="${OUT:-$(mktemp -d)}"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

registry() { go run ./cmd/plugin-registry "$@"; }

if [[ "${1:-}" == "--remove" ]]; then
  registry remove -out "$OUT" "${2:?which plugin}"
else
  archives=()
  for src in "$@"; do
    if [[ -f "$src" && "$src" == *.tgz ]]; then
      archives+=("$(cd "$(dirname "$src")" && pwd)/$(basename "$src")")
      continue
    fi
    echo "==> Building $src"
    (cd "$src" && bun run build >/dev/null && npm pack --silent --pack-destination "$work" >/dev/null)
  done
  shopt -s nullglob
  archives+=("$work"/*.tgz)
  [[ ${#archives[@]} -gt 0 ]] || { echo "usage: $0 <plugin folder or .tgz>..." >&2; exit 2; }
  echo "==> Adding to the index"
  registry publish -out "$OUT" "${archives[@]}"
fi

if [[ "${DRY_RUN:-0}" == "1" ]]; then
  echo "Dry run: the registry's files are in $OUT"
  exit 0
fi

put() { # put <file> <key> <content type> [cache control]
  echo "  ↑ $PREFIX/$2"
  wrangler r2 object put "$BUCKET/$PREFIX/$2" --file "$1" --content-type "$3" ${4:+--cache-control "$4"} --remote >/dev/null
}

echo "==> Uploading to r2://$BUCKET/$PREFIX"
# Archives are never replaced, so they can be cached; the index goes last,
# so that it never lists a file that is not up yet.
(cd "$OUT" && find . -type f ! -name index.json | sed 's|^\./||') | while read -r f; do
  case "$f" in
    *.tgz) put "$OUT/$f" "$f" application/gzip "public, max-age=31536000, immutable" ;;
    *.md) put "$OUT/$f" "$f" "text/markdown; charset=utf-8" "no-cache" ;;
    *.png) put "$OUT/$f" "$f" image/png "no-cache" ;;
  esac
done
put "$OUT/index.json" index.json application/json "no-cache"
echo "==> Published: $KUBEDESK_PLUGIN_REGISTRY/index.json"
