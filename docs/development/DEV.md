# Pheme — Developer Guide (for humans and LLMs)

This document orients a contributor (or an AI assistant) to the Pheme codebase:
what it is, how it is structured, and the conventions to follow. Read it before
making changes. Keep it up to date when conventions change.

## 1. What Pheme is

Pheme is a **notification relay**. A website triggers a **channel** by its public
ID over a simple HTTP API; every device subscribed to that channel receives an
instant push notification, and every message is stored so history is never lost.

Named after the Greek goddess of fame/report. Brand mark: a tilted broadcast
glyph on a violet→grape gradient tile; wordmark in Space Grotesk.

## 2. Architecture

Three Go services share one module and one set of `internal/` packages:

| Service      | Binary           | Auth            | Responsibility |
|--------------|------------------|-----------------|----------------|
| App API      | `cmd/app`        | User JWT        | auth, channels, API keys, devices, subscriptions, history, live SSE, admin API |
| Ingest API   | `cmd/ingest`     | Channel API key | accept website triggers, rate-limit, enqueue |
| Dispatcher   | `cmd/dispatcher` | —               | consume queue → persist message → fan-out push → record deliveries → emit live event |

Infrastructure: **MongoDB** (persistence, incl. **GridFS** for message images),
**RabbitMQ** (durable queue + DLQ), **Redis** (rate limiting + pub/sub for live
events), **FCM + Web Push** (delivery).

**Trigger flow:** `website → Ingest (X-Api-Key) → RabbitMQ → Dispatcher →
Mongo (history) → Redis pub/sub → App API SSE → browser`, and in parallel
`Dispatcher → FCM / Web Push → devices`.

**Two separate auth domains** — keep them distinct:
- **Ingest API** is authenticated by a per-channel **API key** (`X-Api-Key`); it
  only sends. A leaked key can fire one channel and nothing else.
- **App API** is authenticated by **JWT** (Bearer). It manages everything. The
  one bridge is `POST /v1/channels/{id}/notify`, which lets a logged-in owner
  send from the UI using their JWT instead of an API key.

`internal/bootstrap` builds dependencies from config and selects implementations
per driver env var. Every infra dependency has an interface with an in-memory
implementation (default) and a real one, so services run with zero external
infra unless configured otherwise.

See [ARCHITECTURE.md](ARCHITECTURE.md) for the original design and data model.

## 3. Repository layout

```
api/          Go module github.com/rh1tech/pheme/api
  cmd/        app, ingest, dispatcher, vapidgen (entrypoints)
  internal/
    domain/   core entities (User, Channel, APIKey, Device, Subscription, Message, MessageImage, Comment, …)
    store/    Store interface + Memory and Mongo implementations (cascades image-blob deletes)
    blob/     blob Store interface + Memory and GridFS (processed message images)
    imaging/  server-side image processing (decode, EXIF-orient, downscale ≤1000px, re-encode JPEG)
    broker/   Publisher/Consumer interface + Memory and RabbitMQ
    live/     live event Bus + Memory and Redis pub/sub
    ratelimit/ Limiter + in-memory and Redis token bucket
    push/     Sender + FCM, Web Push, MultiSender, LogSender
    email/    transactional mail Sender + LogSender and SMTPSender (verification/reset codes)
    otp/      verification-code Store (Memory + Redis): pending signups, reset codes, send cooldowns
    auth/     Argon2id passwords, password strength policy, JWT tokens, bearer middleware
    channel/  HTTP handlers: app.go, ingest.go, auth_handler.go, admin_handler.go, notify_input.go (shared JSON/multipart parsing + image processing)
    config/   env-driven Config
    httpx/    small HTTP helpers (JSON, Error, Decode, Health)
    bootstrap/ assembles dependencies from Config
web/          Vite + React + TypeScript + Mantine SPA
  src/
    lib/        non-React utilities: api, tokens, jwt, types, notify, webpush, device
    auth/       AuthProvider + context (JWT, role)
    hooks/      reusable hooks (useEventStream)
    components/ reusable presentational components (Layout, Logo, badges, ConfirmModal, …)
      admin/    admin-specific shared UI (AdminUI: shell, SearchBar, Pager, StatCard)
    pages/      route screens; pages/admin/ for the admin area
    i18n/       i18next setup + en.ts / ru.ts resources
    theme.ts    Mantine theme; styles.css global overrides
mobile/       Flutter app (Riverpod + go_router + Dio); see mobile/README.md
deploy/       docker-compose for Mongo/RabbitMQ/Redis (+ k8s later)
scripts/      dev scripts (setup, dev, infra, stop) — see scripts/README.md
docs/         Public operator and protocol documentation
  development/ Historical design notes and this contributor guide
```

