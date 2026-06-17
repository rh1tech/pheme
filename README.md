# Pheme

Pheme is a notification relay service. Websites trigger a channel by its public
ID via a simple HTTP API; all devices subscribed to that channel receive an
instant push notification. Every message is persisted server-side, so history is
never lost and can be browsed or exported.

## Why
Sending reliable notifications from a website to specific people usually relies
on email or chat webhooks, which are fragile and easy to miss. Pheme gives you a
durable, push-first channel: trigger once, deliver everywhere (iOS, Android,
browser), and keep a full history.

## Monorepo layout
```
pheme/
├── api/        Go services — Ingest API, App API, Dispatcher worker (one module, 3 binaries)
├── web/        Vite + TypeScript + Mantine SPA (+ Web Push service worker)
├── mobile/     Flutter app (Firebase Cloud Messaging)
├── deploy/     docker-compose stack (Mongo, RabbitMQ, Redis) + k8s (later)
└── docs/       Architecture and design docs
```

## Stack
| Layer            | Technology                                  |
|------------------|---------------------------------------------|
| API & workers    | Go                                          |
| Web frontend     | TypeScript · Vite · Mantine                 |
| Mobile           | Flutter · firebase_messaging                |
| Database         | MongoDB                                     |
| Message broker   | RabbitMQ (durable queue + DLQ)              |
| Cache / realtime | Redis (rate-limit, pub/sub, idempotency)    |
| Push delivery    | Firebase Cloud Messaging + Web Push (VAPID) |

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the full design.

## Quick start (local infra)
```bash
cp deploy/.env.example deploy/.env
docker compose -f deploy/docker-compose.yml up -d   # Mongo, RabbitMQ, Redis

cd api
go run ./cmd/app         # App API   :8080
go run ./cmd/ingest      # Ingest API :8081
go run ./cmd/dispatcher  # Worker
```

## Status
Early scaffolding. The Go skeleton compiles and exposes health endpoints; domain
logic is being filled in per the phased roadmap in the architecture doc.
