#!/bin/sh
# Relist panel install script.
#
#   curl -fsSL https://github.com/relist/relist-panel/releases/latest/download/install.sh | sh
#   curl -fsSL .../install.sh | sh -s -- --auto --domain panel.example.com --email me@example.com
#   curl -fsSL .../install.sh | sh -s -- --update
#
# The release workflow fills in VERSION and IMAGE (a digest reference), so this
# script installs the image released with it. The whole body is inside main(),
# called on the last line: a truncated download runs nothing.
set -eu

VERSION="@VERSION@"
IMAGE="@IMAGE@"

DEFAULT_DIR=/opt/relist-panel
DEFAULT_DATA_DIR=/var/lib/relist-panel
NETWORK=relist-network
DOMAIN_RE='^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+$'
DOCKER_GUIDE=https://docs.docker.com/engine/install/

usage() {
  cat <<USAGE
Usage: install.sh [options]

Installs the Relist panel and Traefik with Docker Compose, or updates an
existing install.

Options:
  --domain HOST        the panel's host name, like panel.example.com
  --email ADDRESS      the first admin's email
  --acme-email ADDRESS the email Let's Encrypt writes to (default: --email)
  --auto               ask nothing; needs --domain
  --update             update an existing install instead of installing
  --dir PATH           the install directory (default $DEFAULT_DIR)
  --help               print this help
USAGE
}

say() { printf '%s\n' "$*"; }
warn() { printf 'Warning: %s\n' "$*" >&2; }
die() {
  printf 'Error: %s\n' "$*" >&2
  exit 1
}
usage_error() {
  printf 'Error: %s\n\n' "$*" >&2
  usage >&2
  exit 2
}

valid_domain() {
  case $1 in
    *'
'*) return 1 ;;
  esac
  printf '%s\n' "$1" | grep -Eq "$DOMAIN_RE"
}

