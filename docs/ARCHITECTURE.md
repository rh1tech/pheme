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

Infrastructure: **MongoDB** (persistence, incl. **GridFS** for message images),
**RabbitMQ** (durable broker + DLQ), **Redis** (rate limiting, idempotency,
pub/sub for WebSocket fan-out), **FCM + Web Push** (delivery).

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

- **users** — `_id, email, passwordHash (argon2id), role, status, username? + usernameLower (unique handle, display-only), displayName?, bio?, phone?, website?, avatarId? (blob ref), createdAt`. `username` is optional, unique case-insensitively (partial unique index on `usernameLower`, mirroring channel `aliasLower`), and is **not** a login credential — email remains the login.
- **channels** — `_id, publicId, ownerId, name, alias? ("phetag", unique case-insensitively via `aliasLower`), subscriptionMode (open|approval), createdAt`
- **apiKeys** — `_id, channelId, hashedKey, prefix, label, createdAt, revokedAt?`
- **devices** — `_id, userId, platform (ios|android|web), fcmToken?, webPushSub?, createdAt, lastSeenAt`
- **subscriptions** — `_id, channelId, deviceId, status (active|pending|blocked), createdAt` — **per-device**, drives push delivery.
- **channelMembers** — `_id, channelId, userId, role (admin|user), status (active|pending|blocked), createdAt` (unique `{channelId,userId}`). The **per-user** membership layer: the unit of approval, ban, and per-channel moderation role. The channel owner is the implicit top authority and has **no** member row. Membership does not gate the dispatcher; instead approve/ban/remove flip the user's device-subscription statuses, so delivery stays driven by `subscriptions`.
- **messages** — `_id, channelId, title, body, images?, data?, commentsAllowed, createdAt` (indexed `{channelId, createdAt}` for cursor pagination). `images` is an ordered list of `{id, width, height}` (Instagram-style: shown before the text), up to 10 per message; `id` references a processed JPEG in the blob store. `commentsAllowed` is decided per-message when sending (default true) and gates commenting on that message.
- **comments** — `_id, messageId, channelId, userId, body, createdAt` (indexed `{messageId, createdAt}` for cursor pagination, plus `{channelId}`/`{userId}` for cascades and `{createdAt}` for admin moderation). `channelId` is denormalized so deletes cascade by channel and the moderation panel resolves the channel without a message lookup. Active members (and the owner) post instantly; the author or a channel owner/admin can delete.
- **deliveries** — `_id, messageId, deviceId, status (sent|failed|skipped), error?, sentAt`
- **images (blob store)** — processed JPEGs stored in **MongoDB GridFS** (`fs.files`/`fs.chunks`) under an unguessable random id, behind a `blob.Store` driver interface (memory | gridfs). Images are processed (validated, EXIF-oriented, downscaled so the longer edge ≤ 1000px, re-encoded as JPEG ~q82) at the API boundary **before** enqueue, so the broker payload only carries references. Deleting a channel/user cascades to its messages' image blobs.

### Subscription modes
- `open` — any user with the channel's trigger ID or phetag can join; membership is `active` immediately.
- `approval` — joining creates a `pending` membership; the owner (or a channel-admin) must approve it, which activates the user's devices.

### Channel roles (per-channel)
- **owner** — the single `ownerId`; can do everything, including delete the channel, change the phetag, and manage API keys.
- **admin** (member role) — can approve/deny pending, ban/remove subscribers, change other members' roles, and send messages. Cannot delete the channel, change the phetag, or manage API keys.
- **user** (member role) — a plain subscriber.

## 5. API surface (v1)

### Ingest (public, header `X-Api-Key`)
- `POST /v1/ingest/{channelId}/notify` — JSON body `{title, body, data?}` (text
  only), or `multipart/form-data` with `title`/`body`/`data` fields plus up to 10
  `images` file parts (≤ 10 MB each). Optional `Idempotency-Key` header → `202
  Accepted`. At least one of title, body, or an image is required.

