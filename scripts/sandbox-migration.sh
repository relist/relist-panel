#!/usr/bin/env bash
# Start two panels to try a server migration by hand, as
# `task sandbox:migration`. Like the probe, two docker:dind hosts each run the
# install stack from this checkout, but nothing is migrated: the old one is
# seeded with three Projects of seven Composes, of many kinds (databases with
# data, host paths, a system path and a socket that stay unticked, a Source
# built here, replicas, Variables, a stopped Compose), and both are left
# running. `task sandbox:migration:check` then checks the new host after a
# migration.
#
#   old panel  http://localhost:18881   admin@example.com / correct horse battery staple
#   new panel  http://127.0.0.1:18882   seed@example.com  / seeded password 123 (from create-admin)
#
# The two use different hostnames so their session cookies do not overwrite
# each other. The migration itself runs from a terminal on each host
# (ADR-0019): the printout names the commands, and on the new host the old
# one is http://sandbox-old:8888 (its name on the sandbox network).
# `task sandbox:migration:reload` puts a
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
		docker exec -w /install -e RELIST_DATA_DIR="$DATA" "$host" docker compose up -d --no-build panel >/dev/null 2>&1
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

# check reads, on the new host, what the seed put on the old one.
if [ "${1:-}" = check ]; then
	docker inspect "$NEW" >/dev/null 2>&1 || fail "no sandbox to check: run task sandbox:migration"
	eval "$(docker run --rm -v "$JAR:/c" --entrypoint cat curlimages/curl:8.11.1 /c/ids)"
	failed=0
	expect() {
		local what=$1 want=$2 got
		# After finalize the new panel deploys the Composes one by one, so
		# each check waits up to a minute for its Compose to come up.
		for _ in $(seq 30); do
			got=$(docker exec "$NEW" sh -c "$3" 2>/dev/null | tr -d '[:space:]') || true
			[ "$got" = "$want" ] && break
			sleep 2
		done
		if [ "$got" = "$want" ]; then
			printf '  \033[32mok\033[0m    %s\n' "$what"
		else
			printf '  \033[31mFAIL\033[0m  %s: got %q, want %q\n' "$what" "$got" "$want"
			failed=1
		fi
	}
	# Nothing here may create what a migration then finds taken: a volume is
	# read only once it exists.
	say "checking the new host"
	expect "shop/production postgres orders" 1000 "docker exec relist-c$STORE-db-1 psql -tAU postgres -c 'SELECT count(*) FROM orders'"
	expect "shop/staging postgres orders" 50 "docker exec relist-c$STAGING-db-1 psql -tAU postgres -c 'SELECT count(*) FROM orders'"
	expect "shop/staging redis key" staging "docker exec relist-c$STAGING-cache-1 redis-cli GET greeting"
	expect "blog/site mariadb posts" 200 "docker exec relist-c$SITE-db-1 mariadb -N -uroot -psandbox blog -e 'SELECT count(*) FROM posts'"
	expect "/srv/shop/upload.bin" "$(docker exec "$OLD" sha256sum /srv/shop/upload.bin | cut -d' ' -f1)" "sha256sum /srv/shop/upload.bin | cut -d' ' -f1"
	expect "/srv/blog served by nginx" "<h1>blog</h1>" "docker exec relist-c$SITE-web-1 wget -qO- http://127.0.0.1/"
	expect "blog/app built from its Source" "<h1>app</h1>" "docker exec relist-c$APP-web-1 wget -qO- http://127.0.0.1:8080/"
	expect "tools/whoami replicas running" 2 "docker ps -q -f label=com.docker.compose.project=relist-c$WHOAMI -f status=running | wc -l"
	expect "tools/whoami Variable" WHOAMI_NAME=hello-from-tools "docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' relist-c$WHOAMI-whoami-1 | grep ^WHOAMI_NAME="
	expect "tools/batch stays stopped" 0 "docker ps -q -f label=com.docker.compose.project=relist-c$BATCH -f status=running | wc -l"
	expect "tools/batch volume" done "docker volume inspect relist-c${BATCH}_results >/dev/null && docker run --rm -v relist-c${BATCH}_results:/r busybox:1.37 cat /r/result.txt"
	[ "$failed" = 0 ] || fail "the new host differs from the seed"
	say "the new host has everything the seed made"
	exit 0
