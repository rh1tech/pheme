/// <reference types="vite/client" />

/**
 * The id of the build this bundle came from, stamped in by vite.config.ts.
 *
 * Only defined by a real vite build or dev server. Read it through lib/version, which copes with
 * its absence — under vitest there is no define, and touching an undeclared global throws.
 */
declare const __BUILD_ID__: string

/** The release this bundle came from ('v0.9.19', or 'dev' for a local build). Same caveat. */
declare const __APP_VERSION__: string
