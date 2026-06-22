#!/usr/bin/env bash
# Run the full Pheme dev stack: infra + API services + web dev server.
#
#   ./scripts/dev.sh
#
# Builds the Go binaries, ensures the infra is up, starts the App API, Ingest
# API, Dispatcher and the Vite dev server, and streams their combined logs.
# Press Ctrl-C to stop everything.

source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# Bootstrap on first run.
[ -f "$ENV_DEV" ] || "$REPO_ROOT/scripts/setup.sh"
load_env_dev

mkdir -p "$LOG_DIR" "$BIN_DIR" "$PID_DIR"

PIDS=()
cleanup() {
  echo
  info "Shutting down"
  for pidfile in "$PID_DIR"/*.pid; do
    [ -f "$pidfile" ] || continue
    local pid; pid="$(cat "$pidfile")"
    if kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
    fi
    rm -f "$pidfile"
  done
  ok "Stopped (infra still running — './scripts/infra.sh down' to stop it)"
}
trap cleanup INT TERM EXIT

# Ensure infra is running and matches the current config. `docker compose up -d`
# is idempotent: it's a no-op when nothing changed, and reconciles containers
# (e.g. port remaps) when the config differs from what's running.
"$REPO_ROOT/scripts/infra.sh" up

info "Building Go binaries"
( cd "$REPO_ROOT/api" && go build -o "$BIN_DIR/pheme-app" ./cmd/app \
  && go build -o "$BIN_DIR/pheme-ingest" ./cmd/ingest \
  && go build -o "$BIN_DIR/pheme-dispatcher" ./cmd/dispatcher )
ok "Built app, ingest, dispatcher"

# Ensure web deps are installed.
if [ ! -d "$REPO_ROOT/web/node_modules" ]; then
  info "Installing web dependencies"
  ( cd "$REPO_ROOT/web" && npm install )
fi

# start_service NAME CMD...
start_service() {
  local name="$1"; shift
  "$@" >"$LOG_DIR/$name.log" 2>&1 &
  local pid=$!
  echo "$pid" > "$PID_DIR/$name.pid"
  PIDS+=("$pid")
  ok "$name started (pid $pid) → $LOG_DIR/$name.log"
}

info "Starting services"
start_service app        "$BIN_DIR/pheme-app"
start_service ingest     "$BIN_DIR/pheme-ingest"
start_service dispatcher "$BIN_DIR/pheme-dispatcher"
start_service web        bash -c "cd '$REPO_ROOT/web' && exec npm run dev -- --host 127.0.0.1 --port ${WEB_PORT:-5173}"

sleep 2
echo
printf '%s%sPheme dev stack is up%s\n' "$C_BOLD" "$C_GREEN" "$C_RESET"
echo "  Web:        http://localhost:${WEB_PORT:-5173}"
echo "  App API:    http://localhost:${PHEME_APP_ADDR#:}/healthz"
echo "  Ingest API: http://localhost:${PHEME_INGEST_ADDR#:}/healthz"
echo "  Rabbit UI:  http://localhost:${RABBIT_MGMT_HOST_PORT:-15672} (guest/guest)"
echo "  Logs:       $LOG_DIR"
printf '%sStreaming logs — press Ctrl-C to stop.%s\n\n' "$C_DIM" "$C_RESET"

# Stream all service logs until interrupted.
tail -n 2 -F "$LOG_DIR"/app.log "$LOG_DIR"/ingest.log \
  "$LOG_DIR"/dispatcher.log "$LOG_DIR"/web.log &
echo "$!" > "$PID_DIR/tail.pid"

# Wait on the service processes; Ctrl-C triggers the cleanup trap.
wait "${PIDS[@]}"
