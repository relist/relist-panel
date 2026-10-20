#!/usr/bin/env bash
# Probe a whole server migration against real Docker (ADR-0018), as
# `task probe:migration`. Two docker:dind hosts each run the install stack from
# this checkout; the old one gets three Composes, then the new one migrates it
# and the probe compares what arrived. It needs a Docker that can run
# privileged containers, internet access for postgres/traefik/busybox, and
# about 12 GB of free disk. It cleans up on exit.
#
# Traefik's ACME CA points at an unreachable address, so the probe never
# registers with Let's Encrypt. The old host's acme.json is seeded with a
# marker instead, and the new host must end up with the same file.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
NET=probe-mig
OLD=mig-old
NEW=mig-new
JAR=probe-mig-cookies
IMAGE=relist-panel:probe
DATA=/var/lib/relist-panel
CREDS='{"email":"admin@example.com","password":"correct horse battery staple"}'
# The new host is installed with its admin in the env, the way the README
# recommends; the migrated database replaces that admin with the old one.
SEED='{"email":"seed@example.com","password":"seeded password 123"}'

ACME_SEED='{"letsencrypt":{"Account":null,"Certificates":null},"probe-marker":{"Account":null,"Certificates":null}}'

say() { printf '\033[1mprobe:\033[0m %s\n' "$*"; }
fail() { printf '\033[31mprobe: FAIL\033[0m %s\n' "$*" >&2; exit 1; }

cleanup() {
	docker rm -f "$OLD" "$NEW" >/dev/null 2>&1 || true
	docker volume rm -f "$JAR" >/dev/null 2>&1 || true
	docker network rm "$NET" >/dev/null 2>&1 || true
}
# KEEP=1 leaves the two hosts running after the probe, for debugging.
[ "${KEEP:-}" = 1 ] || trap cleanup EXIT
cleanup

# api HOST JAR METHOD PATH [BODY]: one JSON call to a panel, from a curl
# container on the probe network, keeping that host's cookies in JAR.
api() {
	local host=$1 jar=$2 method=$3 path=$4 body=${5:-}
	local args=(-sS -X "$method" -b "/c/$jar" -c "/c/$jar" -H 'Content-Type: application/json' -w '\n%{http_code}')
	[ -n "$body" ] && args+=(--data "$body")
	docker run --rm --user 0:0 --network "$NET" -v "$JAR:/c" curlimages/curl:8.11.1 "${args[@]}" "http://$host:8888$path"
}
# body and code split api's output.
body() { sed '$d'; }
code() { tail -n1; }
# create PATH BODY: POST to the old host and print the new row's id.
create() {
	local out
	out=$(api "$OLD_IP" old POST "$1" "$2")
	case "$(echo "$out" | code)" in
	200 | 201) echo "$out" | body | json 'd["id"]' ;;
	*) fail "POST $1: $(echo "$out" | body)" ;;
	esac
}
ip() { docker inspect -f "{{(index .NetworkSettings.Networks \"$NET\").IPAddress}}" "$1"; }
json() { python3 -c "import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1], {'d': d}))" "$1"; }

say "building $IMAGE"
docker build -q -t "$IMAGE" "$ROOT" >/dev/null

docker network create "$NET" >/dev/null
docker volume create "$JAR" >/dev/null
for host in "$OLD" "$NEW"; do
	docker run -d --privileged --name "$host" --network "$NET" -e DOCKER_TLS_CERTDIR= docker:27-dind >/dev/null
done
for host in "$OLD" "$NEW"; do
	for _ in $(seq 60); do docker exec "$host" docker info >/dev/null 2>&1 && break; sleep 1; done
	docker save "$IMAGE" | docker exec -i "$host" docker load -q >/dev/null
	docker exec "$host" mkdir -p /install
	sed 's|^\(\s*\)- --certificatesresolvers.letsencrypt.acme.storage=.*$|&\n\1- --certificatesresolvers.letsencrypt.acme.caserver=https://127.0.0.1:9/directory|' \
		"$ROOT/docker-compose.yml" | docker exec -i "$host" sh -c 'cat > /install/docker-compose.yml'
	grep -q 'acme.caserver' <(docker exec "$host" cat /install/docker-compose.yml) || fail "could not point Traefik away from Let's Encrypt"
	if [ "$host" = "$OLD" ]; then
		docker exec "$host" sh -c "mkdir -p $DATA/.traefik && chmod 700 $DATA/.traefik && umask 077 && printf '%s' '$ACME_SEED' > $DATA/.traefik/acme.json"
	fi
	flags='[]'
	[ "$host" = "$NEW" ] && flags='["--insecure-migration-source"]'
	docker exec -i "$host" sh -c 'cat > /install/docker-compose.override.yml' <<EOF
