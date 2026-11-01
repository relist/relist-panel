#!/usr/bin/env bash
# Probe a Panel update against real Docker (ADR-0024), as `task probe:panel-update`.
#
# Everything runs inside one docker:dind host, a separate Docker daemon, so
# nothing here touches the daemon this script runs on: a second panel with the
# Install file's Traefik labels would be picked up by a live Traefik, which owns
# ports 80/443 and the shared relist-network on the host.
#
# Inside the dind host:
#   - a registry:2 on localhost:5000, because the preflight pulls the manifest's
#     image by digest and a locally built image has no digest;
#   - the install stack (panel + Traefik) from a sandbox Install file, with the
#     panel at version 1.0.0 (su-test/panel:a);
#   - a manifest server (busybox httpd) on relist-network, which the panel reads
#     through RELIST_UPDATE_MANIFEST_URL.
# The manifests are signed with a throwaway key, built into both panel images.
#
# Two updates are run through the panel's own API:
#   1. 1.0.0 -> 1.1.0 (su-test/panel:b), which must succeed;
#   2. 1.1.0 -> 1.2.0, the same image with `command: ["--bogus"]` in the new
#      Install file, so the panel never answers; it must roll back.
#
# It needs a Docker that can run privileged containers, internet access for
# registry:2 and busybox (unless the host has them), and a few GB of disk. It
# removes the dind host, the network, and the two images it built on exit;
# KEEP=1 leaves the dind host running for debugging.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
DIND=su-test-update-dind
IMG=su-test/panel
CTX=$(mktemp -d -t su-test-ctx.XXXXXX)
WORK=$(mktemp -d -t su-test-work.XXXXXX)
HEALTH_DEADLINE=${HEALTH_DEADLINE:-420}

say() { printf '\033[1mprobe:\033[0m %s\n' "$*" >&2; }
fail() { printf '\033[31mprobe: FAIL\033[0m %s\n' "$*" >&2; exit 1; }

cleanup() {
	docker rm -fv "$DIND" >/dev/null 2>&1 || true
	docker rmi -f "$IMG:a" "$IMG:b" >/dev/null 2>&1 || true
	rm -rf "$CTX" "$WORK"
}
[ "${KEEP:-}" = 1 ] || trap cleanup EXIT

# dind runs a command in the dind host.
dind() { docker exec -i "$DIND" "$@"; }
# put DEST: stdin becomes a file in the dind host.
put() { docker exec -i "$DIND" sh -c "cat > $1"; }
# panel prints the ID of the running panel Container in the dind host, found by label
# (its name is the project's, not fixed).
panel() { dind docker ps -q --filter label=com.docker.compose.project=su-test-update --filter label=com.docker.compose.service=panel </dev/null; }

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

# ---- the throwaway release key and the two Install files ----
say "making the key and the Install files"
openssl genpkey -algorithm ed25519 -out "$WORK/key.pem" 2>/dev/null
KEY=$(openssl pkey -in "$WORK/key.pem" -pubout -outform DER | tail -c 32 | base64 -w0)
python3 - "$ROOT/docker-compose.yml" "$WORK" <<'PY'
import re, sys
src = open(sys.argv[1]).read()
head = re.compile(r"    build: \.\n(?:    #.*\n)*    image: ghcr\.io/relist/relist-panel:local\n    pull_policy: build\n")
if not head.search(src):
    sys.exit("the panel service of docker-compose.yml has changed shape: update the probe")
a = head.sub("    image: ${RELIST_PANEL_IMAGE:-su-test/panel:a}\n", src)
# Version 1.1.0's file differs from 1.0.0's, so the update has something to write.
b = "# su-test: the Install file of 1.1.0\n" + a
bad = b.replace("# su-test: the Install file of 1.1.0", "# su-test: the Install file of 1.2.0 (broken)").replace(
    "    image: ${RELIST_PANEL_IMAGE:-su-test/panel:a}\n", "    image: ${RELIST_PANEL_IMAGE:-su-test/panel:a}\n    command: [\"--bogus\"]\n", 1)
for name, text in (("install-a.yml", a), ("install-b.yml", b), ("install-bad.yml", bad)):
    open(f"{sys.argv[2]}/{name}", "w").write(text)
PY

# ---- the two panel images, built from a copy of this checkout whose
# docker-compose.yml is the sandbox Install file the image embeds ----
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

