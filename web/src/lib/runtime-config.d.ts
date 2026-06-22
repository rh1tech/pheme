// Runtime configuration injected by the web container at startup (or the dev
// default in public/config.js). See src/lib/api.ts.
export {}

declare global {
  interface Window {
    __PHEME_CONFIG?: {
      apiBase?: string
    }
  }
}
