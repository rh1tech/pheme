# Pheme

Pheme is a self-hosted communication system for private conversations and
durable broadcast channels. It combines MLS end-to-end encrypted chat, encrypted
voice-call signalling, push delivery, and optional federation between
independently operated servers.

Servers route and store conversation ciphertext but do not hold the keys needed
to read it. Web and Flutter clients share the same Rust MLS implementation, so
the encryption protocol is not reimplemented separately for each platform.

## What Pheme provides

- **End-to-end encrypted conversations** using MLS (RFC 9420), including groups,
  multiple devices, membership changes, receipts, history handoff, and encrypted
  attachments.
- **Voice calls** over WebRTC with MLS-derived signalling encryption and
  optional TURN relay support.
- **Broadcast channels** with API-key ingestion, images, comments, membership
  controls, push delivery, and durable history.
- **Web, Android, iOS, and macOS clients**, with English and Russian interfaces.
- **Self-hosting** through Docker Compose, with MongoDB, RabbitMQ, Redis, nginx,
  and optional push, mail, and TURN integrations.
- **Federation** for cross-server channels, conversations, receipts, MLS
  commits, and call signalling. Federation is permissioned through a signed
  nodelist rather than open to arbitrary servers.

## Architecture

```text
web / mobile clients
        |
        | HTTPS + JWT
        v
     App API  <---------------- signed HTTPS ----------------> peer App APIs
        |                   federation and conversation hubs
        +---- MongoDB   durable records, ciphertext, GridFS blobs
        +---- Redis     live events, OTPs, limits, replay protection
        +---- FCM/APNs/Web Push

website integrations
        |
        | HTTPS + channel API key
        v
    Ingest API ---- RabbitMQ ---- Dispatcher ---- storage + push
```

The Go backend contains three binaries:

| Binary | Purpose |
|---|---|
| `pheme-app` | Authentication, channels, encrypted conversations, calls, live events, admin API, and federation |
| `pheme-ingest` | Rate-limited, idempotent channel notification ingestion |
| `pheme-dispatcher` | Durable channel-message consumption, persistence, and push fan-out |

The clients perform MLS operations locally through `crates/pheme-mls`. The
server acts as an untrusted delivery service for conversation content. It still
sees operational metadata such as accounts, membership, timing, and routing; E2EE
does not hide that metadata.

## Run locally

Requirements: Go, Node.js with npm, Docker Compose, and `lsof`.

```bash
make setup
make dev
```

Open `http://localhost:5173`. `make setup` generates gitignored local secrets and
configuration; `make dev` starts MongoDB, RabbitMQ, Redis, the three Go services,
and the web client.

Useful commands:

```bash
make help
make test
make lint
make web-build
make stop ARGS=--all
```

For a standalone internet deployment, start with
[`docs/deployment.md`](docs/deployment.md). Do not reuse development credentials
or expose the local infrastructure ports publicly.

## Repository layout

| Path | Contents |
|---|---|
| `api/` | Go APIs, workers, persistence, delivery, and federation |
| `crates/pheme-mls/` | Shared Rust MLS client core, compiled for WASM and mobile FFI |
| `web/` | React, TypeScript, Vite, and Mantine web client |
| `mobile/` | Flutter client for Android, iOS, and macOS |
| `deploy/` | Compose stacks, nginx template, TURN config, and self-host setup |
| `scripts/` | Local development automation |
| `test/` | Cross-component and federation test harnesses |
| `docs/` | Public deployment, operation, protocol, and federation documentation |
| `docs/development/` | Historical design records and contributor references |

## Documentation

1. [Deployment](docs/deployment.md)
2. [Server operation](docs/server-operation.md)
3. [Protocol and security model](docs/protocol.md)
4. [Federation](docs/federation.md)
5. [Development guide](docs/development/DEV.md)

## Project status

Pheme is under active development. Core channels, MLS conversations, calls, and
permissioned federation are implemented and tested. Operators should read the
documented federation availability limits and back up each conversation hub:
there is no automatic hub migration if a hub disappears permanently.

## Licensing

The complete community edition is licensed under the
[GNU General Public License v3](LICENSE). If you distribute a modified version,
the GPL generally requires you to provide its corresponding source under the
same license.

Separate commercial terms may be negotiated for Pheme-owned code when an
organization needs to keep its modifications proprietary. A commercial license
is granted only by a signed agreement; this repository does not itself grant
commercial rights. See [commercial licensing](COMMERCIAL-LICENSE.md), and
[`LICENSING.md`](LICENSING.md) for how the two licenses fit together.

**Current limitation:** parts of the web client are derived from GPLv3-licensed
Telegram Web K code. Those parts cannot be relicensed by the Pheme project, so
the current web client is not available for closed-source distribution. The
affected code and attribution are documented in [`web/NOTICE.md`](web/NOTICE.md).
Third-party dependencies and vendored code remain under their respective
licenses.
