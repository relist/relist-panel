#!/usr/bin/env bash
# Start two panels to try a server migration by hand, as
# `task sandbox:migration`. Like the probe, two docker:dind hosts each run the
# install stack from this checkout, but nothing is migrated: the old one is
# seeded with one Project of three Composes and both are left running.
#
#   old panel  http://localhost:18881   admin@example.com / correct horse battery staple
#   new panel  http://127.0.0.1:18882   seed@example.com  / seeded password 123 (from the env)
#
# The two use different hostnames so their session cookies do not overwrite
# each other. On the new panel, connect to http://sandbox-old:8888 (the old
# host's name on the sandbox network). `task sandbox:migration:reload` puts a
# backend change into the running panels, `task sandbox:migration:reset` wipes
# both back to the start (the old one seeded, the new one empty), and
# `task sandbox:migration:down` removes everything. Traefik's ACME CA points at an unreachable address, so nothing
# here registers with Let's Encrypt.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
NET=sandbox-mig
OLD=sandbox-old
NEW=sandbox-new
JAR=sandbox-mig-cookies
IMAGE=relist-panel:sandbox
DATA=/var/lib/relist-panel
OLD_PORT=${OLD_PORT:-18881}
NEW_PORT=${NEW_PORT:-18882}
CREDS='{"email":"admin@example.com","password":"correct horse battery staple"}'

say() { printf '\033[1msandbox:\033[0m %s\n' "$*"; }
fail() { printf '\033[31msandbox: FAIL\033[0m %s\n' "$*" >&2; exit 1; }

down() {
	docker rm -f "$OLD" "$NEW" >/dev/null 2>&1 || true
	docker volume rm -f "$JAR" >/dev/null 2>&1 || true
	docker network rm "$NET" >/dev/null 2>&1 || true
}
if [ "${1:-}" = down ]; then
	down
	say "removed"
	exit 0
fi
# reload rebuilds the image from this checkout and recreates each panel that is
# running, keeping the sandbox's data: a backend change without a reseed. An
# old host already in maintenance after a migration is left as it is.
if [ "${1:-}" = reload ]; then
	docker inspect "$OLD" "$NEW" >/dev/null 2>&1 || fail "no sandbox to reload: run task sandbox:migration"
	say "building $IMAGE"
	docker build -q -t "$IMAGE" "$ROOT" >/dev/null
	for host in "$OLD" "$NEW"; do
		if [ -z "$(docker exec "$host" docker ps -q -f name=install-panel-1)" ]; then
			say "$host: panel not running, left as it is"
			continue
		fi
		docker save "$IMAGE" | docker exec -i "$host" docker load -q >/dev/null
		admin=()
		[ "$host" = "$NEW" ] && admin=(-e RELIST_ADMIN_EMAIL=seed@example.com -e "RELIST_ADMIN_PASSWORD=seeded password 123")
		docker exec -w /install -e RELIST_DATA_DIR="$DATA" "${admin[@]}" "$host" docker compose up -d --no-build panel >/dev/null 2>&1
		say "$host: panel reloaded"
	done
	exit 0
fi

api() {
	local host=$1 method=$2 path=$3 body=${4:-}
	local args=(-sS -X "$method" -b /c/old -c /c/old -H 'Content-Type: application/json' -w '\n%{http_code}')
	[ -n "$body" ] && args+=(--data "$body")
	docker run --rm --user 0:0 --network "$NET" -v "$JAR:/c" curlimages/curl:8.11.1 "${args[@]}" "http://$host:8888$path"
}
ip() { docker inspect -f "{{(index .NetworkSettings.Networks \"$NET\").IPAddress}}" "$1"; }
body() { sed '$d'; }
code() { tail -n1; }
json() { python3 -c "import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1], {'d': d}))" "$1"; }
create() {
	local out
	out=$(api "$OLD" POST "$1" "$2")
	case "$(echo "$out" | code)" in
	200 | 201) echo "$out" | body | json 'd["id"]' ;;
	*) fail "POST $1: $(echo "$out" | body)" ;;
	esac
}

# reset wipes both hosts back to a fresh install and reseeds the old one,
# keeping the dind hosts, the panel image and the pulled images, so it is
# quicker than starting over.
if [ "${1:-}" = reset ]; then
	docker inspect "$OLD" "$NEW" >/dev/null 2>&1 || fail "no sandbox to reset: run task sandbox:migration"
	say "wiping both hosts"
	for host in "$OLD" "$NEW"; do
		docker exec "$host" sh -c '
			ids=$(docker ps -aq); [ -z "$ids" ] || docker rm -f $ids >/dev/null
			docker volume prune -af >/dev/null
			docker network prune -f >/dev/null
			docker rmi -f sandbox/worker:1 >/dev/null 2>&1 || true
			rm -rf '"$DATA"' /srv/shop'
	done
	docker run --rm --user 0:0 -v "$JAR:/c" --entrypoint rm curlimages/curl:8.11.1 -f /c/old
