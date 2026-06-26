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
- `POST /v1/auth/register` (emails a 6-digit code) · `POST /v1/auth/verify` (confirms the code → creates the account, logs in) · `POST /v1/auth/login` · `POST /v1/auth/refresh`
- `POST /v1/auth/forgot-password` (emails a reset code) · `POST /v1/auth/reset-password`
- `POST /v1/channels` · `GET /v1/channels`
- `POST /v1/channels/{id}/keys` (returns plaintext key once) · `DELETE /v1/channels/{id}/keys/{keyId}`
- `POST /v1/devices` · `DELETE /v1/devices/{id}`
- `POST /v1/channels/{id}/subscribe` · `POST /v1/channels/{id}/approvals/{deviceId}`
- `GET /v1/channels/{id}/messages?cursor=&limit=&q=` · `POST /v1/channels/{id}/notify` (owner sends from the UI)
- `GET /v1/stream` — SSE live messages (token via query parameter)

### Admin (JWT, admin role only) — `/v1/admin/*`
- `GET /v1/admin/stats` — totals (users, channels, messages, deliveries, devices), top channels, recent messages
- `GET /v1/admin/users` · `DELETE /v1/admin/users/{id}` (cascades the user's data) · `POST /v1/admin/users/{id}/reset-password`
- `GET /v1/admin/channels` · `DELETE /v1/admin/channels/{id}` (cascades)
- `GET /v1/admin/channels/{id}/keys` · `DELETE /v1/admin/channels/{id}/keys/{keyId}`

## 6. Security
- API keys stored hashed (SHA-256 of a high-entropy secret); shown once on creation; multiple keys per channel with revocation.
- Passwords hashed with Argon2id; a light strength policy (min 8 chars, ≥2 character classes, common-password blocklist) is enforced on register, reset, and admin-set.
- **Email-verified registration.** Register does not create a user; it stores a pending signup in the OTP store and emails a 6-digit code. `verify` confirms it (creating the account). Codes are stored as SHA-256 hashes, expire (default 30 min), invalidate after 3 wrong attempts, and are rate-limited to one send per email per 2 minutes. Password reset uses the same code mechanism.
- **Transactional mail** is sent via the `email` driver (`log` for dev, `smtp` in prod). Production relays through a host Postfix + OpenDKIM (DKIM `d=app.example.com`), delivering direct-to-MX with SPF/DKIM/DMARC and forward-confirmed rDNS for inbox placement. The relay never sends third-party mail (Brevo is not used).
- JWT: short-lived access token + rotating refresh token. The token carries the user's role.
- Roles: `user` (default) and `admin`. Admins are designated by the `PHEME_ADMIN_EMAILS` allowlist and the role is (re)synced on every register/login, so changing the list takes effect on next login. Admin-only endpoints live under `/v1/admin/*` and verify the role from the JWT.
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

## 9. Backend drivers
Each infrastructure dependency is behind an interface with two implementations,
selected by environment variable. Defaults are zero-dependency so the services
run with no external infrastructure; switch to the real adapters once the
docker-compose stack is up.

| Concern     | Env var                   | Options                  | Real adapter |
|-------------|---------------------------|--------------------------|--------------|
| Persistence | `PHEME_STORE_DRIVER`      | `memory` \| `mongo`      | MongoDB (`internal/store`) |
| Broker      | `PHEME_BROKER_DRIVER`     | `memory` \| `rabbit`     | RabbitMQ, publisher confirms + DLX/DLQ (`internal/broker`) |
| Live events | `PHEME_LIVE_DRIVER`       | `memory` \| `redis`      | Redis pub/sub (`internal/live`) |
| Rate limit  | `PHEME_RATELIMIT_DRIVER`  | `memory` \| `redis`      | Redis Lua token-bucket (`internal/ratelimit`) |
| Push        | `PHEME_PUSH_DRIVER`       | `log` \| `fcm` \| `webpush` \| `both` | FCM + Web Push (`internal/push`) |
| Mail        | `PHEME_MAIL_DRIVER`       | `log` \| `smtp`          | SMTP relay (`internal/email`) |
| OTP codes   | `PHEME_OTP_DRIVER`        | `memory` \| `redis`      | Redis (`internal/otp`) — pending signups, reset codes, send cooldowns |

`internal/bootstrap` builds these from config and is shared by all three mains.

> **Note:** the MongoDB root user created by compose authenticates against the
> `admin` database — use `?authSource=admin` in `PHEME_MONGO_URI`.

