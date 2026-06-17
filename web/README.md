# Pheme Web

TypeScript + Vite + Mantine single-page app: sign in, manage channels and API
keys, browse message history, receive live updates over SSE, and register the
browser for Web Push notifications.

## Develop
```bash
cp .env.example .env.local        # set VITE_API_BASE
npm install
npm run dev                       # http://localhost:5173
```
Requires the App API running (default `http://localhost:8080`). For live updates
and Web Push, run the API with the Redis live bus and a configured VAPID key
pair (see ../deploy/.env.example).

## Build & lint
```bash
npm run build                     # tsc typecheck + vite production build
npx eslint src --max-warnings 0
```

## Structure
```
src/
├── lib/
│   ├── api.ts        API client: bearer auth + transparent token refresh
│   ├── tokens.ts     access/refresh token persistence (localStorage)
│   ├── jwt.ts        client-side JWT subject decode (display only)
│   ├── types.ts      API response types
│   ├── webpush.ts    service-worker registration + PushManager subscribe
│   └── device.ts     local Web Push device id
├── auth/             AuthProvider + context (login/register/logout)
├── hooks/
│   └── useEventStream.ts   SSE subscription to /v1/stream
├── components/       Layout (app shell) + RequireAuth route guard
├── pages/            LoginPage, DashboardPage, ChannelPage
└── App.tsx           routes
```

## Notes
- Auth uses JWT access + refresh tokens; the client refreshes the access token
  transparently on a 401 and redirects to login when the session is gone.
- The SSE stream authenticates via a `token` query parameter because
  `EventSource` cannot send an `Authorization` header.
- `public/sw.js` displays incoming Web Push notifications.
