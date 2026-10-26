#!/usr/bin/env bash
# Builds a signed release of Kube Desk and publishes it to the
# kube-desk-releases bucket of Cloudflare R2, where installed apps look for
# updates (updates.url in mygo.config.ts).
#
#   scripts/release.sh              build and upload darwin/arm64
#   PLATFORM=darwin/universal scripts/release.sh
#   PLATFORM=darwin/arm64,linux/amd64,linux/arm64,windows/amd64 scripts/release.sh
#   UPLOAD=0 scripts/release.sh     build only
#   SKIP_BUILD=1 scripts/release.sh upload what an earlier run built
#   OUT=release scripts/release.sh  build elsewhere than build/, e.g. while
#                                   the app runs from there
#
# Bump `version` in mygo.config.ts and add its section to CHANGELOG.md first.
set -euo pipefail

cd "$(dirname "$0")/.."

BUCKET="${BUCKET:-kube-desk-releases}"
PLATFORM="${PLATFORM:-darwin/arm64}"
UPLOAD="${UPLOAD:-1}"
OUT="${OUT:-build}"
KEY_FILE="${KEY_FILE:-$HOME/Library/Application Support/mygo/update-keys/mygo-update.key}"

if [[ -z "${MYGO_UPDATER_PRIVATE_KEY:-}" ]]; then
  if [[ ! -f "$KEY_FILE" ]]; then
    echo "No update signing key: set MYGO_UPDATER_PRIVATE_KEY or KEY_FILE" >&2
    exit 1
  fi
  MYGO_UPDATER_PRIVATE_KEY="$(cat "$KEY_FILE")"
fi
export MYGO_UPDATER_PRIVATE_KEY

version="$(sed -nE 's/^ *version: *"([^"]+)".*/\1/p' mygo.config.ts | head -1)"
if ! grep -qE "^## \[?${version//./\\.}\]?" CHANGELOG.md; then
  echo "CHANGELOG.md has no section for $version" >&2
  exit 1
fi

IFS=, read -ra platforms <<<"$PLATFORM"
if [[ "${SKIP_BUILD:-0}" != "1" ]]; then
  echo "==> Testing"
  go vet ./...
  go test ./...
  bunx tsc --noEmit

  echo "==> Building Kube Desk $version for $PLATFORM"
  bun run build -- -platform "$PLATFORM" -o "$OUT"
else
  for platform in "${platforms[@]}"; do
    out="$OUT/${platform/\//-}"
    if ! grep -q "\"version\": \"$version\"" "$out"/update-*.json 2>/dev/null; then
      echo "$out holds no build of $version: run without SKIP_BUILD first" >&2
      exit 1
    fi
  done
fi

if [[ "$UPLOAD" != "1" ]]; then
  echo "Built in $OUT (not uploaded)"
  exit 0
fi

put() { # put <file> <key> [extra wrangler flags...]
  local file="$1" key="$2"
  shift 2
  echo "  ↑ $key"
  wrangler r2 object put "$BUCKET/$key" --file "$file" --remote "$@" >/dev/null
}

base="$(sed -nE 's/^ *url: *"([^"]+)".*/\1/p' mygo.config.ts | head -1)"
echo "==> Uploading to r2://$BUCKET"
shopt -s nullglob
for platform in "${platforms[@]}"; do
  out="$OUT/${platform/\//-}"
  os="${platform%/*}"
  arch="${platform#*/}"
  # Archives, deltas and installers first; apps only see a version once its
  # manifest is up, so the manifests go last. Each installer also gets a
  # stable link to the newest version, for the website.
  for f in "$out"/*.tar.gz "$out"/*.delta "$out"/*.zip; do
    put "$f" "$(basename "$f")"
  done
  for f in "$out"/*.dmg; do
    put "$f" "$(basename "$f")" --content-type application/x-apple-diskimage
    put "$f" "Kube-Desk-latest-$arch.dmg" --content-type application/x-apple-diskimage --cache-control no-cache
  done
  for f in "$out"/*.deb; do
    put "$f" "$(basename "$f")" --content-type application/vnd.debian.binary-package
    put "$f" "Kube-Desk-latest-$os-$arch.deb" --content-type application/vnd.debian.binary-package --cache-control no-cache
  done
  for f in "$out"/*Setup*.exe; do
    put "$f" "$(basename "$f")" --content-type application/vnd.microsoft.portable-executable
    put "$f" "Kube-Desk-Setup-latest-$arch.exe" --content-type application/vnd.microsoft.portable-executable --cache-control no-cache
  done
  # curl -fsSL $base/install.sh | sh installs the newest version on Linux.
  if [[ -f "$out/install.sh" ]]; then
    put "$out/install.sh" install.sh --content-type text/x-shellscript --cache-control no-cache
  fi
  manifests=("$out"/update-*.json)
  if [[ ${#manifests[@]} -eq 0 ]]; then
    echo "The build wrote no update manifest for $platform" >&2
    exit 1
  fi
  for f in "${manifests[@]}"; do
    put "$f" "$(basename "$f")" --content-type application/json --cache-control no-cache
    echo "    manifest: $base/$(basename "$f")"
  done
done

echo "==> Published Kube Desk $version"
