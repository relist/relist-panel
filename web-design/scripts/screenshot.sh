#!/usr/bin/env bash
# Prototype screenshot helper (local verification only, not part of the deliverable).
# Renders the single-file build through headless Chrome for each route.
set -euo pipefail

APP_DIR="$(cd "$(dirname "$0")/.." && pwd)"
HTML="$APP_DIR/dist-single/index.html"
OUT="${1:-/tmp/rp-shots}"
WIDTH="${WIDTH:-1600}"
HEIGHT="${HEIGHT:-1000}"

mkdir -p "$OUT"

routes=(
  "overview:/"
  "projects:/projects"
  "project:/projects/1"
  "variables:/projects/1/env/1/variables"
  "settings:/projects/1/env/1/settings"
  "compose:/projects/1/env/1/compose/1"
  "compose-partial:/projects/1/env/1/compose/2"
  "compose-new:/projects/1/env/1/compose/3"
  "guidelines:/design/guidelines"
  "states:/design/states"
  "signin:/signin"
  "setup:/setup"
  "notfound:/nope"
)

for spec in "${routes[@]}"; do
  name="${spec%%:*}"
  path="${spec#*:}"
  google-chrome \
    --headless=new --no-sandbox --disable-gpu --disable-dev-shm-usage --hide-scrollbars \
    --window-size="${WIDTH},${HEIGHT}" \
    --virtual-time-budget=6000 \
    --screenshot="$OUT/$name.png" \
    "file://$HTML#$path" >/dev/null 2>&1
  echo "$name -> $(du -h "$OUT/$name.png" | cut -f1)"
done