## 4. Backend conventions (Go)

- **Interfaces over concretions.** Anything touching infra (store, broker, live,
  ratelimit, push) is an interface in its package, with a `Memory`/`Log` default
  and a real adapter. New backends implement the interface; do not special-case
  callers.
- **Drivers via config.** Selection is by `PHEME_*_DRIVER` env vars resolved in
  `internal/bootstrap`. Defaults are zero-dependency (`memory`/`log`).
- **Handlers stay thin.** HTTP handlers live in `internal/channel`; they validate
  input, call the store/broker, and use `internal/httpx` for responses
  (`httpx.JSON`, `httpx.Error`, `httpx.Decode`). Business rules live below them.
- **Store is the single persistence boundary.** Both `Memory` and `Mongo` must
  implement every `Store` method; keep them in sync. Deletions cascade
  (deleting a channel removes its keys, subscriptions, messages, deliveries;
  deleting a user removes its channels and devices).
- **Errors:** return `store.ErrNotFound` for missing entities; handlers map it to
  404. Avoid leaking entity existence on auth-sensitive paths (ingest returns a
  uniform 401 for bad channel or key).
- **Security:** passwords use Argon2id; API keys are stored as SHA-256 hashes and
  shown once; JWT carries the user role; admin endpoints check the role from the
  request context (`auth.IsAdmin`).
- **Before committing:** `cd api && go build ./... && go vet ./... && gofmt -l .
  && go test ./...` must all pass clean.

## 5. Frontend conventions (web) — IMPORTANT

The web app is **component-based** and must stay **DRY**. When adding UI:

1. **Reuse before writing.** Check `components/` and `components/admin/` first.
   Existing shared building blocks — use these rather than re-implementing:
   - `components/ConfirmModal` — every confirm/delete dialog.
   - `components/badges` — `ModeBadge`, `ChannelStatusBadge`, `UserStatusBadge`,
     `RoleBadge`, `ChannelRoleBadge` (per-channel admin/user), `MemberStatusBadge`
     (active/pending/blocked). Never inline a status/role/mode `<Badge>`; add a
     badge component if a new kind appears.
   - `components/SubscribersPanel` — owner/channel-admin approvals queue +
     lazily-paginated subscriber list with ban/remove/role actions.
   - `components/admin/AdminUI` — `AdminPageShell` (title + header actions row),
     `SearchBar`, `Pager`, `StatCard`, `ADMIN_PAGE_LIMIT` for admin list pages.
   - `lib/notify` — `notifyError(message, err?)` / `notifySuccess(message)`.
     Do **not** call `notifications.show` directly in pages.
2. **Pages are thin compositions.** A file in `pages/` wires data (via `lib/api`)
   to shared components. Extract anything reused by ≥2 pages into `components/`
   (presentational) or `hooks/` (stateful logic) or `lib/` (pure utilities).
3. **Folder rules.**
   - `lib/` — framework-agnostic logic and the API client. No JSX.
   - `hooks/` — reusable `use*` hooks.
   - `components/` — reusable presentational components; `components/admin/` for
     admin-only shared UI.
   - `pages/` — one screen per route; `pages/admin/` for admin routes.
4. **API access only through `lib/api.ts`.** It centralises base URL, bearer
   auth, and transparent access-token refresh on 401. Never `fetch` directly
   from a component.
5. **All user-facing strings are translated.** Add keys to **both** `i18n/en.ts`
   and `i18n/ru.ts` (English is the source of truth; the structural type enforces
   that `ru` has every key). Never hardcode display text. Verify key parity after
   edits.
6. **Theme, not inline styles.** Use Mantine props and the theme in `theme.ts`
   (brand gradient `BRAND_GRADIENT`, `iris` palette). Global tweaks go in
   `styles.css`. Avoid ad-hoc colors.
