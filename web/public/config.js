// Default runtime config for local development. In production the web container
// overwrites this file from PHEME_API_BASE at startup. An empty apiBase makes
// the app fall back to VITE_API_BASE (see src/lib/api.ts).
window.__PHEME_CONFIG = { apiBase: '' }
