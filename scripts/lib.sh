#!/usr/bin/env bash
# Shared helpers for the Pheme dev scripts. Source this; do not run directly.

set -euo pipefail

# Resolve the repository root regardless of where a script is invoked from.
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export REPO_ROOT

# Paths used across scripts.
ENV_DEV="$REPO_ROOT/.env.dev"            # service runtime env (sourced by bash)
COMPOSE_ENV="$REPO_ROOT/deploy/.env"     # docker compose env (KEY=value only)
COMPOSE_FILE="$REPO_ROOT/deploy/docker-compose.yml"
DEV_DIR="$REPO_ROOT/.dev"                # runtime artifacts (pids, logs, bins)
LOG_DIR="$DEV_DIR/logs"
BIN_DIR="$DEV_DIR/bin"
PID_DIR="$DEV_DIR/pids"

# --- Output helpers -------------------------------------------------------
if [ -t 1 ]; then
  C_RESET=$'\033[0m'; C_DIM=$'\033[2m'; C_BLUE=$'\033[34m'
  C_GREEN=$'\033[32m'; C_YELLOW=$'\033[33m'; C_RED=$'\033[31m'; C_BOLD=$'\033[1m'
else
  C_RESET=; C_DIM=; C_BLUE=; C_GREEN=; C_YELLOW=; C_RED=; C_BOLD=
fi

info()  { printf '%s▸%s %s\n' "$C_BLUE" "$C_RESET" "$*"; }
ok()    { printf '%s✓%s %s\n' "$C_GREEN" "$C_RESET" "$*"; }
warn()  { printf '%s!%s %s\n' "$C_YELLOW" "$C_RESET" "$*"; }
err()   { printf '%s✗%s %s\n' "$C_RED" "$C_RESET" "$*" >&2; }
die()   { err "$@"; exit 1; }

# --- Prerequisites --------------------------------------------------------
need() { command -v "$1" >/dev/null 2>&1 || die "missing required tool: $1"; }

docker_compose() {
  # Prefer the Compose v2 plugin; fall back to docker-compose.
  if docker compose version >/dev/null 2>&1; then
    docker compose --env-file "$COMPOSE_ENV" -f "$COMPOSE_FILE" "$@"
  elif command -v docker-compose >/dev/null 2>&1; then
    docker-compose --env-file "$COMPOSE_ENV" -f "$COMPOSE_FILE" "$@"
  else
    die "docker compose is not available"
  fi
}

# --- Ports ----------------------------------------------------------------
# port_in_use PORT -> 0 if something is listening on the TCP port.
port_in_use() {
  lsof -nP -iTCP:"$1" -sTCP:LISTEN >/dev/null 2>&1
}

# pick_port PREFERRED FALLBACK -> echoes a free port, preferring PREFERRED.
pick_port() {
  local preferred="$1" fallback="$2"
  if port_in_use "$preferred"; then
    echo "$fallback"
  else
    echo "$preferred"
  fi
}

# Load the generated service env into the current shell (exports all vars).
load_env_dev() {
  [ -f "$ENV_DEV" ] || die "missing $ENV_DEV — run: ./scripts/setup.sh"
  set -a
  # shellcheck disable=SC1090
  . "$ENV_DEV"
  set +a
}
