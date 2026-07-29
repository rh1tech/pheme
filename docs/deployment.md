# Deployment

This guide deploys one Pheme server. Start in standalone mode, verify it, and
only then add federation using [federation.md](federation.md).

## 1. Deployment model

A production instance normally runs:

- `app`, `ingest`, and `dispatcher` from the Pheme API image;
- MongoDB for durable application data and GridFS blobs;
- RabbitMQ for durable broadcast ingestion;
- Redis for live events, OTP state, distributed limits, idempotency, call
  mailboxes, and federation replay protection;
- host nginx for TLS and routing;
- optionally the web client, coturn, SMTP, FCM, and APNs.

MongoDB, RabbitMQ, and Redis must remain private. The supplied production and
self-host stacks expose only the App and Ingest APIs on host loopback for nginx
to proxy.

## 2. Prerequisites

- A Linux host with Docker Engine and Compose v2.
- A domain whose DNS points to the host.
- A publicly trusted TLS certificate.
- Ports 80/443 for nginx.
- For TURN: 3478 TCP/UDP, 5349 TCP, and the configured UDP relay range.
- Backups stored outside the host.

Use a neutral API hostname if reducing automated discovery matters. Pheme can
mount the API below an unlisted random path and serve an unrelated static site at
the root. This reduces fingerprinting but is not authentication and is not a
substitute for TLS, JWTs, API keys, or federation signatures.

## 3. Self-host quick start

From a checkout on the server:

```bash
cd deploy/self-host
./setup.sh
```

The script asks for the API hostname and initial admin email, generates random
database, broker, JWT, host, path-prefix, and VAPID secrets, writes `node.env`
with mode `0600`, and renders an nginx vhost.

Follow the printed TLS/nginx steps, then start the stack:

```bash
docker compose --env-file node.env up -d
```

Add `--profile web` to serve the web client. Verify from another machine:

```bash
./verify.sh https://chat.example.com <path-prefix>
```

The mobile client must be given the complete base URL, including the prefix:

```text
https://chat.example.com/<path-prefix>
```

Distribute that URL or its QR code out of band.

## 4. Production Compose deployment

For a manually managed production environment:

```bash
sudo install -d -m 0750 /opt/pheme
sudo cp deploy/prod/stack.env.example /opt/pheme/stack.env
sudo chmod 0600 /opt/pheme/stack.env
```

Replace every `change-me` value and every `example.com` hostname. Generate
independent credentials:

```bash
openssl rand -hex 32   # Mongo password
openssl rand -hex 32   # RabbitMQ password
openssl rand -hex 32   # JWT transition secret
openssl rand -hex 32   # API path prefix or use at least 16 random bytes
openssl rand -hex 32   # TURN shared secret, if enabled
```

Generate host and Web Push keys from the API module or image:

```bash
cd api
go run ./cmd/hostkey -env
go run ./cmd/vapidgen -env
```

Start the stack from `deploy/prod`:

```bash
docker compose --env-file /opt/pheme/stack.env up -d
```

Render the API nginx vhost from `deploy/nginx/pheme-api.conf.template`; do not
commit the rendered file because it contains the deployment path:

```bash
set -a
. /opt/pheme/stack.env
set +a
PHEME_API_HOST=chat.example.com \
PHEME_DECOY_DIR=example-decoy \
PHEME_SSL_SNIPPET=/etc/nginx/snippets/pheme-ssl.conf \
  ./deploy/nginx/render.sh | sudo tee /etc/nginx/sites-available/chat.example.com.conf
sudo nginx -t
sudo systemctl reload nginx
```

## 5. Files and persistent data