### App (user JWT)
- `POST /v1/auth/register` (emails a 6-digit code) · `POST /v1/auth/verify` (confirms the code → creates the account, logs in) · `POST /v1/auth/login` · `POST /v1/auth/refresh`
- **Profile (self):** `GET /v1/me` · `PATCH /v1/me` (`{username?, displayName?, bio?, phone?, website?}` — username validated + unique, empty clears it) · `POST /v1/me/avatar` (multipart `avatar`, reuses the image pipeline) · `DELETE /v1/me/avatar`
- `POST /v1/auth/forgot-password` (emails a reset code) · `POST /v1/auth/reset-password`
- `POST /v1/channels` · `GET /v1/channels` (owned) · `GET /v1/channels/{id}` (single, with caller's relation) · `PATCH /v1/channels/{id}` (name/mode, and `alias` owner-only) · `DELETE /v1/channels/{id}`
- `POST /v1/channels/{id}/keys` (returns plaintext key once) · `DELETE /v1/channels/{id}/keys/{keyId}`
- `POST /v1/devices` · `DELETE /v1/devices/{id}`
- **Join & membership:** `POST /v1/channels/join` (`{ref, deviceId?}` — ref is a trigger ID or phetag) · `GET /v1/channels/joined` · `GET /v1/channels/{id}/membership` · `DELETE /v1/channels/{id}/membership` (leave)
- **Approvals (owner/admin):** `GET /v1/channels/{id}/approvals` · `POST /v1/channels/{id}/approvals/{userId}` (approve) · `DELETE /v1/channels/{id}/approvals/{userId}` (deny)
- **Subscribers (owner/admin):** `GET /v1/channels/{id}/members?offset=&limit=` (lazy) · `PATCH /v1/channels/{id}/members/{userId}` (`{role?, status?}` — ban/unban, role change) · `DELETE /v1/channels/{id}/members/{userId}` (remove)
- `POST /v1/channels/{id}/subscribe` (device-level; also establishes membership)
- `GET /v1/channels/{id}/messages?cursor=&limit=&q=` · `GET /v1/channels/{id}/messages/{messageId}` (single message, for the detail view and notification deep-links) · `POST /v1/channels/{id}/notify` (owner sends from the UI; JSON or `multipart/form-data` with `images`, same as ingest; accepts `commentsAllowed`, default true)
- **Comments (active members):** `GET /v1/channels/{id}/messages/{messageId}/comments?cursor=&limit=` (author public profile only — never email) · `POST /v1/channels/{id}/messages/{messageId}/comments` (`{body}`; requires active membership and `commentsAllowed`) · `DELETE /v1/channels/{id}/messages/{messageId}/comments/{commentId}` (author, or channel owner/admin)
- `GET /v1/images/{id}` — serves a processed JPEG by id. **Public** (no JWT): the id is unguessable, devices/`<img>`/push fetch it without a bearer, and message history is already readable by any authenticated user. Long-cached (`immutable`).
- `GET /v1/stream` — SSE live messages (token via query parameter)

### Admin (JWT, admin role only) — `/v1/admin/*`
- `GET /v1/admin/stats` — totals (users, channels, messages, deliveries, devices), top channels, recent messages
- `GET /v1/admin/users` · `POST /v1/admin/users` (create a user directly: email + password + role, bypassing email verification) · `PATCH /v1/admin/users/{id}` (role/status) · `DELETE /v1/admin/users/{id}` (cascades the user's data) · `POST /v1/admin/users/{id}/reset-password`
- `GET /v1/admin/channels` · `DELETE /v1/admin/channels/{id}` (cascades)
- `GET /v1/admin/channels/{id}/keys` · `DELETE /v1/admin/channels/{id}/keys/{keyId}`
- **Comment moderation:** `GET /v1/admin/comments?q=&page=&limit=` (every comment, enriched with author email, channel name and message title) · `DELETE /v1/admin/comments/{id}`. Banning a comment's author reuses `PATCH /v1/admin/users/{id}` (`status: blocked`).

## 6. Security
- API keys stored hashed (SHA-256 of a high-entropy secret); shown once on creation; multiple keys per channel with revocation.
- Passwords hashed with Argon2id; a light strength policy (min 8 chars, ≥2 character classes, common-password blocklist) is enforced on register, reset, and admin-set.
- **Email-verified registration.** Register does not create a user; it stores a pending signup in the OTP store and emails a 6-digit code. `verify` confirms it (creating the account). Codes are stored as SHA-256 hashes, expire (default 30 min), invalidate after 3 wrong attempts, and are rate-limited to one send per email per 2 minutes. Password reset uses the same code mechanism.
- **Transactional mail** is sent via the `email` driver (`log` for dev, `smtp` in prod). Production relays through a host Postfix + OpenDKIM (DKIM `d=app.example.com`), delivering direct-to-MX with SPF/DKIM/DMARC and forward-confirmed rDNS for inbox placement. The relay never sends third-party mail (Brevo is not used).
- JWT: short-lived access token + rotating refresh token. The token carries the user's role.
- Roles: `user` (default) and `admin`. Admins are designated by the `PHEME_ADMIN_EMAILS` allowlist and the role is (re)synced on every register/login, so changing the list takes effect on next login. Admin-only endpoints live under `/v1/admin/*` and verify the role from the JWT. Admins can also add users directly from the panel (`POST /v1/admin/users`), creating an active account with no email step.
- **Initial admin seeding.** When `PHEME_SEED_ADMIN_EMAIL` and `PHEME_SEED_ADMIN_PASSWORD` are both set, the App API ensures a verified, active admin with those credentials exists at startup (created only if missing). This is opt-in (no-op when unset), bootstraps the first admin without the email-verification flow, and backs the E2E suite.
- Public ingest endpoint protected by per-key Redis token-bucket rate limiting and idempotency keys (dedupe website retries).
- FCM service-account JSON and Web Push VAPID keys are injected as runtime secrets — never committed.
- **Message images** are processed server-side (validated, EXIF-oriented, downscaled to ≤ 1000px on the longer edge, re-encoded as JPEG ~q82), which also strips EXIF/GPS metadata. Per-image upload is capped at 10 MB and 10 images per message. Blobs use unguessable random ids and are served public, immutable, and long-cached. Push notifications include the first image's absolute URL when `PHEME_PUBLIC_API_URL` is set (the externally reachable App API base); unset simply omits the notification image.
- **Notification deep-linking.** Push payloads carry `channelId` and `messageId` in their data; tapping a notification opens the message-detail view (web service worker, and mobile via `onMessageOpenedApp`/`getInitialMessage`) rather than the channel list. Message lists show only the first image as a cover; the detail view shows all images in a carousel.

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
| Image blobs | `PHEME_BLOB_DRIVER`       | `memory` \| `gridfs`     | MongoDB GridFS (`internal/blob`); S3/MinIO is the documented future driver |
| Broker      | `PHEME_BROKER_DRIVER`     | `memory` \| `rabbit`     | RabbitMQ, publisher confirms + DLX/DLQ (`internal/broker`) |
| Live events | `PHEME_LIVE_DRIVER`       | `memory` \| `redis`      | Redis pub/sub (`internal/live`) |
| Rate limit  | `PHEME_RATELIMIT_DRIVER`  | `memory` \| `redis`      | Redis Lua token-bucket (`internal/ratelimit`) |
| Push        | `PHEME_PUSH_DRIVER`       | `log` \| `fcm` \| `webpush` \| `both` | FCM + Web Push (`internal/push`) |
| Mail        | `PHEME_MAIL_DRIVER`       | `log` \| `smtp`          | SMTP relay (`internal/email`) |
| OTP codes   | `PHEME_OTP_DRIVER`        | `memory` \| `redis`      | Redis (`internal/otp`) — pending signups, reset codes, send cooldowns |

`internal/bootstrap` builds these from config and is shared by all three mains.

> **Note:** the MongoDB root user created by compose authenticates against the
> `admin` database — use `?authSource=admin` in `PHEME_MONGO_URI`.

