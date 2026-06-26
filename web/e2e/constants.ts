// Shared constants for the E2E suite and Playwright config. The admin credentials
// match the PHEME_SEED_ADMIN_* env passed to the API web server in
// playwright.config.ts, so the seeded admin can always log in.

// Dedicated ports for the E2E stack, chosen to avoid colliding with a running
// `make dev` stack (App API :8080, Vite :5173).
export const WEB_PORT = 4317
export const API_PORT = 8099
export const WEB_URL = `http://localhost:${WEB_PORT}`
export const API_URL = `http://localhost:${API_PORT}`

export const ADMIN_EMAIL = 'admin@pheme.test'
export const ADMIN_PASSWORD = 'Admin12345'
