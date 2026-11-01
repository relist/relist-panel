#!/usr/bin/env bash
# Probe the Install script and the Panel update against real Docker (ADR-0024,
# ADR-0025), as `task probe:panel-update`.
#
# Everything runs inside one docker:dind host, a separate Docker daemon, so
# nothing here touches the daemon this script runs on: the probe's panel has the
# same Container names, router names, ports (80/443) and network (relist-network)
# as a live install, and a live Traefik would pick it up. It refuses to run
# anywhere else.
#
# Inside the dind host:
#   - a registry:2 on localhost:5000, because an update pulls the manifest's image
#     by digest and a locally built image has no digest;
#   - a manifest server (busybox httpd, "su-manifest") on relist-network, which
#     the update command reads through RELIST_UPDATE_MANIFEST_URL;
#   - the Install script (release/install.sh), piped into `sh` as `curl | sh`
#     would, with RELIST_INSTALL_IMAGE pointing at the registry.
# The manifests are signed with a throwaway key, built into both panel images.
#
# The six steps, in order, each printed as PASS or FAIL:
#   1. install.sh --auto installs 1.0.0 and prints the admin password;
#   2. a second install.sh is refused;
#   3. a slow Deployment runs; install.sh --update to 1.1.0 logs what it waits
#      on, then succeeds;
#   4. an update cancelled with `docker stop` while quiescing ends aborted and
#      the panel's lock is released;
#   5. an update whose new panel never answers rolls back, and every Install
#      file and .env match their bytes from before the run;
#   6. with the panel stopped, install.sh --update notes the panel did not
#      answer, takes the backup and succeeds.
#
# It needs a Docker that can run privileged containers, internet access for
# registry:2, busybox and traefik (unless the host has them), and a few GB of
# disk. It removes the dind host and the two images it built on exit; KEEP=1
# leaves the dind host running for debugging. Step 5 waits the 180 s health
# deadline.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
DIND=su-test-update-dind
IMG=su-test/panel
CTX=$(mktemp -d -t su-test-ctx.XXXXXX)
WORK=$(mktemp -d -t su-test-work.XXXXXX)
INSTALL_DIR=/opt/relist-panel
DATA_DIR=/var/lib/relist-panel
ADMIN=admin@relist.local

say() { printf '\033[1mprobe:\033[0m %s\n' "$*" >&2; }
PASSED=()
pass() { PASSED+=("$1"); printf '\033[32mprobe: PASS\033[0m step %s\n' "$1" >&2; }
fail() { printf '\033[31mprobe: FAIL\033[0m %s\n' "$*" >&2; exit 1; }
summary() {
	local status=$?
	if [ $status -ne 0 ]; then
		printf '\033[31mprobe: FAILED\033[0m (passed steps: %s)\n' "${PASSED[*]:-none}" >&2
		# What the Updater said, for the one that was running.
		docker exec "$DIND" sh -c 'for c in $(docker ps -aq --filter name=relist-panel-updater-); do docker logs --tail 30 $c 2>&1; done' >&2 2>/dev/null || true
	fi
	cleanup
	exit $status
}
cleanup() {
	[ "${KEEP:-}" = 1 ] && return 0
	docker rm -fv "$DIND" >/dev/null 2>&1 || true
	docker rmi -f "$IMG:a" "$IMG:b" >/dev/null 2>&1 || true
	rm -rf "$CTX" "$WORK"
}
trap summary EXIT

# dind runs a command in the dind host.
dind() { docker exec -i "$DIND" "$@"; }
# put DEST: stdin becomes a file in the dind host.
put() { docker exec -i "$DIND" sh -c "cat > $1"; }

# api METHOD PATH [BODY]: one JSON call to the panel from a curl container that
# shares the dind host's network, keeping the session in $WORK/jar.
api() {
	local method=$1 path=$2 body=${3:-}
	local args=(-sS -X "$method" -b /w/jar -c /w/jar -H 'Content-Type: application/json' -w '\n%{http_code}')
	[ -n "$body" ] && args+=(--data "$body")
	docker run --rm --user 0:0 --network "container:$DIND" -v "$WORK:/w" curlimages/curl:8.11.1 "${args[@]}" "http://127.0.0.1:8888$path"
}
body() { sed '$d'; }
code() { tail -n1; }
json() { python3 -c "import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1], {'d': d}))" "$1"; }
status() { api GET /api/panel-update/status 2>/dev/null | body; }

