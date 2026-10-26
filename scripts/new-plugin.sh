#!/usr/bin/env bash
# Starts a Kube Desk plugin from plugin-sdk/template.
#
#   scripts/new-plugin.sh ../my-plugin [kubedesk-plugin-my-plugin]
set -euo pipefail

dest="${1:?usage: scripts/new-plugin.sh <folder> [package name]}"
sdk="$(cd "$(dirname "$0")/../plugin-sdk" && pwd)"
name="${2:-kubedesk-plugin-$(basename "$dest")}"

if [[ -e "$dest" ]]; then
  echo "$dest already exists" >&2
  exit 1
fi
mkdir -p "$dest"
cp -R "$sdk/template/." "$dest/"
cp "$sdk/kubedesk.d.ts" "$dest/"
sed -i.bak -e "s/\"kubedesk-plugin-hello\"/\"$name\"/" "$dest/package.json" && rm "$dest/package.json.bak"

echo "Created $name in $dest"
echo "  cd $dest && bun install && bun run dev"
echo "Then in Kube Desk: ⌘K → Plugins… → Load from folder…, and pick $dest"