services:
  panel:
    image: $IMAGE
    command: $flags
EOF
	docker exec "$host" docker network create relist-network >/dev/null
	admin=()
	[ "$host" = "$NEW" ] && admin=(-e RELIST_ADMIN_EMAIL=seed@example.com -e "RELIST_ADMIN_PASSWORD=seeded password 123")
	docker exec -w /install -e RELIST_DATA_DIR="$DATA" "${admin[@]}" "$host" docker compose up -d --no-build --quiet-pull >/dev/null
done
OLD_IP=$(ip "$OLD")
NEW_IP=$(ip "$NEW")
for host in "$OLD_IP" "$NEW_IP"; do
	for _ in $(seq 60); do [ "$(api "$host" x GET /healthz | code)" = 200 ] && break; sleep 1; done
done

say "seeding the old host"
[ "$(api "$OLD_IP" old POST /api/auth/setup "$CREDS" | code)" = 201 ] || fail "setup on the old host"
[ "$(api "$OLD_IP" old POST /api/auth/login "$CREDS" | code)" = 200 ] || fail "login on the old host"
P=$(create /api/projects '{"name":"probe"}')
E=$(create "/api/projects/$P/environments" '{"name":"prod"}')
docker exec "$OLD" sh -c 'printf "FROM busybox:1.37\nCMD [\"sleep\",\"1d\"]\n" | docker build -q -t probe/local:1 - >/dev/null'
docker exec "$OLD" sh -c 'mkdir -p /srv/probe && head -c 1048576 /dev/urandom > /srv/probe/small && truncate -s 5G /srv/probe/big'
# A setgid directory of a group the helper's root is not in: chmod keeps the
# bit only with CAP_FSETID.
docker exec "$OLD" sh -c 'mkdir /srv/probe/shared && chown 0:1234 /srv/probe/shared && chmod 2775 /srv/probe/shared'
YAML='services:\n  db:\n    image: postgres:16\n    environment:\n      POSTGRES_PASSWORD: probe\n    volumes:\n      - pgdata:/var/lib/postgresql/data\n  local:\n    image: probe/local:1\n  files:\n    image: busybox:1.37\n    command: [\"httpd\", \"-f\", \"-p\", \"80\", \"-h\", \"/data\"]\n    volumes:\n      - /srv/probe:/data\nvolumes:\n  pgdata: {}\n'
C=$(create "/api/projects/$P/environments/$E/composes" "{\"name\":\"store\",\"yamlContent\":\"$YAML\"}")
BASE="/api/projects/$P/environments/$E/composes/$C"
api "$OLD_IP" old POST "$BASE/domains" '{"host":"probe.localhost","service":"files","port":80}' >/dev/null
[ "$(api "$OLD_IP" old POST "$BASE/deployments" '{"mode":"deploy"}' | code)" = 202 ] || fail "deploy on the old host"
for _ in $(seq 120); do
	docker exec "$OLD" sh -c "docker exec relist-c$C-db-1 pg_isready -U postgres" >/dev/null 2>&1 && break
	sleep 2
done
docker exec "$OLD" docker exec "relist-c$C-db-1" psql -qU postgres -c \
	'CREATE TABLE probe (n int); INSERT INTO probe SELECT generate_series(1, 1000);' >/dev/null
SUM_OLD=$(docker exec "$OLD" sh -c 'cd /srv/probe && sha256sum small big')
# The Volume's root itself: owner and mode must survive, not only its files.
rootstat() { docker exec "$1" docker run --rm -v "relist-c${C}_pgdata:/v" busybox:1.37 stat -c '%u:%g %a' /v; }
ROOT_OLD=$(rootstat "$OLD")
ACME_OLD=$(docker exec "$OLD" sh -c "cat $DATA/.traefik/acme.json 2>/dev/null | sha256sum")
ACME_OLD_TEXT=$(docker exec "$OLD" cat "$DATA/.traefik/acme.json" 2>/dev/null || echo "(none)")

