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
# Run with real backends (see deploy/.env.example for all toggles):
export PHEME_STORE_DRIVER=mongo PHEME_BROKER_DRIVER=rabbit \
       PHEME_LIVE_DRIVER=redis PHEME_RATELIMIT_DRIVER=redis \
       PHEME_MONGO_URI='mongodb://pheme:pheme@localhost:27017/?authSource=admin'
go run ./cmd/app         # App API    :8080  (auth, channels, history, SSE)
go run ./cmd/ingest      # Ingest API :8081  (public trigger, API-key auth)
go run ./cmd/dispatcher  # Worker      (consume → persist → push → live event)
```
Omit the env vars to run with zero-dependency in-memory backends.

## Status
Phase 1 (scaffold) and phase 2 (infrastructure) complete: JWT auth (Argon2id),
MongoDB persistence, RabbitMQ broker with DLQ, Redis rate limiting and live
pub/sub, and FCM + Web Push senders — all selectable per environment and
verified end-to-end against the docker-compose stack. Web and mobile clients are
the next phases.
