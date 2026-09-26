#!/usr/bin/env bash
# Prototype screenshot helper (local verification only, not part of the deliverable).
# Renders the single-file build through headless Chrome for each route.
#
# Every route that reaches the panel's shell belongs here — `monitoring` was
# missing from this list until the gallery was built, which meant the one screen
# nothing looked at was also the one screen nothing rendered.
#
# The old screens gallery was removed for the still board (#/design/board), which
# is a pannable canvas of live frames — no single viewport-sized screenshot can
# show it, and it is not a route a static per-route shooter means to cover.
#
# THEME=dark bash scripts/screenshot.sh /tmp/rp-shots-dark
set -euo pipefail

APP_DIR="$(cd "$(dirname "$0")/.." && pwd)"
HTML="$APP_DIR/dist-single/index.html"
OUT="${1:-/tmp/rp-shots}"
WIDTH="${WIDTH:-1600}"
HEIGHT="${HEIGHT:-1000}"
QUERY=""
[[ "${THEME:-light}" == "dark" ]] && QUERY="?theme=dark"

mkdir -p "$OUT"

routes=(
  "overview:/"
  "monitoring:/monitoring"
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
  "compose-volumes:/projects/1/env/1/compose/1/volumes"
  "compose-volumes-unresolved:/projects/1/env/1/compose/3/volumes"
  "volume-explorer:/projects/1/env/1/compose/1/volumes/1%3A%2Fapp%2Fuploads"
  "volume-explorer-subdir:/projects/1/env/1/compose/1/volumes/1%3A%2Fapp%2Fuploads/invoices/2026-09"
)

for spec in "${routes[@]}"; do
  name="${spec%%:*}"
  path="${spec#*:}"
  google-chrome \
    --headless=new --no-sandbox --disable-gpu --disable-dev-shm-usage --hide-scrollbars \
    --window-size="${WIDTH},${HEIGHT}" \
    --virtual-time-budget=6000 \
    --screenshot="$OUT/$name.png" \
    "file://$HTML$QUERY#$path" >/dev/null 2>&1
  echo "$name -> $(du -h "$OUT/$name.png" | cut -f1)"
done
