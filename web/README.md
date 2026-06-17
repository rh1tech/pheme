# Pheme Web

TypeScript + Vite + Mantine single-page app for browsing notifications,
managing channels and API keys, registering devices, and (in the Web phase)
receiving browser push.

## Develop
```bash
cp .env.example .env.local   # set VITE_API_BASE / VITE_DEV_USER
npm install
npm run dev                  # http://localhost:5173
```
Requires the App API running (default `http://localhost:8080`).

## Build
```bash
npm run build
npm run preview
```

## Notes
- Auth is currently a development placeholder (`X-User-Id` header); it will be
  replaced by JWT bearer tokens once the auth endpoints land.
- `public/sw.js` is a Web Push service worker placeholder, wired up in the Web
  phase together with server-side VAPID keys.
