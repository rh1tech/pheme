#!/usr/bin/env bash
# Manage the local infrastructure containers (MongoDB, RabbitMQ, Redis).
#
#   ./scripts/infra.sh up       # start and wait until healthy
#   ./scripts/infra.sh down     # stop and remove containers (keep data)
#   ./scripts/infra.sh reset    # stop and remove containers AND volumes
#   ./scripts/infra.sh status   # show container status
#   ./scripts/infra.sh logs     # follow container logs

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

[ -f "$COMPOSE_ENV" ] || die "missing $COMPOSE_ENV — run: ./scripts/setup.sh"
need docker

cmd="${1:-up}"

wait_healthy() {
  info "Waiting for containers to become healthy"
  local tries=60
  while [ "$tries" -gt 0 ]; do
    local unhealthy
    unhealthy="$(docker_compose ps --format '{{.Name}} {{.Health}}' 2>/dev/null \
      | awk '$2 != "" && $2 != "healthy" {print $1}')"
    if [ -z "$unhealthy" ]; then
      ok "All containers healthy"
      return 0
    fi
    sleep 2
    tries=$((tries - 1))
  done
  warn "Timed out waiting for health; check: ./scripts/infra.sh status"
  return 1
}

case "$cmd" in
  up)
    info "Starting infrastructure"
    docker_compose up -d
    wait_healthy || true
    docker_compose ps --format 'table {{.Name}}\t{{.Status}}\t{{.Ports}}'
    ;;
  down)
    info "Stopping infrastructure (keeping data volumes)"
    docker_compose down
    ok "Stopped"
    ;;
  reset)
    warn "Removing containers AND data volumes"
    docker_compose down -v
    ok "Reset"
    ;;
  status|ps)
    docker_compose ps --format 'table {{.Name}}\t{{.Status}}\t{{.Ports}}'
    ;;
  logs)
    docker_compose logs -f
    ;;
  *)
    die "unknown command: $cmd (use up|down|reset|status|logs)"
    ;;
esac