fi

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
			imgs=$(docker images --format "{{.Repository}}:{{.Tag}}" | grep -E "^(sandbox/|relist-c)" || true)
			[ -z "$imgs" ] || docker rmi -f $imgs >/dev/null 2>&1 || true
			rm -rf '"$DATA"' /srv/shop /srv/blog'
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
	# The probe reaches each panel at http://<host>:8888 from another container,
	# so it drops the install file's loopback-only host IP.
	sed -e 's|^\(\s*\)- --certificatesresolvers.letsencrypt.acme.storage=.*$|&\n\1- --certificatesresolvers.letsencrypt.acme.caserver=https://127.0.0.1:9/directory|' -e 's|"127\.0\.0\.1:\${RELIST_PORT|"${RELIST_PORT|' \
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
	docker exec -w /install -e RELIST_DATA_DIR="$DATA" "$host" docker compose up -d --no-build --quiet-pull >/dev/null
done
for host in "$OLD" "$NEW"; do
	for _ in $(seq 60); do [ "$(api "$host" GET /healthz | code)" = 200 ] && break; sleep 1; done
done

# mkadmin HOST EMAIL PASSWORD creates the admin the way an operator does.
mkadmin() { printf '%s\n' "$3" | docker exec -i "$1" docker exec -i install-panel-1 relist-panel create-admin --email "$2" --password-stdin >/dev/null; }
mkadmin "$NEW" seed@example.com 'seeded password 123' || fail "create-admin on the new panel"

say "seeding the old panel"
mkadmin "$OLD" admin@example.com 'correct horse battery staple' || fail "create-admin on the old panel"
[ "$(api "$OLD" POST /api/auth/login "$CREDS" | code)" = 200 ] || fail "login on the old panel"
# mkcompose PROJECT ENV NAME, the YAML on stdin, prints the new Compose's id.
mkcompose() {
	create "/api/projects/$1/environments/$2/composes" \
		"$(python3 -c 'import json,sys; print(json.dumps({"name": sys.argv[1], "yamlContent": sys.stdin.read()}))' "$3")"
}
base() { echo "/api/projects/$1/environments/$2/composes/$3"; }
environment() { api "$OLD" GET "/api/projects/$1/environments" | body | json "[e['id'] for e in d if e['name'] == '$2'][0]"; }
# deploy BASE [MODE]: a Compose with a Source takes build, the rest deploy.
deploy() { [ "$(api "$OLD" POST "$1/deployments" "{\"mode\":\"${2:-deploy}\"}" | code)" = 202 ] || fail "deploy $1"; }
# settle waits for a Compose's latest Deployment to end, and fails on a failed one.
settle() {
	local status=""
	for _ in $(seq 300); do
		status=$(api "$OLD" GET "$1/deployments" | body | json 'd[0]["status"]')
		case "$status" in
		succeeded) return 0 ;;
		failed | canceled) fail "deployment of $1 $status" ;;
		esac
		sleep 2
	done
	fail "deployment of $1 still $status"
}
# retry runs a command on the old host until it succeeds, for a database still starting.
retry() {
	for _ in $(seq 120); do docker exec "$OLD" sh -c "$1" >/dev/null 2>&1 && return 0; sleep 2; done
	fail "$2"
}

docker exec "$OLD" sh -c 'printf "FROM busybox:1.37\nCMD [\"sleep\",\"1d\"]\n" | docker build -q -t sandbox/worker:1 - >/dev/null'
docker exec "$OLD" sh -c 'mkdir -p /srv/shop /srv/blog/assets && head -c 1048576 /dev/urandom > /srv/shop/upload.bin && echo "<h1>shop files</h1>" > /srv/shop/index.html
	echo "<h1>blog</h1>" > /srv/blog/index.html && head -c 262144 /dev/urandom > /srv/blog/assets/hero.jpg'

