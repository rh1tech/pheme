#!/usr/bin/env bash
# Stop dev services started by dev.sh, and optionally the infrastructure.
#
#   ./scripts/stop.sh           # stop API services + web
#   ./scripts/stop.sh --all     # also stop the docker infra

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

info "Stopping dev services"
stopped=0
if [ -d "$PID_DIR" ]; then
  for pidfile in "$PID_DIR"/*.pid; do
    [ -f "$pidfile" ] || continue
    pid="$(cat "$pidfile")"
    name="$(basename "$pidfile" .pid)"
    if kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null && ok "stopped $name (pid $pid)" && stopped=$((stopped + 1))
    fi
    rm -f "$pidfile"
  done
fi
[ "$stopped" -eq 0 ] && warn "no running dev services found"

if [ "${1:-}" = "--all" ]; then
  "$REPO_ROOT/scripts/infra.sh" down
fi
