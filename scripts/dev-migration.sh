#!/usr/bin/env bash
# Try a server migration with the frontend live-reloading, as
# `task dev:migration`. It starts the migration sandbox when there is none
# (FRESH=1 starts a new one), then runs one Vite dev server per panel, each
# proxying /api to its sandbox panel:
#
#   old panel  http://localhost:5181   admin@example.com / correct horse battery staple
#   new panel  http://127.0.0.1:5182   seed@example.com  / seeded password 123
#
# Edits under web/ show up at once. A backend change needs
# `task sandbox:migration:reload`, and `task sandbox:migration:reset` starts
# the migration over. Ctrl-C stops the dev servers and leaves the
# sandbox running; `task sandbox:migration:down` removes it.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
OLD_PORT=${OLD_PORT:-18881}
NEW_PORT=${NEW_PORT:-18882}

if [ "${FRESH:-}" = 1 ] || ! docker inspect sandbox-old sandbox-new >/dev/null 2>&1; then
	OLD_PORT=$OLD_PORT NEW_PORT=$NEW_PORT "$ROOT/scripts/sandbox-migration.sh"
fi
command -v pnpm >/dev/null || { echo "dev: pnpm is not on PATH" >&2; exit 1; }

pids=()
trap 'kill "${pids[@]}" 2>/dev/null || true' EXIT INT TERM
# The two use different hostnames so their session cookies stay apart.
RELIST_PANEL_URL="http://127.0.0.1:$OLD_PORT" pnpm --dir "$ROOT/web" exec vite --host 127.0.0.1 --port 5181 --strictPort --clearScreen false &
pids+=($!)
RELIST_PANEL_URL="http://127.0.0.1:$NEW_PORT" pnpm --dir "$ROOT/web" exec vite --host 127.0.0.1 --port 5182 --strictPort --clearScreen false &
pids+=($!)

cat <<EOF

dev: old panel  http://localhost:5181   admin@example.com / correct horse battery staple
dev: new panel  http://127.0.0.1:5182   seed@example.com / seeded password 123
dev: on the new panel connect to http://sandbox-old:8888 and select /srv/shop
dev: Ctrl-C stops the dev servers; the sandbox keeps running

EOF
wait