# shop: production keeps the original store; staging is a second Environment.
P=$(create /api/projects '{"name":"shop"}')
# A new Project comes with its production Environment.
E=$(environment "$P" production)
STORE=$(mkcompose "$P" "$E" store <<'YAML'
services:
  db:
    image: postgres:16
    environment:
      POSTGRES_PASSWORD: sandbox
    volumes:
      - pgdata:/var/lib/postgresql/data
  worker:
    image: sandbox/worker:1
  files:
    image: busybox:1.37
    command: ["httpd", "-f", "-p", "80", "-h", "/data"]
    volumes:
      - /srv/shop:/data
volumes:
  pgdata: {}
YAML
)
api "$OLD" POST "$(base "$P" "$E" "$STORE")/domains" '{"host":"shop.localhost","service":"files","port":80}' >/dev/null
ES=$(create "/api/projects/$P/environments" '{"name":"staging"}')
STAGING=$(mkcompose "$P" "$ES" store <<'YAML'
services:
  db:
    image: postgres:16
    environment:
      POSTGRES_PASSWORD: sandbox
    volumes:
      - pgdata:/var/lib/postgresql/data
  cache:
    image: redis:7-alpine
    command: ["redis-server", "--appendonly", "yes"]
    volumes:
      - redisdata:/data
volumes:
  pgdata: {}
  redisdata: {}
YAML
)

# blog: a database site behind nginx on a host path, and an app built from a zip Source.
B=$(create /api/projects '{"name":"blog"}')
BE=$(environment "$B" production)
SITE=$(mkcompose "$B" "$BE" site <<'YAML'
services:
  db:
    image: mariadb:11
    environment:
      MARIADB_ROOT_PASSWORD: sandbox
      MARIADB_DATABASE: blog
    volumes:
      - dbdata:/var/lib/mysql
  web:
    image: nginx:1.27-alpine
    volumes:
      - /srv/blog:/usr/share/nginx/html:ro
      - /etc/localtime:/etc/localtime:ro
  sessions:
    image: memcached:1.6-alpine
volumes:
  dbdata: {}
YAML
)
api "$OLD" POST "$(base "$B" "$BE" "$SITE")/domains" '{"host":"blog.localhost","service":"web","port":80}' >/dev/null
APP=$(mkcompose "$B" "$BE" app <<'YAML'
services:
  web:
    build: ./src
YAML
)
python3 -c '
import io, sys, zipfile
buf = io.BytesIO()
with zipfile.ZipFile(buf, "w") as z:
    z.writestr("Dockerfile", "FROM busybox:1.37\nCOPY index.html /www/\nCMD [\"httpd\", \"-f\", \"-p\", \"8080\", \"-h\", \"/www\"]\n")
    z.writestr("index.html", "<h1>app</h1>\n")
sys.stdout.buffer.write(buf.getvalue())' |
	docker run --rm -i --user 0:0 -v "$JAR:/c" --entrypoint sh curlimages/curl:8.11.1 -c 'cat > /c/app.zip'
up=$(docker run --rm --user 0:0 --network "$NET" -v "$JAR:/c" curlimages/curl:8.11.1 -sS -b /c/old -c /c/old \
	-F file=@/c/app.zip -w '\n%{http_code}' "http://$OLD:8888$(base "$B" "$BE" "$APP")/source/upload")
case "$(echo "$up" | code)" in 200 | 201) ;; *) fail "upload the app's Source: $(echo "$up" | body)" ;; esac
api "$OLD" POST "$(base "$B" "$BE" "$APP")/domains" '{"host":"app.localhost","service":"web","port":8080}' >/dev/null

# tools: replicas reading Variables, a socket bind, and a Compose left stopped.
T=$(create /api/projects '{"name":"tools"}')
TE=$(environment "$T" production)
create "/api/projects/$T/environments/$TE/variables" '{"key":"GREETING","value":"hello-from-tools","secret":false}' >/dev/null
create "/api/projects/$T/environments/$TE/variables" '{"key":"API_TOKEN","value":"sandbox-secret-token","secret":true}' >/dev/null
WHOAMI=$(mkcompose "$T" "$TE" whoami <<'YAML'
services:
  whoami:
    image: traefik/whoami:v1.10
    deploy:
      replicas: 2
    environment:
      WHOAMI_NAME: ${GREETING}
      API_TOKEN: ${API_TOKEN}
YAML
)
api "$OLD" POST "$(base "$T" "$TE" "$WHOAMI")/domains" '{"host":"whoami.localhost","service":"whoami","port":80}' >/dev/null
MONITOR=$(mkcompose "$T" "$TE" monitor <<'YAML'
services:
  agent:
    image: busybox:1.37
    command: ["sleep", "1d"]
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
YAML
)
BATCH=$(mkcompose "$T" "$TE" batch <<'YAML'
services:
  job:
    image: busybox:1.37
    command: ["sh", "-c", "echo done > /results/result.txt && sleep 1d"]
    volumes:
      - results:/results
volumes:
  results: {}
YAML
)

