# Server operation

This document describes what runs on a Pheme server, how requests move through
it, where state lives, and how failures affect users.

## Processes

| Process | Default address | Responsibility |
|---|---:|---|
| App API (`cmd/app`) | `:8080` | Accounts, JWTs, profiles, channels, chat, MLS delivery, calls, live SSE, administration, and federation |
| Ingest API (`cmd/ingest`) | `:8081` | Channel API-key authentication, rate limiting, idempotency, image processing, and queue publication |
| Dispatcher (`cmd/dispatcher`) | none | Consume broadcast jobs, persist them, send push, record delivery results, and emit live events |

The three binaries share the packages under `api/internal/`. Backend concerns
are selected independently:

| Concern | Driver variable | Development | Production |
|---|---|---|---|
| Records | `PHEME_STORE_DRIVER` | `memory` | `mongo` |
| Blobs | `PHEME_BLOB_DRIVER` | `memory` | `gridfs` |
| Broadcast queue | `PHEME_BROKER_DRIVER` | `memory` | `rabbit` |
| Live events/call mailbox | `PHEME_LIVE_DRIVER` | `memory` | `redis` |
| Limits and idempotency | `PHEME_RATELIMIT_DRIVER` | `memory` | `redis` |
| Verification codes | `PHEME_OTP_DRIVER` | `memory` | `redis` |
| Push | `PHEME_PUSH_DRIVER` | `log` | `webpush`, `fcm`, or `both` |
| Mail | `PHEME_MAIL_DRIVER` | `log` | `smtp` |

Do not horizontally scale a production process while using memory drivers.
Their state is process-local, so limits, replay checks, OTPs, and live events
would disagree between replicas.

## Broadcast channel flow

1. A website posts to `/v1/ingest/{channelId}/notify` with `X-Api-Key`.
2. Ingest verifies the hashed channel key, applies a per-key token bucket, and
   checks an optional `Idempotency-Key`.
3. Images are validated, oriented, resized, re-encoded, and stored in GridFS.
4. RabbitMQ confirms the durable publication before Ingest returns `202`.
5. Dispatcher consumes the job and writes the channel message to MongoDB.
6. Dispatcher sends Web Push/FCM, records delivery outcomes, and publishes a
   Redis live event.
7. Connected App API instances deliver the event over SSE.

The dispatcher acknowledges the queue only after durable persistence. Poison
jobs are routed through RabbitMQ's dead-letter behavior. Delivery failures do
not remove message history.

Federated open channels add a final fan-out step: the origin sends the post and
processed image bytes to subscribed peer hosts, which store a local mirror and
notify local subscribers. Approval-mode membership and remote comments are not
currently federated.

## Encrypted conversation flow

1. Clients authenticate with the App API and register an MLS device.
2. Each device publishes public, single-use MLS KeyPackages.
3. A group member claims packages for conversation members and creates or
   updates the MLS group locally.
4. The client stages an MLS Commit and posts it with the epoch it was based on.
5. The conversation hub accepts only the commit matching its current epoch.
6. The client merges an accepted commit or discards a rejected staged commit.
7. Application messages and attachments are encrypted on the client. The server
   stores and routes opaque bytes with a hub-assigned sequence.

MongoDB stores the conversation roster, ciphertext log, MLS public handshake
material, key packages, encrypted key backups, and encrypted history handoffs.
Private MLS ratchet state remains on user devices.

For a federated conversation, the creator's server is the immutable hub. A
follower forwards local writes to the hub and persists only the authoritative
echo. Each participant host therefore holds a readable local ciphertext mirror
without creating a second writer.

## Call flow

Call signalling is sealed with a key exported from the MLS group. Redis keeps a
short-lived signal mailbox and the first-device-to-answer lock; call records are
not written to MongoDB. WebRTC media is peer-to-peer unless TURN is needed.

Across servers, each participant host relays sealed signals to the other
participant hosts. TURN credentials remain host-local secrets: a peer requests
only a short-lived credential through the signed federation channel.

## Storage map

**MongoDB** is the durable source of truth. It stores users, devices, channels,
memberships, broadcast messages, comments, delivery records, conversations,
conversation members, ciphertext messages, attachments, MLS metadata, key
packages, key backups, federation mirrors, and GridFS blobs.

**RabbitMQ** holds accepted broadcast work between Ingest and Dispatcher. It is
not used to order encrypted conversation messages.

**Redis** stores ephemeral/distributed operational state: live pub/sub, OTPs,
rate-limit buckets, idempotency keys, federation nonces, call signalling, and
answer locks. Redis persistence helps continuity but does not replace MongoDB
backups.

**Client storage** holds private MLS keys and ratchet state. Device loss is
handled through encrypted key backup, external join, and sealed history handoff;
the server cannot reconstruct keys from conversation ciphertext.

## Scaling

- App and Ingest are stateless with respect to request execution when backed by
  MongoDB and Redis, and can be replicated behind a load balancer.
- Dispatcher scales with RabbitMQ consumers.
- Every replica must use the same MongoDB, Redis, RabbitMQ, secrets, host domain,
  and host key for one logical server.
- Preserve the original request path at the application. Federation signatures
  bind method, path, destination, body hash, timestamp, and nonce.
- Federation calls have a bounded client timeout. A slow peer must not consume
  local request workers indefinitely.

## Availability and failure behavior

| Failure | Effect |
|---|---|
| App API unavailable | Login, chat writes, history reads, SSE, and federation stop; queued broadcast processing may continue |
| Ingest unavailable | New website broadcasts are rejected; existing history remains |
| Dispatcher unavailable | Accepted broadcasts accumulate in RabbitMQ |
| MongoDB unavailable | Durable reads/writes stop; services should fail rather than report false success |
| RabbitMQ unavailable | Ingest cannot durably accept broadcasts |
| Redis unavailable | Live delivery, OTPs, distributed limits, calls, and federation replay checks fail or degrade according to the caller; conversation history remains |
| Push provider unavailable | History remains available; background notification delivery fails |
| Conversation hub unavailable | Mirrors remain readable, but new messages, commits, and ordered receipts pause |
| Conversation hub permanently lost | The conversation remains readable on mirrors but becomes permanently read-only in v1 |

The hub behavior intentionally favors consistency over availability. Followers
do not accept speculative writes and therefore cannot split the conversation.

## Routine operations

Monitor container health, HTTP health endpoints, MongoDB capacity and latency,
RabbitMQ queue depth/dead letters, Redis memory/AOF health, push errors, SMTP
errors, federation signature failures, and nodelist expiry.

Before an upgrade:

1. Confirm an off-host backup and restore procedure.
2. Save current image digests and configuration.
3. Check whether the release includes a data or client protocol migration.
4. Upgrade one environment and exercise registration, channel ingest, encrypted
   chat, calls, and federation before production rollout.

When rotating credentials:

- JWT secret rotation signs users out unless the asymmetric host-key transition
  window is configured.
- Host-key rotation requires a new signed nodelist entry and coordinated rollout.
- Path-prefix rotation requires nginx, API base URLs, push URLs, and every client
  configuration to overlap until clients move.
- Nodelists must be reissued before expiry with an increasing serial. Removal
  from the next list is peer revocation.

See [deployment.md](deployment.md) for backups and file placement and
[federation.md](federation.md) for peer operations.