valid_email() {
  case $1 in
    *'
'* | *' '* | *'$'* | *'"'* | *"'"* | *'`'* | *'#'* | *'\'*) return 1 ;;
  esac
  printf '%s\n' "$1" | grep -Eq '^[^@]+@[^@]+$'
}

# have_tty is true when a terminal can be opened to ask on. stdin is the pipe
# from curl, so prompts read /dev/tty. Under cron, CI or `ssh host '... | sh'`
# there is none, and opening it fails.
have_tty() {
  ( : </dev/tty ) 2>/dev/null
}

# ask PROMPT [DEFAULT] sets REPLY from the terminal.
ask() {
  if [ -n "${2:-}" ]; then
    printf '%s [%s]: ' "$1" "$2" >&2
  else
    printf '%s: ' "$1" >&2
  fi
  REPLY=""
  IFS= read -r REPLY </dev/tty || REPLY=""
  if [ -z "$REPLY" ]; then
    REPLY=${2:-}
  fi
}

# confirm PROMPT is true on y or yes.
confirm() {
  printf '%s [y/N]: ' "$1" >&2
  ANSWER=""
  IFS= read -r ANSWER </dev/tty || ANSWER=""
  case $ANSWER in
    y | Y | yes | YES | Yes) return 0 ;;
  esac
  return 1
}

# env_value FILE KEY prints KEY's value from a .env file, if it has one.
env_value() {
  [ -f "$1" ] || return 0
  sed -n "s/^[[:space:]]*$2=//p" "$1" | tail -n 1 | sed 's/^"\(.*\)"$/\1/; s/^'"'"'\(.*\)'"'"'$/\1/'
}

parse_args() {
  AUTO=0
  UPDATE=0
  DIR=$DEFAULT_DIR
  DOMAIN=""
  EMAIL=""
  ACME_EMAIL=""
  ACME_SET=0
  while [ $# -gt 0 ]; do
    case $1 in
      --help | -h)
        usage
        exit 0
        ;;
      --auto) AUTO=1 ;;
      --update) UPDATE=1 ;;
      --domain | --email | --acme-email | --dir)
        [ $# -ge 2 ] || usage_error "$1 needs a value"
        case $1 in
          --domain) DOMAIN=$2 ;;
          --email) EMAIL=$2 ;;
          --acme-email)
            ACME_EMAIL=$2
            ACME_SET=1
            ;;
          --dir) DIR=$2 ;;
        esac
        shift
        ;;
      --domain=*) DOMAIN=${1#*=} ;;
      --email=*) EMAIL=${1#*=} ;;
      --acme-email=*)
        ACME_EMAIL=${1#*=}
        ACME_SET=1
        ;;
      --dir=*) DIR=${1#*=} ;;
      *) usage_error "unknown option: $1" ;;
    esac
    shift
  done
}

# validate_args refuses bad flags before anything is checked or written.
validate_args() {
  case $DIR in
    /*) ;;
    *) usage_error "--dir must be an absolute path" ;;
  esac
  if [ -n "$DOMAIN" ] && ! valid_domain "$DOMAIN"; then
    usage_error "Give the host name only, like panel.example.com"
  fi
  if [ -n "$EMAIL" ] && ! valid_email "$EMAIL"; then
    usage_error "--email is not an email address"
  fi
  if [ -n "$ACME_EMAIL" ] && ! valid_email "$ACME_EMAIL"; then
    usage_error "--acme-email is not an email address"
  fi
  if [ "$UPDATE" -eq 0 ] && [ "$AUTO" -eq 1 ] && [ -z "$DOMAIN" ]; then
    usage_error "--auto needs --domain"
  fi
  if [ "$UPDATE" -eq 0 ] && [ "$AUTO" -eq 0 ] && ! have_tty; then
    usage_error "No terminal to ask on; pass --auto with --domain and --email"
  fi
}

resolve_image() {
  IMAGE=${RELIST_INSTALL_IMAGE:-$IMAGE}
}

require_image() {
  case $IMAGE in
    @IMAGE@) die "This script has no image filled in. Download install.sh from a release." ;;
  esac
}

check_root() {
  if [ "$(id -u)" -ne 0 ]; then
    die "This needs root. Re-run it with sudo."
  fi
}

check_docker() {
  command -v docker >/dev/null 2>&1 || die "Docker is not installed. Install it first: $DOCKER_GUIDE"
  docker info >/dev/null 2>&1 || die "Docker does not answer. Start it, or run this as a user that can use it. Install guide: $DOCKER_GUIDE"
  COMPOSE_VERSION=$(docker compose version --short 2>/dev/null || true)
  COMPOSE_VERSION=${COMPOSE_VERSION#v}
  COMPOSE_MAJOR=${COMPOSE_VERSION%%.*}
  case $COMPOSE_MAJOR in
    '' | *[!0-9]*) die "Docker Compose v2 is missing (docker compose version fails). Install guide: $DOCKER_GUIDE" ;;
  esac
  [ "$COMPOSE_MAJOR" -ge 2 ] || die "Docker Compose v2 is needed, found $COMPOSE_VERSION. Install guide: $DOCKER_GUIDE"
}

check_arch() {
  case $(uname -m) in
    x86_64 | aarch64) ;;
    *) die "This machine is $(uname -m); the panel image is built for x86_64 and aarch64." ;;
  esac
}

check_no_install() {
  if [ -e "$DIR" ] && [ -n "$(ls -A "$DIR" 2>/dev/null)" ]; then
    die "$DIR is not empty. To update an existing install, run this script with --update."
  fi
  names=$(docker ps -a --format '{{.Names}}' 2>/dev/null || true)
  for name in relist-panel relist-traefik; do
    if printf '%s\n' "$names" | grep -qx "$name"; then
      die "A Container named $name exists, so there is an install on this host. To update it, run this script with --update."
    fi
  done
  if [ -n "$(docker ps -a -q --filter label=com.docker.compose.project=relist-panel 2>/dev/null || true)" ]; then
    die "A Compose project named relist-panel exists on this host. To update it, run this script with --update."
  fi
}

check_ports() {
  if ! command -v ss >/dev/null 2>&1; then
    warn "ss is not installed, so ports 80 and 443 were not checked."
    return 0
  fi
  listening=$(ss -ltn 2>/dev/null | awk 'NR > 1 {print $4}')
  for port in 80 443; do
    if printf '%s\n' "$listening" | grep -Eq "[:.]$port\$"; then
      die "Port $port is in use. Traefik needs ports 80 and 443; stop what listens there first."
    fi
  done
}

ask_domain() {
  if [ -n "$DOMAIN" ]; then
    return 0
  fi
  while :; do
    ask "Domain for the panel (like panel.example.com)"
    if valid_domain "$REPLY"; then
      DOMAIN=$REPLY
      return 0
    fi
    say "Give the host name only, like panel.example.com" >&2
  done
}

ask_email() {
  if [ -n "$EMAIL" ]; then
    return 0
  fi
  if [ "$AUTO" -eq 1 ]; then
    EMAIL=admin@relist.local
    return 0
  fi
  while :; do
    ask "Email for the first admin"
    if valid_email "$REPLY"; then
      EMAIL=$REPLY
      return 0
    fi
    say "That is not an email address." >&2
  done
}

choose_acme_email() {
  if [ "$ACME_SET" -eq 0 ]; then
    ACME_EMAIL=$EMAIL
  fi
  case $ACME_EMAIL in
    *.local) ACME_EMAIL="" ;;
  esac
}

check_dns() {
  resolved=""
  if command -v getent >/dev/null 2>&1; then
    resolved=$(getent hosts "$DOMAIN" 2>/dev/null | awk '{print $1}' || true)
  fi
  if [ -z "$resolved" ]; then
    warn "$DOMAIN does not resolve. Let's Encrypt cannot issue a certificate until it points at this host."
    return 0
  fi
  mine=""
  if command -v hostname >/dev/null 2>&1; then
    mine=$(hostname -I 2>/dev/null || true)
  fi
  found=0
  for addr in $resolved; do
    for own in $mine; do
      [ "$addr" = "$own" ] && found=1
    done
  done
  if [ -n "$mine" ] && [ "$found" -eq 0 ]; then
    warn "$DOMAIN resolves to $resolved, which is none of this host's addresses ($mine). Let's Encrypt cannot issue a certificate until it points here."
  fi
}

wait_healthy() {
  waited=0
  while [ "$waited" -lt 60 ]; do
    if command -v curl >/dev/null 2>&1; then
      curl -fsS -o /dev/null http://127.0.0.1:8888/healthz 2>/dev/null && return 0
    elif command -v wget >/dev/null 2>&1; then
      wget -q -O /dev/null http://127.0.0.1:8888/healthz 2>/dev/null && return 0
    else
      docker exec relist-panel wget -q -O /dev/null http://127.0.0.1:8080/healthz 2>/dev/null && return 0
    fi
    sleep 2
    waited=$((waited + 2))
  done
  return 1
}

# on_install_exit tells the operator what a failed install left behind.
on_install_exit() {
  status=$?
  if [ "$status" -ne 0 ] && [ "${WROTE:-0}" -eq 1 ]; then
    {
      echo
      echo "The install stopped part way. It created:"
      echo "  $DIR (the install files)"
      [ "${MADE_NETWORK:-0}" -eq 1 ] && echo "  the Docker network $NETWORK"
      echo "To remove it and start again:"
      echo "  (cd $DIR && docker compose down)"
      [ "${MADE_NETWORK:-0}" -eq 1 ] && echo "  docker network rm $NETWORK"
      echo "  rm -rf $DIR"
    } >&2
  fi
}

do_install() {
  check_root
  check_docker
  check_arch
  check_no_install
  check_ports
  ask_domain
  ask_email
  choose_acme_email
  check_dns
  say "Pulling $IMAGE"
  docker pull "$IMAGE" >/dev/null || die "Could not pull $IMAGE."

  WROTE=0
  MADE_NETWORK=0
  trap on_install_exit EXIT
  if ! docker network inspect "$NETWORK" >/dev/null 2>&1; then
    docker network create "$NETWORK" >/dev/null
    MADE_NETWORK=1
  fi
  mkdir -p "$DIR"
  WROTE=1
  docker run --rm -v "$DIR:$DIR" "$IMAGE" setup --dir "$DIR" --domain "$DOMAIN" --acme-email "$ACME_EMAIL" --image "$IMAGE"
  (cd "$DIR" && docker compose up -d)
  say "Waiting for the panel to answer"
  wait_healthy || die "The panel did not answer on http://127.0.0.1:8888/healthz within 60 seconds. See: docker logs relist-panel"

  if [ "$AUTO" -eq 1 ]; then
    docker exec relist-panel relist-panel create-admin --email "$EMAIL" --generate
  else
    docker exec -it relist-panel relist-panel create-admin --email "$EMAIL" </dev/tty
  fi

  HOST=$(hostname 2>/dev/null || echo this-host)
  echo
  echo "The panel is installed (version $VERSION)."
  echo "  Open:        https://$DOMAIN"
  echo "  If routing is not ready yet, reach it through an SSH tunnel:"
  echo "               ssh -L 8888:127.0.0.1:8888 $HOST"
  echo "               then open http://127.0.0.1:8888"
  echo "  Files:       $DIR/docker-compose.yml and $DIR/.env"
  echo "  To update:   curl -fsSL https://github.com/relist/relist-panel/releases/latest/download/install.sh | sudo sh -s -- --update"
}

on_update_interrupt() {
  echo >&2
  echo "Stopped following the log. The update keeps running in its Container." >&2
  echo "  To follow it again:  docker logs -f $UPDATER_NAME" >&2
  state=$(run_state)
  if [ "$state" = quiescing ]; then
    echo "  It is still waiting for the panel to go quiet. To cancel it:  docker stop $UPDATER_NAME" >&2
  fi
  exit 130
}

run_state() {
  sed -n 's/.*"state": *"\([a-z-]*\)".*/\1/p' "$DATA_DIR/.panel-update/$RUN_ID/status.json" 2>/dev/null | head -n 1
}

do_update() {
  check_root
  check_docker
  [ -f "$DIR/docker-compose.yml" ] || die "$DIR holds no install (no docker-compose.yml). To install, run this script without --update."
  DATA_DIR=$(env_value "$DIR/.env" RELIST_DATA_DIR)
  DATA_DIR=${DATA_DIR:-$DEFAULT_DATA_DIR}
  # The current image: the panel Container's, else the one .env names.
  CURRENT=$(docker inspect --format '{{.Image}}' relist-panel 2>/dev/null || true)
  if [ -z "$CURRENT" ]; then
    CURRENT=$(env_value "$DIR/.env" RELIST_PANEL_IMAGE)
  fi
  CURRENT=${CURRENT:-$IMAGE}
  # The update Container's mounts and environment, as positional parameters.
  set -- -v /var/run/docker.sock:/var/run/docker.sock -v "$DIR:$DIR" -v "$DATA_DIR:$DATA_DIR" -e "RELIST_DATA_DIR=$DATA_DIR"
  if [ -n "${RELIST_UPDATE_MANIFEST_URL:-}" ]; then
    set -- "$@" -e "RELIST_UPDATE_MANIFEST_URL=$RELIST_UPDATE_MANIFEST_URL"
  fi

  REPORT_FILE=$(mktemp)
  trap 'rm -f "$REPORT_FILE"' EXIT
  say "Checking for an update"
  code=0
  docker run --rm --network "$NETWORK" "$@" "$CURRENT" update --preflight --dir "$DIR" >"$REPORT_FILE" || code=$?
  cat "$REPORT_FILE"
  case $code in
    0) ;;
    3) exit 0 ;;
    *) exit "$code" ;;
  esac

  report_id=$(sed -n 's/^Report: //p' "$REPORT_FILE" | head -n 1)
  [ -n "$report_id" ] || die "The preflight printed no report id."
  steps=$(sed -n 's/^Manual step \[\(.*\)\]: .*/\1/p' "$REPORT_FILE")
  done_steps=""
  if [ -n "$steps" ]; then
    if [ "$AUTO" -eq 1 ] || ! have_tty; then
      echo "This update asks for steps only you can do, so it cannot run without a terminal:" >&2
      sed -n 's/^Manual step \[\(.*\)\]: \(.*\)/  - \2 (\1)/p' "$REPORT_FILE" >&2
      exit 1
    fi
    for id in $steps; do
      text=$(sed -n "s/^Manual step \\[$id\\]: //p" "$REPORT_FILE" | head -n 1)
      if ! confirm "Have you done this: $text"; then
        die "Do that first, then run the update again."
      fi
      done_steps=${done_steps:+$done_steps,}$id
    done
  elif [ "$AUTO" -eq 0 ] && have_tty; then
    confirm "Update now?" || die "Not updating."
  fi

  RUN_ID=$(date -u +%Y%m%dT%H%M%SZ)
  UPDATER_NAME=relist-panel-updater-$RUN_ID
  trap on_update_interrupt INT
  say "Starting the update (run $RUN_ID)"
  if [ -n "$done_steps" ]; then
    docker run -d --name "$UPDATER_NAME" --network "$NETWORK" "$@" "$CURRENT" update --report "$report_id" --run "$RUN_ID" --steps-done "$done_steps" --dir "$DIR" >/dev/null
  else
    docker run -d --name "$UPDATER_NAME" --network "$NETWORK" "$@" "$CURRENT" update --report "$report_id" --run "$RUN_ID" --dir "$DIR" >/dev/null
  fi
  docker logs -f "$UPDATER_NAME" 2>&1 || true
  trap - INT
  state=$(run_state)
  echo
  echo "Update ${state:-ended without a final state}."
  if [ "$state" != succeeded ]; then
    echo "Logs: docker logs $UPDATER_NAME" >&2
    exit 1
  fi
}

main() {
  parse_args "$@"
  validate_args
  resolve_image
  if [ "$UPDATE" -eq 1 ]; then
    do_update
  else
    require_image
    do_install
  fi
}

main "$@"
