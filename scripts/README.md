# Dev scripts

Helpers for running Pheme locally. All are driven by `make` targets from the
repo root, but can also be run directly.

| Script        | `make` target   | What it does |
|---------------|-----------------|--------------|
| `setup.sh`    | `make setup`    | One-time bootstrap: checks tools, picks free infra ports, generates VAPID keys, writes `.env.dev`, `deploy/.env` and `web/.env.local`. |
| `dev.sh`      | `make dev`      | Builds the Go binaries, brings up the infra, starts the App API, Ingest API, Dispatcher and the Vite dev server, and streams their combined logs. Ctrl-C stops the services. |
| `stop.sh`     | `make stop`     | Stops the dev services. Pass `--all` (`make stop ARGS=--all`) to also stop the infra. |
| `infra.sh`    | `make infra-*`  | Manage the Docker infra: `up`, `down`, `reset` (drops data volumes), `status`, `logs`. |
| `lib.sh`      | —               | Shared helpers; sourced by the others. |

## Typical use

```bash
make setup     # first time only
make dev       # day-to-day: everything up, logs streaming
# Ctrl-C to stop services; infra keeps running
make stop ARGS=--all   # stop services and infra
```

## Configuration

`setup.sh` writes two gitignored env files:

- **`.env.dev`** (repo root) — service runtime config, sourced by `dev.sh`.
  Edit it to change drivers, ports, the JWT secret, or `PHEME_ADMIN_EMAILS`
  (comma-separated emails granted the admin role on register/login).
- **`deploy/.env`** — credentials and host ports for Docker Compose.

Re-run `./scripts/setup.sh --force` to regenerate them (this rotates the VAPID
keys and JWT secret).

### Ports

`setup.sh` prefers the standard ports (Mongo 27017, RabbitMQ 5672/15672, Redis
6379) and automatically falls back to alternates (37017 / 35672 / 45672 / 36379)
when a standard port is already in use — so it coexists with other local
databases. The chosen ports are written to both env files and the service
connection strings stay in sync.

## Runtime artifacts

`dev.sh` writes to `.dev/` (gitignored):

- `.dev/bin/` — compiled service binaries
- `.dev/logs/` — per-service log files (`app.log`, `ingest.log`, …)
- `.dev/pids/` — pid files used by `stop.sh`

## Requirements

Go, Node.js + npm, Docker (with Compose), and `lsof`.
