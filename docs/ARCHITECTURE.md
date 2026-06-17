# Pheme Architecture

## 1. Overview
Pheme delivers website-originated notifications to users' devices in real time and
stores every message durably.

A **website** triggers a **Channel** (identified by a public `channelId` and
authenticated with a secret API key). The trigger is enqueued, persisted, and
fanned out to all **Devices** subscribed to that channel via push (FCM / Web
Push) and to any open web clients via WebSocket.

## 2. Components

| Service        | Binary            | Auth            | Responsibility |
|----------------|-------------------|-----------------|----------------|
| Ingest API     | `cmd/ingest`      | Channel API key | Accept triggers, rate-limit, dedupe, enqueue |
| App API        | `cmd/app`         | User JWT        | Auth, channels, keys, devices, subscriptions, history, live stream |
| Dispatcher     | `cmd/dispatcher`  | —               | Consume queue, persist message, fan-out push, write receipts, emit live event |

Infrastructure: **MongoDB** (persistence), **RabbitMQ** (durable broker + DLQ),
**Redis** (rate limiting, idempotency, pub/sub for WebSocket fan-out),
**FCM + Web Push** (delivery).

The three Go binaries share one module and `internal/` packages. Ingest and App
are stateless and horizontally scalable; Dispatcher scales by adding consumers.

## 3. Trigger flow
```
website ──POST /v1/ingest/{channelId}/notify (X-Api-Key)──▶ Ingest API
   │ validate key · token-bucket rate-limit (Redis) · idempotency check
   └─ publish (publisher confirms) ──▶ RabbitMQ ──▶ 202 Accepted

Dispatcher: consume
   ├─ insert Message (Mongo)                  ← history is source of truth
   ├─ load subscribed Devices' tokens
   ├─ FCM multicast (≤500/batch) + Web Push
   ├─ write Delivery rows (sent / failed)
   └─ publish live event ──▶ Redis pub/sub ──▶ App API WebSocket ──▶ open web tabs
```
The message is persisted **before** push is attempted, so delivery failures never
lose history. Dispatcher acks the queue only **after** the Mongo write succeeds;
repeated failures route to a Dead Letter Queue for retry/inspection.

## 4. Data model (MongoDB collections)

- **users** — `_id, email, passwordHash (argon2id), createdAt`
- **channels** — `_id, publicId, ownerId, name, subscriptionMode (open|approval), createdAt`
- **apiKeys** — `_id, channelId, hashedKey, prefix, label, createdAt, revokedAt?`
- **devices** — `_id, userId, platform (ios|android|web), fcmToken?, webPushSub?, createdAt, lastSeenAt`
- **subscriptions** — `_id, channelId, deviceId, status (active|pending|blocked), createdAt`
- **messages** — `_id, channelId, title, body, data?, createdAt` (indexed `{channelId, _id}` for cursor pagination)
- **deliveries** — `_id, messageId, deviceId, status (sent|failed|skipped), error?, sentAt`

### Subscription modes
- `open` — any user with the public channel ID can subscribe; status `active` immediately.
- `approval` — subscribing creates a `pending` row; the channel owner must approve.

## 5. API surface (v1)

### Ingest (public, header `X-Api-Key`)
- `POST /v1/ingest/{channelId}/notify` — body `{title, body, data?}`, optional
  `Idempotency-Key` header → `202 Accepted`.

### App (user JWT)
- `POST /v1/auth/register` · `POST /v1/auth/login` · `POST /v1/auth/refresh`
- `POST /v1/channels` · `GET /v1/channels`
- `POST /v1/channels/{id}/keys` (returns plaintext key once) · `DELETE /v1/channels/{id}/keys/{keyId}`
- `POST /v1/devices` · `DELETE /v1/devices/{id}`
- `POST /v1/channels/{id}/subscribe` · `POST /v1/channels/{id}/approvals/{deviceId}`
- `GET /v1/channels/{id}/messages?cursor=&limit=` · `GET /v1/channels/{id}/export?format=json|csv`
- `GET /v1/stream` — WebSocket, live messages for the user's subscribed channels

## 6. Security
- API keys stored hashed (SHA-256 of a high-entropy secret); shown once on creation; multiple keys per channel with revocation.
- Passwords hashed with Argon2id.
- JWT: short-lived access token + rotating refresh token.
- Public ingest endpoint protected by per-key Redis token-bucket rate limiting and idempotency keys (dedupe website retries).
- FCM service-account JSON and Web Push VAPID keys are injected as runtime secrets — never committed.

## 7. Reliability — "no messages lost"
1. Ingest acks the website only after RabbitMQ confirms the publish (publisher confirms).
2. Queue and messages are durable; broker survives restarts.
3. Dispatcher persists to Mongo before acking the queue, and before sending push.
4. Failed deliveries are recorded per-device for later resend; poison messages go to a DLQ.

## 8. Phased roadmap
1. **MVP** — Auth, Channels + keys, Ingest→RabbitMQ→Dispatcher→Mongo, FCM to a device, history API.
2. **Web** — Mantine SPA, device registration, history browsing, WebSocket live + Web Push.
3. **Mobile** — Flutter FCM registration, history, subscribe.
4. **Hardening** — approval mode, delivery receipts/resend, rate limits, export, metrics (Prometheus), Kubernetes manifests.