# install_sh ARGS...: the Install script, piped into sh like `curl | sh`, with
# the image and the manifest location of the probe. Output goes to stdout and
# stderr; the exit code is the script's.
install_sh() {
	docker exec -i -e "RELIST_INSTALL_IMAGE=$REF_A" -e RELIST_UPDATE_MANIFEST_URL=http://su-manifest/update-manifest.json \
		"$DIND" sh -s -- "$@" <"$ROOT/release/install.sh"
}

# Every Docker command below that touches an install runs inside $DIND, a
# privileged docker:dind container with its own daemon; the host's daemon only
# builds the two images and starts the dind host.
command -v docker >/dev/null || fail "docker is not installed"

# ---- the throwaway release key and the Install files ----
say "making the key and the Install files"
openssl genpkey -algorithm ed25519 -out "$WORK/key.pem" 2>/dev/null
KEY=$(openssl pkey -in "$WORK/key.pem" -pubout -outform DER | tail -c 32 | base64 -w0)
# 1.0.0 ships the repo's Install file as it is; each later version differs by a
# first comment line, so an update has something to write.
cp "$ROOT/docker-compose.yml" "$WORK/install-a.yml"
{ echo "# su-test: the Install file of 1.1.0"; cat "$ROOT/docker-compose.yml"; } >"$WORK/install-b.yml"
{ echo "# su-test: the Install file of 1.2.0"; cat "$ROOT/docker-compose.yml"; } >"$WORK/install-c.yml"
# The same, with a panel command that never starts, so the health check fails.
{ echo "# su-test: the Install file of 1.2.0 (broken)"; sed '0,/^    container_name: relist-panel$/s//    container_name: relist-panel\n    command: ["--bogus"]/' "$ROOT/docker-compose.yml"; } >"$WORK/install-bad.yml"
grep -q -- '--bogus' "$WORK/install-bad.yml" || fail "the panel service of docker-compose.yml has changed shape: update the probe"

# ---- the two panel images, built from a copy of this checkout whose
# docker-compose.yml is the Install file the image embeds ----
say "building $IMG:a (1.0.0) and $IMG:b (1.1.0)"
tar -C "$ROOT" --exclude=.git --exclude=node_modules --exclude=.scratch --exclude=.superpowers --exclude=.claude -cf - . | tar -C "$CTX" -xf -
build() { # VERSION TAG INSTALL_FILE
	cp "$WORK/$3" "$CTX/docker-compose.yml"
	docker build -q --build-arg "VERSION=$1" --build-arg "RELEASE_KEY=$KEY" -t "$IMG:$2" "$CTX" >/dev/null
}
build 1.0.0 a install-a.yml
build 1.1.0 b install-b.yml

# ---- the dind host ----
say "starting the dind host"
docker rm -fv "$DIND" >/dev/null 2>&1 || true
docker run -d --privileged --name "$DIND" -e DOCKER_TLS_CERTDIR= docker:27-dind >/dev/null
for _ in $(seq 60); do dind docker info >/dev/null 2>&1 && break; sleep 1; done
dind docker info >/dev/null 2>&1 || fail "the dind host did not start"
# Images the host already has are loaded; the rest are pulled by the dind host.
for image in traefik:v3.7 busybox:1.37 registry:2; do
	if docker image inspect "$image" >/dev/null 2>&1; then
		docker save "$image" | dind docker load -q >/dev/null
	else
		dind docker pull -q "$image" >/dev/null
	fi
done
docker save "$IMG:a" | dind docker load -q >/dev/null
docker save "$IMG:b" | dind docker load -q >/dev/null

say "pushing both panel images to a registry in the dind host"
dind docker run -d --name su-registry -p 5000:5000 registry:2 >/dev/null
for _ in $(seq 30); do dind wget -qO- http://127.0.0.1:5000/v2/ >/dev/null 2>&1 && break; sleep 1; done
for t in a b; do
	dind docker tag "$IMG:$t" "localhost:5000/$IMG:$t"
	dind docker push -q "localhost:5000/$IMG:$t" >/dev/null
