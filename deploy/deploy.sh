#!/usr/bin/env bash
# Deploy (or update) a Pheme environment on the server.
#
#   deploy.sh <prod|dev>
#
# Lives at /opt/pheme/deploy.sh. Reads the shared compose file and the
# per-environment secrets in /opt/pheme/<env>/stack.env. If API_IMAGE and/or
# WEB_IMAGE are exported, they are written back into stack.env so the env file
# stays the single source of truth (used by CI/CD to pin the freshly built tag).
set -euo pipefail

ENV_NAME="${1:?usage: deploy.sh <prod|dev>}"
case "$ENV_NAME" in
  prod|dev) ;;
  *) echo "error: environment must be 'prod' or 'dev'" >&2; exit 1 ;;
esac

ROOT=/opt/pheme
COMPOSE="$ROOT/docker-compose.yml"
ENV_FILE="$ROOT/$ENV_NAME/stack.env"
PROJECT="pheme-$ENV_NAME"

[ -f "$COMPOSE" ]  || { echo "error: missing $COMPOSE" >&2; exit 1; }
[ -f "$ENV_FILE" ] || { echo "error: missing $ENV_FILE" >&2; exit 1; }

# The app mounts a nodelist file; a standalone (non-federated) host has none, so the
# compose default points at this placeholder, which the app never reads unless
# PHEME_NODELIST_PATH is also set. Create it if it does not exist yet.
[ -f "$ROOT/no-nodelist.json" ] || echo '{}' > "$ROOT/no-nodelist.json"

# Same trick for the APNs signing key, which only a host that rings iPhones has. Without the
# file, Docker would create a DIRECTORY at the bind-mount source and mount that — harmless while
# PHEME_APNS_KEY_FILE is unset, but confusing to find later. A host that does have a key points
# PHEME_APNS_KEY_HOST at it and this placeholder goes unused.
[ -f "$ROOT/no-apns-key.p8" ] || : > "$ROOT/no-apns-key.p8"

if [ -n "${API_IMAGE:-}" ]; then
  sed -i "s|^API_IMAGE=.*|API_IMAGE=${API_IMAGE}|" "$ENV_FILE"
fi
if [ -n "${WEB_IMAGE:-}" ]; then
  sed -i "s|^WEB_IMAGE=.*|WEB_IMAGE=${WEB_IMAGE}|" "$ENV_FILE"
fi

compose() {
  docker compose -p "$PROJECT" --env-file "$ENV_FILE" -f "$COMPOSE" "$@"
}

echo ">> Deploying $PROJECT"
compose pull
compose up -d --remove-orphans
docker image prune -f >/dev/null 2>&1 || true
compose ps
