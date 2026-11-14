#!/usr/bin/env bash
# One isolated install stack per checkout, for e2e tests and agent-driven
# live checks: `task e2e:serve`. Everything (panel, Traefik, the Composes a test
# deploys) runs inside one docker:dind container named after this checkout, so
# several worktrees can run their sandboxes side by side and none of them
# touches the host's own containers.
#
# Only Traefik's two entrypoints reach the host, on ports Docker picks when the
# container starts, so two sandboxes can never ask for the same port. The panel
# is served through Traefik like on a real host, at panel.localhost; deployed
# Domains use other *.localhost hosts. The dind Docker API is a unix socket
# under $XDG_RUNTIME_DIR, owned by your group, so no port is needed for it.
#
# Never guess a port: read .data/e2e/env (shell) or .data/e2e/env.json, which
# `serve` and `reload` rewrite from `docker port` every time and `down` removes.
#
#   scripts/e2e-sandbox.sh [serve]  start (or reuse) the sandbox, build, compose up, create admin
#   scripts/e2e-sandbox.sh reload   rebuild the panel image from this checkout, keep its data
#   scripts/e2e-sandbox.sh env      print .data/e2e/env
#   scripts/e2e-sandbox.sh ls       list every e2e sandbox on this host and its checkout
#   scripts/e2e-sandbox.sh down     remove this checkout's sandbox and its env file
#
# SEED_DB=<copy of a panel.db> starts a new sandbox from that database instead
# of an empty one, so migrations and the UI run on real data without touching
# the original. Instead of create-admin, the script resets the password (and
# 2FA) of SEED_EMAIL, or of the only user. Nothing the seeded Composes ran
# exists inside dind. A copy of a live database holds real secrets: keep it
# under .data/ and never commit it.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
SLUG=$(basename "$ROOT" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9\n' '-' | cut -c1-40)
NAME=${SANDBOX_NAME:-relist-e2e-$SLUG}
PROJECT=relist-e2e
IMAGE=relist-panel:e2e
OUT=$ROOT/.data/e2e
SOCK_DIR=${XDG_RUNTIME_DIR:-/tmp/relist-e2e-$(id -u)}/relist-e2e/$NAME
EMAIL=admin@e2e.local
SEED_DB=${SEED_DB:-}
SEED_EMAIL=${SEED_EMAIL:-}

say() { printf '\033[1me2e-sandbox:\033[0m %s\n' "$*"; }
fail() { printf '\033[31me2e-sandbox: FAIL\033[0m %s\n' "$*" >&2; exit 1; }

mkdir -p "$OUT/dockercfg" "$OUT/install"
chmod 700 "$OUT"
[ -f "$OUT/dockercfg/config.json" ] || echo '{}' >"$OUT/dockercfg/config.json"
# A broken ~/.docker/config.json must not matter, and nothing here may reach
# another Docker host by accident.
export DOCKER_CONFIG="$OUT/dockercfg"
host() { env -u DOCKER_HOST docker "$@"; }
dind() { DOCKER_HOST="unix://$SOCK_DIR/docker.sock" docker "$@"; }

wait_for() { # wait_for <seconds> <command...>
	local n=$1; shift
	for _ in $(seq 1 "$n"); do "$@" >/dev/null 2>&1 && return 0; sleep 1; done
	return 1
}

host_port() { # host_port <container port>: the host port Docker picked
	host port "$NAME" "$1/tcp" | head -1 | sed 's/.*://'
}

healthy() {
	curl -skf --resolve "panel.localhost:$1:127.0.0.1" "https://panel.localhost:$1/healthz"
}

build() {
	say "building $IMAGE into $NAME"
	dind build -q -t "$IMAGE" "$ROOT" >/dev/null
}

write_env() {
	local http https
	http=$(host_port 80)
	https=$(host_port 443)
	wait_for 90 healthy "$https" || fail "panel not healthy through Traefik (docker logs: DOCKER_HOST=unix://$SOCK_DIR/docker.sock docker logs relist-panel)"
	umask 077
	cat >"$OUT/env" <<EOF
# Written by scripts/e2e-sandbox.sh for $ROOT. Do not edit; rerun task e2e:serve.
export E2E_SANDBOX=$NAME
export PANEL_URL=https://panel.localhost:$https
export E2E_HTTP_PORT=$http
export E2E_HTTPS_PORT=$https
export DOCKER_HOST=unix://$SOCK_DIR/docker.sock
export DOCKER_CONFIG=$OUT/dockercfg
export ADMIN_EMAIL=$(cat "$OUT/admin-email.txt")
export ADMIN_PASSWORD=$(cat "$OUT/admin-pw.txt")
EOF
	cat >"$OUT/env.json" <<EOF
{
  "sandbox": "$NAME",
  "panelUrl": "https://panel.localhost:$https",
  "httpPort": $http,
  "httpsPort": $https,
  "dockerHost": "unix://$SOCK_DIR/docker.sock",
  "dockerConfig": "$OUT/dockercfg",
  "adminEmail": "$(cat "$OUT/admin-email.txt")",
  "adminPassword": "$(cat "$OUT/admin-pw.txt")"
}
EOF
	say "panel    https://panel.localhost:$https  (self-signed; $(cat "$OUT/admin-email.txt"))"
	say "traefik  http 127.0.0.1:$http  https 127.0.0.1:$https  (Host: <name>.localhost)"
	say "docker   source $OUT/env   # sets DOCKER_HOST to this sandbox only"
}

case "${1:-serve}" in
ls)
	host ps -a --filter label=relist.e2e.root \
		--format '{{.Names}}\t{{.Status}}\t{{.Ports}}\t{{.Label "relist.e2e.root"}}'
	exit 0
	;;