done
REF_A=$(dind docker inspect -f '{{index .RepoDigests 0}}' "localhost:5000/$IMG:a")
REF_B=$(dind docker inspect -f '{{index .RepoDigests 0}}' "localhost:5000/$IMG:b")
ID_B=$(dind docker inspect -f '{{.Id}}' "$IMG:b")
case "$REF_A$REF_B" in localhost:5000/*@sha256:*localhost:5000/*@sha256:*) ;; *) fail "no digest references: $REF_A $REF_B" ;; esac
# Only the registry holds them from here on, so every pull is real.
dind docker rmi "$IMG:a" "$IMG:b" "localhost:5000/$IMG:a" "localhost:5000/$IMG:b" >/dev/null
say "a is $REF_A; b is $REF_B"

# The shared network and the manifest server. install.sh creates relist-network
# when it is missing; the probe creates it first so the manifest server can sit
# on it.
dind docker network create relist-network >/dev/null
dind mkdir -p /srv/manifest
dind docker run -d --name su-manifest --network relist-network -v /srv/manifest:/www busybox:1.37 httpd -f -p 80 -h /www >/dev/null

# publish VERSION INSTALL_FILE: sign and serve a manifest naming b's digest.
publish() {
	python3 - "$1" "$WORK/$2" "$REF_B" "$WORK/manifest.json" <<'PY'
import json, sys
version, install, ref, out = sys.argv[1:]
manifest = {
    "schema": 1, "version": version, "released_at": "2026-11-20T09:00:00Z", "image": ref,
    "notes_url": "https://example.test/notes",
    "install_file": open(install).read(),
    "invariants": [
        {"kind": "service-image", "service": "panel", "image": ref},
        {"kind": "service-mount", "service": "panel", "source": "/var/run/docker.sock", "target": "/var/run/docker.sock"},
        {"kind": "service-network", "service": "panel", "network": "relist-network"},
        {"kind": "network-external", "network": "relist-network", "name": "relist-network"},
    ],
    "manual_steps": [],
}
open(out, "w").write(json.dumps(manifest))
PY
	openssl pkeyutl -sign -inkey "$WORK/key.pem" -rawin -in "$WORK/manifest.json" -out "$WORK/manifest.json.sig"
	put /srv/manifest/update-manifest.json <"$WORK/manifest.json"
	put /srv/manifest/update-manifest.json.sig <"$WORK/manifest.json.sig"
}

# slow_deployment NAME: a Compose whose `up` blocks for about a minute (its
# dependency is healthy only after sleeping), deployed through the panel's API,
# so a Deployment is running while the update asks the panel to quiesce.
SLOW_PROJECT=""
SLOW_ENV=""
slow_deployment() {
	local name=$1 out compose yaml
	if [ -z "$SLOW_PROJECT" ]; then
		out=$(api POST /api/projects '{"name":"probe","description":""}')
		[ "$(echo "$out" | code)" = 201 ] || fail "create project: $(echo "$out" | body)"
		SLOW_PROJECT=$(echo "$out" | body | json 'd["id"]')
		out=$(api POST "/api/projects/$SLOW_PROJECT/environments" '{"name":"prod"}')
		[ "$(echo "$out" | code)" = 201 ] || fail "create environment: $(echo "$out" | body)"
		SLOW_ENV=$(echo "$out" | body | json 'd["id"]')
	fi
	yaml=$(python3 - <<'PY'
import json
print(json.dumps("""services:
  slow:
    image: busybox:1.37
    command: ["sh", "-c", "sleep 60; touch /tmp/ready; sleep 3600"]
    healthcheck:
      test: ["CMD", "test", "-f", "/tmp/ready"]
      interval: 2s
      timeout: 2s
      retries: 60
  app:
    image: busybox:1.37
    command: ["sleep", "3600"]
    depends_on:
      slow:
        condition: service_healthy
"""))
PY
)
	out=$(api POST "/api/projects/$SLOW_PROJECT/environments/$SLOW_ENV/composes" "{\"name\":\"$name\",\"yamlContent\":$yaml}")
	[ "$(echo "$out" | code)" = 201 ] || fail "create compose: $(echo "$out" | body)"
	compose=$(echo "$out" | body | json 'd["id"]')
	out=$(api POST "/api/projects/$SLOW_PROJECT/environments/$SLOW_ENV/composes/$compose/deployments" '{"mode":"deploy"}')
	case "$(echo "$out" | code)" in 200 | 201 | 202) ;; *) fail "deploy $name: $(echo "$out" | body)" ;; esac
	for _ in $(seq 30); do
		[ "$(api GET /api/deployments/queue | body | json 'any(x.get("status") == "running" for x in d)' 2>/dev/null || echo False)" = True ] && return 0
		sleep 1
	done
	fail "the Deployment of $name did not start running"
}

# wait_unlocked: the panel's watcher settles a finished run and releases the lock.
wait_unlocked() {
	for _ in $(seq 60); do
		[ "$(status | json 'd["locked"]' 2>/dev/null || echo True)" = False ] && return 0
		sleep 2
	done
	fail "the update lock is still on"
}
panel_image() { dind docker inspect -f '{{.Image}}' relist-panel; }

# ============ 1. install.sh --auto installs 1.0.0 ============
say "step 1: install.sh --auto --domain panel.localhost"
install_sh --auto --domain panel.localhost >"$WORK/install1.out" 2>&1 || { cat "$WORK/install1.out" >&2; fail "step 1: install.sh --auto exited non-zero"; }
PASSWORD=$(sed -n 's/^Password: //p' "$WORK/install1.out" | head -n1)
[ -n "$PASSWORD" ] || { cat "$WORK/install1.out" >&2; fail "step 1: no password line in the output"; }
[ "$(dind docker ps --filter name=relist-panel --filter status=running -q | wc -l)" -ge 1 ] || fail "step 1: relist-panel is not running"
[ "$(dind docker ps --filter name=relist-traefik --filter status=running -q | wc -l)" = 1 ] || fail "step 1: relist-traefik is not running"
dind grep -q "^RELIST_PANEL_IMAGE=$REF_A\$" "$INSTALL_DIR/.env" || fail "step 1: .env does not pin a"
[ "$(api POST /api/auth/login "{\"email\":\"$ADMIN\",\"password\":\"$PASSWORD\"}" | code)" = 200 ] || fail "step 1: the printed password does not sign in"
[ "$(status | json 'd["version"]')" = 1.0.0 ] || fail "step 1: running version is not 1.0.0"
pass 1

# ============ 2. a second install.sh is refused ============
say "step 2: a second install.sh"
if install_sh --auto --domain panel.localhost >"$WORK/install2.out" 2>&1; then
	fail "step 2: the second install.sh succeeded"
fi
grep -q -- '--update' "$WORK/install2.out" || { cat "$WORK/install2.out" >&2; fail "step 2: the refusal does not point to --update"; }
[ "$(status | json 'd["version"]')" = 1.0.0 ] || fail "step 2: the refused install disturbed the panel"
pass 2

# ============ 3. a running Deployment is waited for ============
say "step 3: install.sh --update to 1.1.0 while a Deployment runs"
publish 1.1.0 install-b.yml
slow_deployment slow3
install_sh --update --auto >"$WORK/update3.out" 2>&1 || { cat "$WORK/update3.out" >&2; fail "step 3: install.sh --update exited non-zero"; }
grep -q 'waiting on Deployment' "$WORK/update3.out" || { cat "$WORK/update3.out" >&2; fail "step 3: the log never said what the update waited on"; }
grep -q 'Update succeeded' "$WORK/update3.out" || { cat "$WORK/update3.out" >&2; fail "step 3: the update did not report success"; }
for _ in $(seq 30); do [ "$(status | json 'd["version"]' 2>/dev/null)" = 1.1.0 ] && break; sleep 2; done
[ "$(status | json 'd["version"]')" = 1.1.0 ] || fail "step 3: the panel did not come up at 1.1.0"
[ "$(panel_image)" = "$ID_B" ] || fail "step 3: the panel runs $(panel_image), want $ID_B"
dind grep -q "^RELIST_PANEL_IMAGE=$REF_B\$" "$INSTALL_DIR/.env" || fail "step 3: .env does not pin b"
dind grep -q 'the Install file of 1.1.0' "$INSTALL_DIR/docker-compose.yml" || fail "step 3: the Install file was not replaced"
wait_unlocked
# The image 1.0.0 ran from is unused now; a prune plan names it as the Panel
# update's, and skips it. (Checked here: a later successful update, step 6,
# makes the previous image the one that is still running.)
PLAN=$(api GET '/api/docker/prune-plan?kind=unused-images')
[ "$(echo "$PLAN" | code)" = 200 ] || fail "step 3: prune plan: $(echo "$PLAN" | body)"
echo "$PLAN" | body | json '[s for s in d["skip"] if s.get("compose") == "Panel update"][0]["name"]' >/dev/null || fail "step 3: the previous panel image is not skipped as Panel update's"
pass 3

# ============ 4. a cancelled update ends aborted ============
say "step 4: cancel an update while it is quiescing"
publish 1.2.0 install-c.yml
slow_deployment slow4
install_sh --update --auto >"$WORK/update4.out" 2>&1 &
UPDATE4=$!
for _ in $(seq 60); do
	[ "$(status | json '(d["run"] or {}).get("state", "")' 2>/dev/null)" = quiescing ] && break
	sleep 1
done
[ "$(status | json '(d["run"] or {}).get("state", "")')" = quiescing ] || { cat "$WORK/update4.out" >&2; fail "step 4: the run never reached quiescing"; }
UPDATER=$(dind docker ps --filter name=relist-panel-updater- --format '{{.Names}}' | head -n1)
[ -n "$UPDATER" ] || fail "step 4: no Updater Container"
dind docker stop "$UPDATER" >/dev/null
wait "$UPDATE4" || true
for _ in $(seq 30); do [ "$(status | json '(d["run"] or {}).get("state", "")' 2>/dev/null)" = aborted ] && break; sleep 1; done
[ "$(status | json 'd["run"]["state"]')" = aborted ] || fail "step 4: the run is $(status | json 'd["run"]["state"]'), want aborted"
wait_unlocked
[ "$(status | json 'd["locked"]')" = False ] || fail "step 4: the lock was not released"
dind grep -q 'the Install file of 1.1.0' "$INSTALL_DIR/docker-compose.yml" || fail "step 4: the Install file changed"
pass 4

# ============ 5. a failed health check rolls back byte for byte ============
say "step 5: the new panel never answers (waits up to 180 s, then rolls back)"
for f in docker-compose.yml .env; do dind cat "$INSTALL_DIR/$f" >"$WORK/before-$f"; done
dind sh -c "ls $INSTALL_DIR" >"$WORK/before-ls"
publish 1.2.0 install-bad.yml
if install_sh --update --auto >"$WORK/update5.out" 2>&1; then
	cat "$WORK/update5.out" >&2
	fail "step 5: the update succeeded with a panel that cannot start"
fi
grep -q 'rolled-back' "$WORK/update5.out" || { cat "$WORK/update5.out" >&2; fail "step 5: the run did not end rolled-back"; }
for f in docker-compose.yml .env; do
	dind cat "$INSTALL_DIR/$f" | cmp -s - "$WORK/before-$f" || fail "step 5: $f differs from its bytes before the run"
done
[ "$(dind sh -c "ls $INSTALL_DIR")" = "$(cat "$WORK/before-ls")" ] || fail "step 5: the Install directory holds other files than before"
wait_unlocked
[ "$(status | json 'd["version"]')" = 1.1.0 ] || fail "step 5: the old panel is not back"
[ "$(panel_image)" = "$ID_B" ] || fail "step 5: the panel runs $(panel_image) after the rollback"
dind grep -q bogus "$INSTALL_DIR/docker-compose.yml" && fail "step 5: the broken Install file is still in place"
pass 5

# ============ 6. an update with the panel stopped ============
say "step 6: install.sh --update with relist-panel stopped"
publish 1.2.0 install-c.yml
dind docker stop relist-panel >/dev/null
install_sh --update --auto >"$WORK/update6.out" 2>&1 || { cat "$WORK/update6.out" >&2; fail "step 6: install.sh --update exited non-zero"; }
grep -q 'did not answer' "$WORK/update6.out" || { cat "$WORK/update6.out" >&2; fail "step 6: the log does not say the panel did not answer"; }
grep -q 'Update succeeded' "$WORK/update6.out" || { cat "$WORK/update6.out" >&2; fail "step 6: the update did not report success"; }
dind grep -q 'the Install file of 1.2.0' "$INSTALL_DIR/docker-compose.yml" || fail "step 6: the Install file was not replaced"
LAST_RUN=$(dind sh -c "ls -1 $DATA_DIR/.panel-update | grep -E '^[0-9]{8}T[0-9]{6}Z\$' | tail -n1")
dind test -s "$DATA_DIR/.panel-update/$LAST_RUN/panel.db" || fail "step 6: no database backup in run $LAST_RUN"
for _ in $(seq 30); do [ "$(api GET /healthz | code 2>/dev/null)" = 200 ] && break; sleep 2; done
[ "$(api GET /healthz | code)" = 200 ] || fail "step 6: the panel does not answer after the update"
pass 6

say "ALL PASSED: steps ${PASSED[*]}"