say "migrating"
TOKEN=$(api "$OLD_IP" old POST /api/migration/tokens '{"lifetime":"1h"}' | body | json 'd["token"]')
# The env admin closes setup and the pull routes until someone signs in.
[ "$(api "$NEW_IP" new POST /api/auth/setup "$CREDS" | code)" = 409 ] || fail "setup is still open on the new host"
[ "$(api "$NEW_IP" new GET /api/migration/pull/progress | code)" = 401 ] || fail "the pull routes answer without a sign-in"
[ "$(api "$NEW_IP" new POST /api/auth/login "$SEED" | code)" = 200 ] || fail "sign-in with the env admin"
# The pull is driven over the control socket inside the panel container, the
# way relist-panel migration pull does it (ADR-0019).
ctl() { docker exec "$NEW" docker exec install-panel-1 relist-panel migration api "$@"; }
out=$(ctl POST /api/migration/pull/connect "{\"sourceUrl\":\"http://$OLD_IP:8888\",\"token\":\"$TOKEN\",\"selfUrl\":\"http://$NEW_IP:8888\"}") || fail "connect: $out"
# Finalize must re-execute the panel in place: same container, nothing
# restarted, so a status --watch started before it follows it to the end.
before=$(docker exec "$NEW" docker inspect -f '{{.State.StartedAt}} {{.RestartCount}}' install-panel-1)
docker exec "$NEW" docker exec -d install-panel-1 sh -c 'relist-panel migration status --watch > /tmp/watch.log 2>&1'
out=$(ctl POST /api/migration/pull/start '{"hostPaths":["/srv/probe"]}') || fail "start: $out"
phase=""
for _ in $(seq 900); do
	# Empty while the panel re-executes: the socket is briefly gone.
	out=$(ctl GET /api/migration/pull/progress 2>/dev/null || true)
	[ -n "$out" ] && phase=$(echo "$out" | json 'd["phase"]')
	[ "$phase" = done ] && break
	[ "$phase" = paused ] && fail "paused: $out"
	sleep 2
done
[ "$phase" = done ] || fail "the migration did not finish (last phase: $phase)"
after=$(docker exec "$NEW" docker inspect -f '{{.State.StartedAt}} {{.RestartCount}}' install-panel-1)
[ "$after" = "$before" ] || fail "the panel container restarted at finalize: $before, then $after"
for _ in $(seq 15); do
	tail -n1 <(docker exec "$NEW" docker exec install-panel-1 cat /tmp/watch.log) | grep -q 'done' && break
	sleep 1
done
WATCH=$(docker exec "$NEW" docker exec install-panel-1 cat /tmp/watch.log)
echo "$WATCH" | tail -n1 | grep -q 'done' || { echo "$WATCH"; fail "status --watch did not follow the migration to done"; }
grep -q 'Restarting the panel' <<<"$WATCH" && say "status --watch saw the panel re-execute" || say "status --watch did not poll during the re-execute"
if docker exec "$NEW" docker exec install-panel-1 ls -l /proc/1/fd | grep -q '(deleted)'; then
	docker exec "$NEW" docker exec install-panel-1 ls -l /proc/1/fd
	fail "the re-executed panel holds a deleted file open"
fi

say "checking the new host"
for _ in $(seq 120); do
	docker exec "$NEW" sh -c "docker exec relist-c$C-db-1 pg_isready -U postgres" >/dev/null 2>&1 && break
	sleep 2
done
ROWS=$(docker exec "$NEW" docker exec "relist-c$C-db-1" psql -tAU postgres -c 'SELECT count(*) FROM probe')
[ "$ROWS" = 1000 ] || fail "postgres rows on the new host: $ROWS"
[ "$(docker exec "$NEW" sh -c 'cd /srv/probe && sha256sum small big')" = "$SUM_OLD" ] || fail "/srv/probe differs"
SHARED=$(docker exec "$NEW" stat -c '%u:%g %a' /srv/probe/shared)
[ "$SHARED" = "0:1234 2775" ] || fail "/srv/probe/shared is $SHARED on the new host, want 0:1234 2775"
[ "$(rootstat "$NEW")" = "$ROOT_OLD" ] || fail "the Volume root is $(rootstat "$NEW") on the new host, $ROOT_OLD on the old"
if [ "$(docker exec "$NEW" sh -c "cat $DATA/.traefik/acme.json 2>/dev/null | sha256sum")" != "$ACME_OLD" ]; then
	echo "old acme.json:"; echo "$ACME_OLD_TEXT"
	echo "new acme.json:"; docker exec "$NEW" cat "$DATA/.traefik/acme.json" || true
	fail "acme.json differs"
fi
[ "$(api "$NEW_IP" fresh POST /api/auth/login "$SEED" | code)" = 401 ] || fail "the env admin survived the migration"
for svc in db local files; do
	state=$(docker exec "$NEW" docker inspect -f '{{.State.Status}}' "relist-c$C-$svc-1" 2>/dev/null || true)
	[ "$state" = running ] || fail "service $svc on the new host is $state"
done
[ "$(api "$OLD_IP" old GET /api/projects | code)" = 503 ] || fail "the old host still serves /api/projects"
[ "$(docker exec "$OLD" docker inspect -f '{{.State.Status}}' relist-maintenance)" = running ] || fail "no maintenance container on the old host"
echo "probe: migration ok"