7. **Data-loading effect pattern** (used consistently): guard against unmounts
   with an `active` flag and set state inside the promise callback:
   ```tsx
   useEffect(() => {
     let active = true
     api.something().then((d) => active && setData(d)).catch((e) => active && notifyError(t('x'), e))
     return () => { active = false }
   }, [deps])
   ```
   Do not call `setState` synchronously in an effect body (the lint rule
   `react-hooks/set-state-in-effect` forbids it).
8. **Before committing:** `cd web && npm run build` (tsc + vite) and
   `npx eslint src --max-warnings 0` must pass with **zero** warnings.

## 6. Internationalization

`react-i18next`, languages **en** (default) and **ru**, switchable at runtime and
persisted to localStorage. `i18n/en.ts` defines the `Resources` shape; `ru.ts`
must satisfy it (full key parity, enforced at compile time). The document `lang`
attribute is kept in sync. Add a language by adding `i18n/<lang>.ts` and listing
it in `SUPPORTED_LANGUAGES`.

## 7. Auth & roles (web)

`auth/AuthProvider` stores JWT access/refresh tokens, decodes the user id and
role, and exposes `isAuthenticated` / `isAdmin`. Route guards: `RequireAuth` and
`RequireAdmin`. Admins are granted by the server's `PHEME_ADMIN_EMAILS` allowlist
(synced on login) and can promote/demote/block other users.

## 8. Local development

```bash
make setup   # one-time: tools check, pick ports, generate VAPID keys
make dev     # build + run infra, API services and web; streams logs
```

Open http://localhost:5173. `make help` lists all targets (build, test, lint,
infra-*, vapid, web-build). Setup auto-detects port conflicts and writes the
gitignored `.env.dev`, `deploy/.env` and `web/.env.local`. To become an admin,
set `PHEME_ADMIN_EMAILS` in `.env.dev`. Details in
[../../scripts/README.md](../../scripts/README.md).

## 9. Testing

Two layers, both run in CI (`.github/workflows/ci.yml`).

**API unit tests (Go).** Standard `go test`, table-driven where it helps, run
with the race detector. HTTP handlers are tested through their real routes:
`AuthHandler`/`AdminHandler` mount on a bare mux and inject the caller's
id+role into the context (mirroring the JWT middleware); `AppHandler` tests go
through the full `Routes` wiring with real access tokens. In-memory store /
broker / live drivers keep tests dependency-free.

```bash
make test         # cd api && go test -race ./...
make test-cover   # adds a per-package coverage summary
```

**E2E (Playwright, `web/e2e/`).** Drives the real SPA against a live App API.
`playwright.config.ts` starts two `webServer`s — the App API (`go run ./cmd/app`
with in-memory drivers and a **seeded admin**, ports chosen to avoid a running
`make dev` stack) and the Vite dev server pointed at it — then tears them down.
The seeded admin (`PHEME_SEED_ADMIN_EMAIL` / `PHEME_SEED_ADMIN_PASSWORD`) lets
the suite log in deterministically, and every run executes on both Chromium and
mobile WebKit. Scenarios cover the login/auth guard; the full admin user
lifecycle (create, create-as-admin, promote/demote, block/unblock, the blocked
user being unable to log in, password reset, delete); admin channel
disable/enable and delete; the owner flows of channel creation, API-key
(token) creation, sending a message, and attaching an image to a message; and
the membership flows (setting a phetag + duplicate rejection, and a second user
joining by phetag with the owner approving the request).

```bash
make e2e-install  # one-time: npm ci + playwright browsers
make e2e          # npx playwright test (starts/stops the servers itself)
```

When adding a handler, add or extend a test in `internal/channel/*_test.go`.
When adding a user-facing flow, add an E2E scenario; prefer role/label/text
selectors over brittle DOM queries.

## 10. Definition of done for a change

- Backend: `go build`/`go vet`/`gofmt -l`/`go test -race` clean; both store
  backends updated together; new infra behind an interface + driver; new/changed
  handlers covered by a unit test.
- Frontend: `npm run build` and `eslint --max-warnings 0` clean; no duplicated
  UI (reused shared components); all strings in en + ru with key parity; API
  calls via `lib/api`; notifications via `lib/notify`; new user flows covered by
  an E2E scenario (`npm run e2e`).
- Commits: do **not** include AI-trace trailers (a commit-msg hook rejects
  `Co-Authored-By: …(AI)` / "Generated with…").
