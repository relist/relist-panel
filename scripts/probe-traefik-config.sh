#!/usr/bin/env bash
# Probe the move of Traefik's static config into a generated file (ADR-0030),
# as `task probe:traefik-config`: a Panel update from the real v0.0.2 code to
# this checkout, and a fresh install of this checkout.
#
# Like probe-panel-update.sh, everything runs inside one docker:dind host, so
# nothing here touches the daemon this script runs on.
#
# Image a is built from `git archive v0.0.2` (VERSION 1.0.0), image b from this
# checkout (VERSION 1.1.0), both with a throwaway release key. The update is the
# 0.0.2 Updater writing b's Install file, which it has never seen.
#
# The steps, each printed as PASS or FAIL:
#   1. a's install.sh --auto installs 1.0.0, whose Traefik runs the old flags
#      with the Access log on;
#   2. install.sh --update to 1.1.0 succeeds: relist-traefik-config exited 0,
#      the Traefik override has the Access log from the old flags, Traefik runs
#      only --configfile, and the panel answers /healthz through Traefik;
#   3. on a clean dind host, b's install.sh --auto installs 1.1.0 with the
#      Access log off and the panel answering through Traefik.
#
# It needs a Docker that can run privileged containers, internet access for
# registry:2, busybox and traefik (unless the host has them), and the v0.0.2
# tag in this repository. KEEP=1 leaves the dind host running.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
DIND=tc-test-dind
IMG=tc-test/panel
CTX_A=$(mktemp -d -t tc-test-ctx-a.XXXXXX)
CTX_B=$(mktemp -d -t tc-test-ctx-b.XXXXXX)
WORK=$(mktemp -d -t tc-test-work.XXXXXX)
INSTALL_DIR=/opt/relist-panel
DATA_DIR=/var/lib/relist-panel
STATIC=$DATA_DIR/.traefik/static
FROM_REF=${FROM_REF:-v0.0.2}

say() { printf '\033[1mprobe:\033[0m %s\n' "$*" >&2; }
PASSED=()
pass() { PASSED+=("$1"); printf '\033[32mprobe: PASS\033[0m step %s\n' "$1" >&2; }
fail() { printf '\033[31mprobe: FAIL\033[0m %s\n' "$*" >&2; exit 1; }
summary() {
	local status=$?
	if [ $status -ne 0 ]; then
		printf '\033[31mprobe: FAILED\033[0m (passed steps: %s)\n' "${PASSED[*]:-none}" >&2
		docker exec "$DIND" sh -c "for f in $STATIC/traefik.override.yml $STATIC/apply.json; do echo \"== \$f\"; cat \$f; done" >&2 2>/dev/null || true
		docker exec "$DIND" sh -c 'for c in relist-traefik-config relist-traefik $(docker ps -aq --filter name=relist-panel-updater-); do echo "== $c"; docker logs --tail 30 $c 2>&1; done' >&2 2>/dev/null || true
	fi
	cleanup
	exit $status
}
cleanup() {
	[ "${KEEP:-}" = 1 ] && return 0
	docker rm -fv "$DIND" >/dev/null 2>&1 || true
	docker rmi -f "$IMG:a" "$IMG:b" >/dev/null 2>&1 || true
	rm -rf "$CTX_A" "$CTX_B" "$WORK"
}
trap summary EXIT

dind() { docker exec -i "$DIND" "$@"; }
put() { docker exec -i "$DIND" sh -c "cat > $1"; }
json() { python3 -c "import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1], {'d': d}))" "$1"; }

# install_sh SCRIPT IMAGE ARGS...: an Install script piped into sh like `curl | sh`.
install_sh() {
	local script=$1 image=$2
	shift 2
	docker exec -i -e "RELIST_INSTALL_IMAGE=$image" -e RELIST_UPDATE_MANIFEST_URL=http://tc-manifest/update-manifest.json \
		"$DIND" sh -s -- "$@" <"$script"
}

# through_traefik: the panel's /healthz through Traefik, as a browser reaches it.
through_traefik() {
	docker run --rm --network "container:$DIND" curlimages/curl:8.11.1 -sk -o /dev/null -w '%{http_code}' \
		--resolve panel.localhost:443:127.0.0.1 https://panel.localhost/healthz || true
}
wait_through_traefik() {
	for _ in $(seq 60); do [ "$(through_traefik)" = 200 ] && return 0; sleep 2; done
	fail "$1: the panel does not answer /healthz through Traefik"
}

command -v docker >/dev/null || fail "docker is not installed"
git -C "$ROOT" rev-parse -q --verify "$FROM_REF^{commit}" >/dev/null || fail "no $FROM_REF in this repository"

say "making the key and building $IMG:a ($FROM_REF as 1.0.0) and $IMG:b (this checkout as 1.1.0)"
openssl genpkey -algorithm ed25519 -out "$WORK/key.pem" 2>/dev/null
KEY=$(openssl pkey -in "$WORK/key.pem" -pubout -outform DER | tail -c 32 | base64 -w0)
git -C "$ROOT" archive "$FROM_REF" | tar -C "$CTX_A" -xf -
tar -C "$ROOT" --exclude=.git --exclude=node_modules --exclude=.scratch --exclude=.superpowers --exclude=.claude -cf - . | tar -C "$CTX_B" -xf -
docker build -q --build-arg VERSION=1.0.0 --build-arg "RELEASE_KEY=$KEY" -t "$IMG:a" "$CTX_A" >/dev/null
docker build -q --build-arg VERSION=1.1.0 --build-arg "RELEASE_KEY=$KEY" -t "$IMG:b" "$CTX_B" >/dev/null

