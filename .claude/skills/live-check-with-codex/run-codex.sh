#!/usr/bin/env bash
# Run Codex CLI headless on a prompt file, from the repo root.
#   bash .claude/skills/live-check-with-codex/run-codex.sh <prompt.md> <out dir>
# Writes <out dir>/codex-result.md (final answer) and <out dir>/codex-run.log.
# Defaults: gpt-6.1-terra, xhigh effort, priority ("fast") tier; override with
# CODEX_MODEL, CODEX_EFFORT, CODEX_TIER (empty CODEX_TIER = default tier).
set -euo pipefail
PROMPT=${1:?prompt file}
OUT=${2:?out dir}
ROOT=$(cd "$(dirname "$0")/../../.." && pwd)
mkdir -p "$OUT/shots"
args=(exec -m "${CODEX_MODEL:-gpt-6.1-terra}"
	-c "model_reasoning_effort=\"${CODEX_EFFORT:-xhigh}\""
	-s workspace-write -c sandbox_workspace_write.network_access=true
	--add-dir "$OUT" -C "$ROOT" -o "$OUT/codex-result.md")
# The sandbox's Docker socket lives outside the repo; Codex's sandbox must be
# allowed to reach it.
if [ -f "$ROOT/.data/e2e/env" ]; then
	sock=$(sed -n 's|^export DOCKER_HOST=unix://||p' "$ROOT/.data/e2e/env")
	[ -n "$sock" ] && args+=(--add-dir "$(dirname "$sock")")
fi
tier=${CODEX_TIER-priority}
[ -n "$tier" ] && args+=(-c "service_tier=\"$tier\"")
codex "${args[@]}" - <"$PROMPT" >"$OUT/codex-run.log" 2>&1
