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
# Domains use other *.localhost hosts. The dind Docker API is TLS-only
# (--tlsverify) on a third loopback port Docker picks, 127.0.0.1 only. dind makes
# its own certs; `serve` and `reload` copy the client ones to .data/e2e/certs
# (anyone who can read them controls this sandbox's dind). A TCP port, not a
# bind-mounted unix socket, so this also works on Docker Desktop.
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
# A proxy in the user's shell must not catch the loopback Docker API or curl.
export NO_PROXY="${NO_PROXY:+$NO_PROXY,}127.0.0.1,localhost"
host() { env -u DOCKER_HOST -u DOCKER_TLS_VERIFY -u DOCKER_CERT_PATH docker "$@"; }
dind() { DOCKER_HOST="tcp://127.0.0.1:$(host_port 2376)" DOCKER_TLS_VERIFY=1 DOCKER_CERT_PATH="$OUT/certs" docker "$@"; }

wait_for() { # wait_for <seconds> <command...>
	local n=$1; shift
	for _ in $(seq 1 "$n"); do "$@" >/dev/null 2>&1 && return 0; sleep 1; done
	return 1
}

host_port() { # host_port <container port>: the host port Docker picked
	host port "$NAME" "$1/tcp" | head -1 | sed 's/.*://'
}

require_tls_port() { # an older sandbox has no 2376 binding: stop before touching it
	host inspect -f '{{json .HostConfig.PortBindings}}' "$NAME" 2>/dev/null | grep -q '"2376/tcp"' \
		|| fail "this sandbox was made by an older e2e-sandbox.sh (no TLS port). Run: task e2e:down, then task e2e:serve"
}

copy_certs() { # dind makes new public certs on every start: copy them out again
	local f ok
	rm -rf "$OUT/certs"
	for _ in $(seq 1 60); do
		ok=1
		host cp "$NAME:/certs/client/." "$OUT/certs" >/dev/null 2>&1 || ok=0
		for f in ca cert key; do [ -s "$OUT/certs/$f.pem" ] || ok=0; done
		[ "$ok" = 1 ] && break
		sleep 1
	done
	[ "$ok" = 1 ] || fail "dind client certs did not appear (docker logs $NAME)"
	chmod 700 "$OUT/certs"
	chmod 644 "$OUT/certs/ca.pem" "$OUT/certs/cert.pem"
	chmod 600 "$OUT/certs/key.pem"
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
	wait_for 90 healthy "$https" || fail "panel not healthy through Traefik (docker logs: source $OUT/env && docker logs relist-panel)"
	umask 077
	cat >"$OUT/env" <<EOF
# Written by scripts/e2e-sandbox.sh for $ROOT. Do not edit; rerun task e2e:serve.
export E2E_SANDBOX=$NAME
export PANEL_URL=https://panel.localhost:$https
export E2E_HTTP_PORT=$http
export E2E_HTTPS_PORT=$https
export DOCKER_HOST=tcp://127.0.0.1:$(host_port 2376)
export DOCKER_TLS_VERIFY=1
export DOCKER_CERT_PATH=$OUT/certs
export DOCKER_CONFIG=$OUT/dockercfg
export NO_PROXY="\${NO_PROXY:+\$NO_PROXY,}127.0.0.1,localhost"
export ADMIN_EMAIL=$(cat "$OUT/admin-email.txt")
export ADMIN_PASSWORD=$(cat "$OUT/admin-pw.txt")
EOF
	cat >"$OUT/env.json" <<EOF
{
  "sandbox": "$NAME",
  "panelUrl": "https://panel.localhost:$https",
  "httpPort": $http,
  "httpsPort": $https,
  "dockerHost": "tcp://127.0.0.1:$(host_port 2376)",
  "dockerTlsVerify": "1",
  "dockerCertPath": "$OUT/certs",
  "dockerConfig": "$OUT/dockercfg",
  "adminEmail": "$(cat "$OUT/admin-email.txt")",
  "adminPassword": "$(cat "$OUT/admin-pw.txt")"
}
EOF
	say "panel    https://panel.localhost:$https  (self-signed; $(cat "$OUT/admin-email.txt"))"
	say "traefik  http 127.0.0.1:$http  https 127.0.0.1:$https  (Host: <name>.localhost)"
	say "docker   source $OUT/env   # sets DOCKER_HOST, TLS and NO_PROXY for this sandbox only"
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
	rm -rf "$OUT/certs" "$OUT/env" "$OUT/env.json" "$OUT/admin-pw.txt" "$OUT/admin-email.txt"
	say "removed $NAME"
	exit 0
	;;
reload)
	host info >/dev/null 2>&1 || fail "cannot reach the host's Docker (is it running? a sourced DOCKER_* env?)"
	host inspect "$NAME" >/dev/null 2>&1 || fail "no sandbox: run task e2e:serve"
	require_tls_port
	host start "$NAME" >/dev/null
	copy_certs
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

host info >/dev/null 2>&1 || fail "cannot reach the host's Docker (is it running? a sourced DOCKER_* env?)"
if [ -n "$SEED_DB" ]; then
	[ -s "$SEED_DB" ] || fail "SEED_DB $SEED_DB is missing or empty"
	host inspect "$NAME" >/dev/null 2>&1 && fail "SEED_DB needs a new sandbox: run task e2e:down first"
fi

if ! host inspect "$NAME" >/dev/null 2>&1; then
	say "starting $NAME"
	rm -f "$OUT/admin-pw.txt" "$OUT/admin-email.txt" # a new dind has a new panel.db
	host run -d --privileged --name "$NAME" \
		--label "relist.e2e.root=$ROOT" \
		-p 127.0.0.1::80 -p 127.0.0.1::443 -p 127.0.0.1::2376 \
		docker:29-dind >/dev/null
else
	require_tls_port
	host start "$NAME" >/dev/null
fi
copy_certs
wait_for 60 dind info || fail "dind did not come up (docker logs $NAME)"

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
	|| fail "panel not healthy (source $OUT/env && docker logs relist-panel)"

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