else
	down
	say "building $IMAGE"
	docker build -q -t "$IMAGE" "$ROOT" >/dev/null
	docker network create "$NET" >/dev/null
	docker volume create "$JAR" >/dev/null
	docker run -d --privileged --name "$OLD" --network "$NET" -p "127.0.0.1:$OLD_PORT:8888" -e DOCKER_TLS_CERTDIR= docker:27-dind >/dev/null
	docker run -d --privileged --name "$NEW" --network "$NET" -p "127.0.0.1:$NEW_PORT:8888" -e DOCKER_TLS_CERTDIR= docker:27-dind >/dev/null
	for host in "$OLD" "$NEW"; do
		for _ in $(seq 60); do docker exec "$host" docker info >/dev/null 2>&1 && break; sleep 1; done
		docker save "$IMAGE" | docker exec -i "$host" docker load -q >/dev/null
	done
fi
for host in "$OLD" "$NEW"; do
	docker exec "$host" mkdir -p /install
	sed 's|^\(\s*\)- --certificatesresolvers.letsencrypt.acme.storage=.*$|&\n\1- --certificatesresolvers.letsencrypt.acme.caserver=https://127.0.0.1:9/directory|' \
		"$ROOT/docker-compose.yml" | docker exec -i "$host" sh -c 'cat > /install/docker-compose.yml'
	grep -q 'acme.caserver' <(docker exec "$host" cat /install/docker-compose.yml) || fail "could not point Traefik away from Let's Encrypt"
	flags='[]'
	hosts='[]'
	# The new panel runs in the new host's own Docker, where the sandbox
	# network's names do not resolve: map the old host's name to its address.
	if [ "$host" = "$NEW" ]; then
		flags='["--insecure-migration-source"]'
		hosts="[\"$OLD:$(ip "$OLD")\"]"
	fi
	docker exec -i "$host" sh -c 'cat > /install/docker-compose.override.yml' <<EOF
services:
  panel:
    image: $IMAGE
    command: $flags
    extra_hosts: $hosts
EOF
	docker exec "$host" sh -c 'docker network inspect relist-network >/dev/null 2>&1 || docker network create relist-network >/dev/null'
	admin=()
	[ "$host" = "$NEW" ] && admin=(-e RELIST_ADMIN_EMAIL=seed@example.com -e "RELIST_ADMIN_PASSWORD=seeded password 123")
	docker exec -w /install -e RELIST_DATA_DIR="$DATA" "${admin[@]}" "$host" docker compose up -d --no-build --quiet-pull >/dev/null
done
for host in "$OLD" "$NEW"; do
	for _ in $(seq 60); do [ "$(api "$host" GET /healthz | code)" = 200 ] && break; sleep 1; done
done

say "seeding the old panel"
[ "$(api "$OLD" POST /api/auth/setup "$CREDS" | code)" = 201 ] || fail "setup on the old panel"
[ "$(api "$OLD" POST /api/auth/login "$CREDS" | code)" = 200 ] || fail "login on the old panel"
P=$(create /api/projects '{"name":"shop"}')
# A new Project comes with its production Environment.
E=$(api "$OLD" GET "/api/projects/$P/environments" | body | json '[e["id"] for e in d if e["name"] == "production"][0]')
docker exec "$OLD" sh -c 'printf "FROM busybox:1.37\nCMD [\"sleep\",\"1d\"]\n" | docker build -q -t sandbox/worker:1 - >/dev/null'
docker exec "$OLD" sh -c 'mkdir -p /srv/shop && head -c 1048576 /dev/urandom > /srv/shop/upload.bin && echo "<h1>shop files</h1>" > /srv/shop/index.html'
YAML='services:\n  db:\n    image: postgres:16\n    environment:\n      POSTGRES_PASSWORD: sandbox\n    volumes:\n      - pgdata:/var/lib/postgresql/data\n  worker:\n    image: sandbox/worker:1\n  files:\n    image: busybox:1.37\n    command: [\"httpd\", \"-f\", \"-p\", \"80\", \"-h\", \"/data\"]\n    volumes:\n      - /srv/shop:/data\nvolumes:\n  pgdata: {}\n'
C=$(create "/api/projects/$P/environments/$E/composes" "{\"name\":\"store\",\"yamlContent\":\"$YAML\"}")
BASE="/api/projects/$P/environments/$E/composes/$C"
api "$OLD" POST "$BASE/domains" '{"host":"shop.localhost","service":"files","port":80}' >/dev/null
[ "$(api "$OLD" POST "$BASE/deployments" '{"mode":"deploy"}' | code)" = 202 ] || fail "deploy on the old panel"
for _ in $(seq 120); do
	docker exec "$OLD" sh -c "docker exec relist-c$C-db-1 pg_isready -U postgres" >/dev/null 2>&1 && break
	sleep 2
done
docker exec "$OLD" docker exec "relist-c$C-db-1" psql -qU postgres -c \
	'CREATE TABLE orders (id int, item text); INSERT INTO orders SELECT n, '"'"'item '"'"' || n FROM generate_series(1, 1000) n;' >/dev/null

cat <<EOF

$(say ready)
  old panel  http://localhost:$OLD_PORT
             admin@example.com / correct horse battery staple
             Project "shop" > production > Compose "store" (postgres with 1000 rows, a local image, /srv/shop)
  new panel  http://127.0.0.1:$NEW_PORT
             seed@example.com / seeded password 123
             Source URL to connect to: http://$OLD:8888   Host path to select: /srv/shop

  check postgres on the new host after the migration:
    docker exec $NEW docker exec relist-c$C-db-1 psql -U postgres -c 'SELECT count(*) FROM orders'
  remove everything:
    task sandbox:migration:down
EOF
