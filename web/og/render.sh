#!/usr/bin/env bash
# Renders og.html into the Open Graph images of each page: assets/og.jpg for
# the home page, assets/og-<page>.jpg for the others. Needs Google Chrome and
# ImageMagick.
set -euo pipefail

cd "$(dirname "$0")"
CHROME="${CHROME:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

for page in home download plugins changelog; do
  "$CHROME" --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=1 \
    --window-size=1200,630 --virtual-time-budget=6000 \
    --screenshot="$tmp/$page.png" "file://$PWD/og.html#$page" 2>/dev/null
  out="../assets/og-$page.jpg"
  [[ "$page" == home ]] && out="../assets/og.jpg"
  magick "$tmp/$page.png" -strip -quality 86 "$out"
  echo "  $out"
done