| Item | Recommended location | Treatment |
|---|---|---|
| Environment and generated secrets | `/opt/pheme/stack.env` or `deploy/self-host/node.env` | `0600`, root/operator readable, encrypted off-host backup |
| Firebase service account | `/opt/pheme/secrets/fcm-service-account.json` | `0600`, readable by container uid 65532, never commit |
| APNs `.p8` key | `/opt/pheme/secrets/apns-key.p8` | `0600`, never commit |
| Signed federation nodelist | `/opt/pheme/nodelist.json` | Public content, but update atomically and keep the last valid copy |
| Host TLS certificate | System certificate directory | Follow host nginx permissions and renewal process |
| TURN certificate copy | `/opt/pheme/turn-certs/` | Readable by coturn; restart coturn after renewal |
| Rendered nginx vhost | Distribution-specific nginx config directory | Host-local; contains deployment routing details |
| MongoDB | Compose volume `mongo-data` | Durable; primary backup target |
| RabbitMQ | Compose volume `rabbit-data` | Durable queue state; back up for strict broadcast continuity |
| Redis | Compose volume `redis-data` | AOF state; operational rather than message history |

Conversation attachments, channel images, and avatars are stored in MongoDB
GridFS when `PHEME_BLOB_DRIVER=gridfs`; there is no separate upload directory.
Conversation records contain ciphertext, while broadcast channel content is
server-readable.

## 6. Required configuration

At minimum set:

- database and broker credentials;
- `PHEME_JWT_SECRET` during standalone/transition operation;
- `PHEME_HOST_DOMAIN` and `PHEME_HOST_KEY`;
- `PHEME_API_BASE`, including the path prefix;
- `PHEME_PUBLIC_API_URL` to the same externally reachable base;
- `PHEME_CORS_ORIGINS` to exact web origins, or empty for mobile-only;
- `PHEME_ADMIN_EMAILS`;
- VAPID keys if Web Push is enabled.

The production stack selects MongoDB, GridFS, RabbitMQ, Redis-backed live events,
Redis-backed limits, and Redis OTP storage. In-memory drivers are intended for
development and tests; they do not survive restart or coordinate replicas.

## 7. Optional integrations

**Mail.** `PHEME_MAIL_DRIVER=log` writes verification codes to application logs.
Use `smtp` for real users and configure SPF, DKIM, and DMARC for the sender
domain. Do not enable `PHEME_SMTP_INSECURE_TLS` outside a trusted private relay.

**Web Push.** VAPID keys are generated locally. Use an HTTPS VAPID subject for
Apple browser compatibility.

**Mobile push.** FCM requires an operator-owned Firebase project and service
account. iOS ringing additionally requires an APNs PushKit key, team, key, and
bundle identifiers. Keep all vendor credentials outside Git.

**Calls.** Leave `PHEME_TURN_URLS` empty to disable calls cleanly. If enabled,
run coturn with `use-auth-secret`, the same `PHEME_TURN_SECRET`, the private-peer
deny rules in `deploy/prod/turnserver.conf`, quotas, and a publicly trusted TURN
certificate.

## 8. Backup and restore

Back up MongoDB consistently using `mongodump`, filesystem snapshots taken with
the database quiesced, or a managed Mongo backup mechanism. A Mongo backup must
include the GridFS collections. Keep copies of `stack.env`/`node.env`, the host
key, nodelist coordinator key if you operate one, push keys, and TLS/TURN
material.

Losing the host key changes the server's federation identity. Losing MongoDB
loses accounts, membership, ciphertext history, and blobs. Losing only Redis
interrupts OTPs, live delivery, short-lived call state, limits, and replay
memory, but MongoDB history remains. Losing RabbitMQ can lose accepted broadcast
jobs that have not yet reached MongoDB.

Test restoration on a separate host. Restore secrets and MongoDB before exposing
the replacement, then restore RabbitMQ/Redis if those snapshots are part of the
recovery plan.

## 9. Verification and upgrades

Check:

```bash
docker compose --env-file /opt/pheme/stack.env ps
curl --fail http://127.0.0.1:8191/healthz
curl --fail http://127.0.0.1:8192/healthz
```

Use the public `deploy/self-host/verify.sh` check after nginx changes. For
upgrades, back up first, read release notes for migrations, update image tags,
pull, and recreate:

```bash
docker compose --env-file /opt/pheme/stack.env pull
docker compose --env-file /opt/pheme/stack.env up -d
```

Proceed to [server-operation.md](server-operation.md) before operating the
service, and [federation.md](federation.md) before enabling peer routes.