say "pushing $IMG:b to a registry in the dind host"
dind docker run -d --name su-registry -p 5000:5000 registry:2 >/dev/null
for _ in $(seq 30); do dind wget -qO- http://127.0.0.1:5000/v2/ >/dev/null 2>&1 && break; sleep 1; done
dind docker tag "$IMG:b" "localhost:5000/$IMG:b"
dind docker push -q "localhost:5000/$IMG:b" >/dev/null
REF_B=$(dind docker inspect -f '{{index .RepoDigests 0}}' "localhost:5000/$IMG:b")
ID_B=$(dind docker inspect -f '{{.Id}}' "$IMG:b")
case "$REF_B" in localhost:5000/*@sha256:*) ;; *) fail "no digest reference for b: $REF_B" ;; esac
# Only the registry holds b from here on, so the preflight's pull is real.
dind docker rmi "$IMG:b" "localhost:5000/$IMG:b" >/dev/null
say "b is $REF_B (image $ID_B)"

# ---- the Install ----
say "starting the install stack at 1.0.0"
dind mkdir -p /install /srv/manifest /srv/su-data
put /install/docker-compose.yml <"$WORK/install-a.yml"
put /install/.env <<'ENV'
RELIST_DOMAIN=panel.test
RELIST_DATA_DIR=/srv/su-data
ENV
put /install/docker-compose.override.yml <<'YML'
services:
  panel:
    environment:
      RELIST_UPDATE_MANIFEST_URL: http://su-manifest/update-manifest.json
YML
dind docker network create relist-network >/dev/null
dind docker run -d --name su-manifest --network relist-network -v /srv/manifest:/www busybox:1.37 httpd -f -p 80 -h /www >/dev/null
dind sh -c 'cd /install && docker compose -p su-test-update up -d --no-build' >/dev/null 2>&1 || fail "docker compose up"

for _ in $(seq 60); do [ "$(api GET /healthz | code)" = 200 ] && break; sleep 1; done
[ "$(api GET /healthz | code)" = 200 ] || fail "the panel does not answer"
PANEL_ID=$(panel)
printf '%s\n' 'probe password 123' | dind docker exec -i "$PANEL_ID" relist-panel create-admin --email probe@example.com --password-stdin >/dev/null || fail "create-admin"
[ "$(api POST /api/auth/login '{"email":"probe@example.com","password":"probe password 123"}' | code)" = 200 ] || fail "login"
VERSION_NOW=$(api GET /api/panel-update/status | body | json 'd["version"]')
[ "$VERSION_NOW" = 1.0.0 ] || fail "running version $VERSION_NOW, want 1.0.0"

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
    "manual_steps": [{"id": "open-443", "text": "Open port 443."}],
}
open(out, "w").write(json.dumps(manifest))
PY
	openssl pkeyutl -sign -inkey "$WORK/key.pem" -rawin -in "$WORK/manifest.json" -out "$WORK/manifest.json.sig"
	put /srv/manifest/update-manifest.json <"$WORK/manifest.json"
	put /srv/manifest/update-manifest.json.sig <"$WORK/manifest.json.sig"
}

# update VERSION: check, preview, apply, and wait for the run to end. Prints
# the final run as JSON.
update() {
	local want=$1 out report run state=""
	[ "$(api POST /api/panel-update/check | code)" = 200 ] || fail "check"
	out=$(api GET /api/panel-update/status)
	[ "$(echo "$out" | body | json 'd["available"]["version"]')" = "$want" ] || fail "available version is not $want: $(echo "$out" | body)"
	out=$(api POST /api/panel-update/preflight)
	[ "$(echo "$out" | code)" = 200 ] || fail "preflight: $(echo "$out" | body)"
	echo "$out" | body | json '"probe: preflight: ready=%s change=%s recreated=%s traefikRecreated=%s failed=%s invariants=%s" % (d["ready"], d["change"], d["recreated"], d["traefikRecreated"], d["failed"], [(i["kind"], i["ok"]) for i in d["invariants"]])' >&2
	[ "$(echo "$out" | body | json 'd["ready"]')" = True ] || fail "the preflight is not ready: $(echo "$out" | body)"
	report=$(echo "$out" | body | json 'd["id"]')
	out=$(api POST /api/panel-update/apply "{\"report_id\":\"$report\",\"manual_steps_done\":[\"open-443\"]}")
	[ "$(echo "$out" | code)" = 202 ] || fail "apply: $(echo "$out" | body)"
	run=$(echo "$out" | body | json 'd["id"]')
	say "run $run started"
	local deadline=$((SECONDS + HEALTH_DEADLINE)) last=""
	while [ $SECONDS -lt $deadline ]; do
		out=$(api GET /api/panel-update/status 2>/dev/null || true)
		if [ "$(echo "$out" | code)" = 200 ]; then
			state=$(echo "$out" | body | json '(d["run"] or {}).get("state", "")')
			[ "$state" != "$last" ] && say "  state: $state" && last=$state
			case "$state" in succeeded | rolled-back | rollback-failed | interrupted)
				echo "$out" | body
				return 0
				;;
			esac
		else
			[ "$last" != restarting ] && say "  the panel does not answer (restarting)" && last=restarting
		fi
		sleep 2
	done
	fail "run $run did not end within ${HEALTH_DEADLINE}s (last state: $state)"
}
panel_image() { dind docker ps --filter label=com.docker.compose.project=su-test-update --filter label=com.docker.compose.service=panel --format '{{.ID}}' | xargs -r -n1 docker exec "$DIND" docker inspect -f '{{.Image}}'; }

# ---- 1. 1.0.0 -> 1.1.0 ----
say "update 1: 1.0.0 -> 1.1.0 (must succeed)"
publish 1.1.0 install-b.yml
RUN1=$(update 1.1.0)
echo "$RUN1" | json 'd["run"]'
[ "$(echo "$RUN1" | json 'd["run"]["state"]')" = succeeded ] || fail "update 1 did not succeed: $(echo "$RUN1" | json 'd["run"]')"
# The page's success test: the version from the status is the target. The
# watcher in the new panel settles the run and releases the lock.
for _ in $(seq 30); do
	[ "$(api GET /api/panel-update/status | body | json 'd["version"]')" = 1.1.0 ] && [ "$(api GET /api/panel-update/status | body | json 'd["locked"]')" = False ] && break
	sleep 2
done
[ "$(api GET /api/panel-update/status | body | json 'd["version"]')" = 1.1.0 ] || fail "the panel did not come up at 1.1.0"
[ "$(api GET /api/panel-update/status | body | json 'd["locked"]')" = False ] || fail "the update lock is still on"
[ "$(panel_image)" = "$ID_B" ] || fail "the panel runs $(panel_image), want $ID_B"
dind grep -q "^RELIST_PANEL_IMAGE=$REF_B\$" /install/.env || fail ".env does not pin b"
dind grep -q 'the Install file of 1.1.0' /install/docker-compose.yml || fail "the Install file was not replaced"
[ "$(dind docker ps -aq --filter name=relist-panel-updater- | wc -l)" = 0 ] || fail "the exited Updater was not removed"
[ "$(api GET /api/auth/status | code)" = 200 ] || fail "the session did not survive"
say "update 1 succeeded: running $ID_B, .env pinned, Install file replaced, Updater removed"
# The previous panel image is owned by Panel update, so a prune plan skips it.
ID_A=$(dind docker inspect -f '{{.Id}}' "$IMG:a")

# ---- 2. 1.1.0 -> 1.2.0 with a panel that never answers ----
say "update 2: 1.1.0 -> 1.2.0, forced to fail health (must roll back; waits up to 180 s)"
INSTALL_BEFORE=$(dind cat /install/docker-compose.yml | sha256sum)
ENV_BEFORE=$(dind cat /install/.env | sha256sum)
publish 1.2.0 install-bad.yml
RUN2=$(update 1.2.0)
echo "$RUN2" | json 'd["run"]'
[ "$(echo "$RUN2" | json 'd["run"]["state"]')" = rolled-back ] || fail "update 2 did not roll back: $(echo "$RUN2" | json 'd["run"]')"
for _ in $(seq 30); do [ "$(api GET /api/panel-update/status | body | json 'd["locked"]' 2>/dev/null)" = False ] && break; sleep 2; done
[ "$(api GET /api/panel-update/status | body | json 'd["version"]')" = 1.1.0 ] || fail "the old panel is not back"
[ "$(api GET /api/panel-update/status | body | json 'd["locked"]')" = False ] || fail "the update lock is still on after the rollback"
[ "$(panel_image)" = "$ID_B" ] || fail "the panel runs $(panel_image) after the rollback, want $ID_B"
[ "$(dind cat /install/docker-compose.yml | sha256sum)" = "$INSTALL_BEFORE" ] || fail "the Install file was not restored"
[ "$(dind cat /install/.env | sha256sum)" = "$ENV_BEFORE" ] || fail ".env was not restored"
dind grep -q bogus /install/docker-compose.yml && fail "the broken Install file is still in place"
[ "$(dind docker ps -aq --filter name=relist-panel-updater- | wc -l)" = 0 ] || fail "the exited Updater was not removed"
say "update 2 rolled back: running $ID_B again, Install file and .env restored, Updater removed"

# ---- prune ownership ----
# The image 1.0.0 ran from is unused now; a prune plan names it as the Panel
# update's, and skips it.
PLAN=$(api GET '/api/docker/prune-plan?kind=unused-images')
[ "$(echo "$PLAN" | code)" = 200 ] || fail "prune plan: $(echo "$PLAN" | body)"
echo "$PLAN" | body | json '"prune plan: skip=%s delete=%s" % (d["skip"], [e["name"] for e in d["delete"]])'
echo "$PLAN" | body | json '[s for s in d["skip"] if s.get("compose") == "Panel update"][0]["name"]' >/dev/null || fail "the previous panel image is not skipped as Panel update's"
say "ALL PASSED (the previous image was $ID_A)"