say "deploying seven Composes"
for c in "$P $E $STORE" "$P $ES $STAGING" "$B $BE $SITE" "$T $TE $WHOAMI" "$T $TE $MONITOR" "$T $TE $BATCH"; do
	deploy "$(base $c)"
done
deploy "$(base "$B" "$BE" "$APP")" build
for c in "$P $E $STORE" "$P $ES $STAGING" "$B $BE $SITE" "$B $BE $APP" "$T $TE $WHOAMI" "$T $TE $MONITOR" "$T $TE $BATCH"; do
	settle "$(base $c)"
done
[ "$(api "$OLD" POST "$(base "$T" "$TE" "$BATCH")/stop" | code)" = 200 ] || fail "stop tools/batch"

say "filling the databases"
# pg_isready can answer from the image's temporary initdb server, which then
# shuts down: retry each seed until the real server takes it.
retry "docker exec relist-c$STORE-db-1 psql -qU postgres -c \"CREATE TABLE orders (id int, item text); INSERT INTO orders SELECT n, 'item ' || n FROM generate_series(1, 1000) n;\"" "seed shop/production postgres"
retry "docker exec relist-c$STAGING-db-1 psql -qU postgres -c \"CREATE TABLE orders (id int, item text); INSERT INTO orders SELECT n, 'staging ' || n FROM generate_series(1, 50) n;\"" "seed shop/staging postgres"
retry "docker exec relist-c$STAGING-cache-1 redis-cli SET greeting staging | grep -q OK" "seed shop/staging redis"
retry "docker exec relist-c$SITE-db-1 mariadb -uroot -psandbox blog -e \"CREATE TABLE posts (id int, title text); INSERT INTO posts SELECT seq, CONCAT('post ', seq) FROM seq_1_to_200;\"" "seed blog/site mariadb"
printf 'STORE=%s\nSTAGING=%s\nSITE=%s\nAPP=%s\nWHOAMI=%s\nMONITOR=%s\nBATCH=%s\n' "$STORE" "$STAGING" "$SITE" "$APP" "$WHOAMI" "$MONITOR" "$BATCH" |
	docker run --rm -i --user 0:0 -v "$JAR:/c" --entrypoint sh curlimages/curl:8.11.1 -c 'cat > /c/ids'

cat <<EOF

$(say ready)
  old panel  http://localhost:$OLD_PORT
             admin@example.com / correct horse battery staple
             shop   production  store    postgres (1000 rows), a local image, /srv/shop       shop.localhost
                    staging     store    postgres (50 rows), redis (appendonly)
             blog   production  site     mariadb (200 rows), nginx on /srv/blog, memcached   blog.localhost
                                app      built from a zip Source                              app.localhost
             tools  production  whoami   2 replicas, Variables GREETING and API_TOKEN (secret) whoami.localhost
                                monitor  binds /var/run/docker.sock (a socket: stays unticked)
                                batch    stopped, with a named volume
  new panel  http://127.0.0.1:$NEW_PORT
             seed@example.com / seeded password 123

  migrate from a terminal (ADR-0019):
    old  docker exec -it $OLD docker exec -it install-panel-1 relist-panel migration serve
    new  docker exec -it $NEW docker exec -it install-panel-1 relist-panel migration pull
         old panel URL: http://$OLD:8888   this panel's URL: http://127.0.0.1:$NEW_PORT
         host paths: tick /srv/shop and /srv/blog; /etc/localtime and the socket stay unticked

  check the new host after the migration:
    task sandbox:migration:check
  remove everything:
    task sandbox:migration:down
EOF