start_dind() {
	docker rm -fv "$DIND" >/dev/null 2>&1 || true
	docker run -d --privileged --name "$DIND" -e DOCKER_TLS_CERTDIR= docker:27-dind >/dev/null
	for _ in $(seq 60); do dind docker info >/dev/null 2>&1 && break; sleep 1; done
	dind docker info >/dev/null 2>&1 || fail "the dind host did not start"
	for image in traefik:v3.7 busybox:1.37 registry:2; do
		if docker image inspect "$image" >/dev/null 2>&1; then
			docker save "$image" | dind docker load -q >/dev/null
		else
			dind docker pull -q "$image" >/dev/null
		fi
	done
	docker save "$IMG:a" | dind docker load -q >/dev/null
	docker save "$IMG:b" | dind docker load -q >/dev/null
	dind docker run -d --name tc-registry -p 5000:5000 registry:2 >/dev/null
	for _ in $(seq 30); do dind wget -qO- http://127.0.0.1:5000/v2/ >/dev/null 2>&1 && break; sleep 1; done
	for t in a b; do
		dind docker tag "$IMG:$t" "localhost:5000/$IMG:$t"
		dind docker push -q "localhost:5000/$IMG:$t" >/dev/null
	done
	REF_A=$(dind docker inspect -f '{{index .RepoDigests 0}}' "localhost:5000/$IMG:a")
	REF_B=$(dind docker inspect -f '{{index .RepoDigests 0}}' "localhost:5000/$IMG:b")
	dind docker rmi "$IMG:a" "$IMG:b" "localhost:5000/$IMG:a" "localhost:5000/$IMG:b" >/dev/null
	dind docker network create relist-network >/dev/null
	dind mkdir -p /srv/manifest
	dind docker run -d --name tc-manifest --network relist-network -v /srv/manifest:/www busybox:1.37 httpd -f -p 80 -h /www >/dev/null
}

# publish: sign and serve a 1.1.0 manifest with this checkout's Install file
# and its real invariants, the image pinned to b's digest.
publish() {
	python3 - "$ROOT/docker-compose.yml" "$ROOT/release/invariants.json" "$REF_B" "$WORK/manifest.json" <<'PY'
import json, sys
install, invariants, ref, out = sys.argv[1:]
invs = json.load(open(invariants))
for inv in invs:
    if inv["kind"] == "service-image":
        inv["image"] = ref
manifest = {
    "schema": 1, "version": "1.1.0", "released_at": "2026-11-20T09:00:00Z", "image": ref,
    "notes_url": "https://example.test/notes",
    "install_file": open(install).read(),
    "invariants": invs,
    "manual_steps": [],
}
open(out, "w").write(json.dumps(manifest))
PY
	openssl pkeyutl -sign -inkey "$WORK/key.pem" -rawin -in "$WORK/manifest.json" -out "$WORK/manifest.json.sig"
	put /srv/manifest/update-manifest.json <"$WORK/manifest.json"
	put /srv/manifest/update-manifest.json.sig <"$WORK/manifest.json.sig"
}

traefik_args() { dind docker inspect -f '{{join .Args " "}}' relist-traefik; }

# ============ 1. install 1.0.0 from v0.0.2 ============
start_dind
say "step 1: $FROM_REF's install.sh --auto"
install_sh "$CTX_A/release/install.sh" "$REF_A" --auto --domain panel.localhost --acme-email ops@example.com >"$WORK/install1.out" 2>&1 ||
	{ cat "$WORK/install1.out" >&2; fail "step 1: install.sh exited non-zero"; }
case "$(traefik_args)" in *--accesslog=true*) ;; *) fail "step 1: Traefik runs without the Access log: $(traefik_args)" ;; esac
wait_through_traefik "step 1"
pass 1

# ============ 2. update to this checkout ============
say "step 2: install.sh --update to 1.1.0 with the 0.0.2 Updater"
publish
install_sh "$ROOT/release/install.sh" "$REF_A" --update --auto >"$WORK/update2.out" 2>&1 ||
	{ cat "$WORK/update2.out" >&2; fail "step 2: install.sh --update exited non-zero"; }
grep -q 'Update succeeded' "$WORK/update2.out" || { cat "$WORK/update2.out" >&2; fail "step 2: the update did not report success"; }
[ "$(dind docker inspect -f '{{.State.ExitCode}}' relist-traefik-config)" = 0 ] || fail "step 2: relist-traefik-config did not exit 0"
dind grep -q accessLog "$STATIC/traefik.override.yml" || fail "step 2: the Traefik override lost the Access log"
dind grep -q ops@example.com "$STATIC/traefik.override.yml" || fail "step 2: the Traefik override lost the ACME email"
[ "$(traefik_args)" = "--configfile=/etc/traefik/traefik.yml" ] || fail "step 2: Traefik runs $(traefik_args)"
wait_through_traefik "step 2"
pass 2

# ============ 3. a fresh install of this checkout ============
say "step 3: a fresh install of 1.1.0 on a clean dind host"
start_dind
install_sh "$ROOT/release/install.sh" "$REF_B" --auto --domain panel.localhost >"$WORK/install3.out" 2>&1 ||
	{ cat "$WORK/install3.out" >&2; fail "step 3: install.sh exited non-zero"; }
dind test -f "$STATIC/traefik.yml" || fail "step 3: no generated traefik.yml"
dind grep -q accessLog "$STATIC/traefik.yml" && fail "step 3: a fresh install has the Access log on"
[ "$(traefik_args)" = "--configfile=/etc/traefik/traefik.yml" ] || fail "step 3: Traefik runs $(traefik_args)"
wait_through_traefik "step 3"
pass 3

say "probe passed: steps ${PASSED[*]}"