env)
	[ -f "$OUT/env" ] || fail "no env file: run task e2e:serve"
	cat "$OUT/env"
	exit 0
	;;
down)
	host rm -f -v "$NAME" >/dev/null 2>&1 || true
	rm -rf "$SOCK_DIR" "$OUT/env" "$OUT/env.json" "$OUT/admin-pw.txt" "$OUT/admin-email.txt"
	say "removed $NAME"
	exit 0
	;;
reload)
	host inspect "$NAME" >/dev/null 2>&1 || fail "no sandbox: run task e2e:serve"
	host start "$NAME" >/dev/null
	wait_for 60 dind info || fail "dind did not come up"
	build
	(cd "$OUT/install" && dind compose -p "$PROJECT" up -d panel >/dev/null 2>&1) || fail "compose up failed"
	write_env
	say "panel reloaded, data kept"
	exit 0
	;;
serve) ;;
*) fail "unknown command $1 (serve, reload, env, ls, down)" ;;
esac

if [ -n "$SEED_DB" ]; then
	[ -s "$SEED_DB" ] || fail "SEED_DB $SEED_DB is missing or empty"
	host inspect "$NAME" >/dev/null 2>&1 && fail "SEED_DB needs a new sandbox: run task e2e:down first"
fi

if ! host inspect "$NAME" >/dev/null 2>&1; then
	say "starting $NAME"
	rm -f "$OUT/admin-pw.txt" "$OUT/admin-email.txt" # a new dind has a new panel.db
	rm -rf "$SOCK_DIR" && mkdir -p "$SOCK_DIR"
	host run -d --privileged --name "$NAME" \
		--label "relist.e2e.root=$ROOT" \
		-v "$SOCK_DIR:/e2e-sock" \
		-p 127.0.0.1::80 -p 127.0.0.1::443 \
		docker:29-dind \
		--host=unix:///var/run/docker.sock --host=unix:///e2e-sock/docker.sock --group="$(id -g)" >/dev/null
else
	host start "$NAME" >/dev/null
fi
wait_for 60 dind info || fail "dind did not come up on $SOCK_DIR/docker.sock"

build
cp "$ROOT/docker-compose.yml" "$OUT/install/docker-compose.yml"
printf 'RELIST_PANEL_IMAGE=%s\nRELIST_DOMAIN=panel.localhost\n' "$IMAGE" >"$OUT/install/.env"
dind network inspect relist-network >/dev/null 2>&1 || dind network create relist-network >/dev/null
if [ -n "$SEED_DB" ]; then
	# Create the containers and the data volume, put the copy in place, then
	# start: the panel's first start runs the migrations on the copy.
	(cd "$OUT/install" && dind compose -p "$PROJECT" create >/dev/null 2>&1) || fail "compose create failed"
	dind cp "$SEED_DB" relist-panel:/var/lib/relist-panel/panel.db >/dev/null || fail "could not copy SEED_DB"
	say "seeded panel.db from $SEED_DB"
fi
(cd "$OUT/install" && dind compose -p "$PROJECT" up -d >/dev/null 2>&1) || fail "compose up failed"
wait_for 90 dind exec relist-panel wget -qO- http://127.0.0.1:8080/healthz \
	|| fail "panel not healthy (DOCKER_HOST=unix://$SOCK_DIR/docker.sock docker logs relist-panel)"

if [ ! -s "$OUT/admin-pw.txt" ]; then
	head -c 32 /dev/urandom | base64 | tr -dc A-Za-z0-9 | head -c 24 >"$OUT/admin-pw.txt"
	if [ -n "$SEED_DB" ]; then
		args=(); [ -n "$SEED_EMAIL" ] && args=(--email "$SEED_EMAIL")
		out=$(dind exec -i relist-panel relist-panel reset-password "${args[@]}" --password-stdin <"$OUT/admin-pw.txt") \
			|| fail "reset-password failed (several users? set SEED_EMAIL)"
		sed -n 's/^Reset password for \(.*\); revoked.*/\1/p' <<<"$out" >"$OUT/admin-email.txt"
		dind exec relist-panel relist-panel reset-2fa --email "$(cat "$OUT/admin-email.txt")" >/dev/null 2>&1 || true
	else
		dind exec -i relist-panel relist-panel create-admin --email "$EMAIL" --password-stdin <"$OUT/admin-pw.txt" >/dev/null \
			|| fail "create-admin failed"
		echo "$EMAIL" >"$OUT/admin-email.txt"
	fi
fi

write_env
