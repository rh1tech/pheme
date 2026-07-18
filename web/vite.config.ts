import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'

/**
 * One id per build, stamped into the bundle AND written to /version.json.
 *
 * A tab therefore knows the id of the code it is running, and can ask the server which id is
 * deployed; when the two differ, the tab is out of date and offers to reload (lib/version).
 *
 * A timestamp rather than a hash of the output, because the id must be known while the bundle is
 * being written — it is compiled INTO it — and the output's content hashes do not exist yet at that
 * point. The cost is that rebuilding identical code mints a new id; since a build only reaches
 * anyone by being deployed, "a new build is live" is what we would want to say anyway.
 */
const buildId = Date.now().toString(36)

/**
 * The human-readable release, for showing a person which Pheme they are running.
 *
 * Separate from buildId and not a substitute for it: this is what someone reads out in a bug
 * report, while buildId is what the staleness check compares. They answer different questions and
 * a release can be rebuilt.
 *
 * Comes from the CI tag via a Docker build arg (deploy.yml -> Dockerfile), because the bundle is
 * built inside the image where there is no git and no tag to look at. 'dev' for a local build is
 * the honest answer: it is not a release.
 */
const version = process.env.PHEME_VERSION?.trim() || 'dev'

/** Writes the deployed ids where a running tab can poll for them. */
function versionManifest(): Plugin {
  return {
    name: 'pheme-version-manifest',
    apply: 'build',
    generateBundle() {
      this.emitFile({
        type: 'asset',
        fileName: 'version.json',
        source: `${JSON.stringify({ buildId, version })}\n`,
      })
    },
  }
}

/**
 * Serves the MLS WASM binary at a fixed URL for the service worker.
 *
 * The worker decrypts message previews and so needs the same crypto the app runs — but it is a
 * classic script outside the bundle, so it cannot import the hashed asset Vite emits. It fetches
 * /mls/pheme_mls_bg.wasm instead.
 *
 * This copies rather than duplicates on purpose. Committing a second 1.2 MB binary under public/
 * would work today and drift tomorrow: two copies of the crypto, one rebuilt and one forgotten,
 * with the worker quietly decrypting against a different build than the page. There is one file in
 * the repo, and it is the one wasm-pack writes.
 */
function serviceWorkerWasm(): Plugin {
  const wasmPath = fileURLToPath(new URL('./src/crypto/pkg/pheme_mls_bg.wasm', import.meta.url))
  const url = '/mls/pheme_mls_bg.wasm'
  return {
    name: 'pheme-sw-wasm',
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        if (req.url?.split('?')[0] !== url) return next()
        res.setHeader('Content-Type', 'application/wasm')
        res.end(readFileSync(wasmPath))
      })
    },
    generateBundle() {
      this.emitFile({
        type: 'asset',
        fileName: 'mls/pheme_mls_bg.wasm',
        source: readFileSync(wasmPath),
      })
    },
  }
}

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), versionManifest(), serviceWorkerWasm()],
  define: {
    __BUILD_ID__: JSON.stringify(buildId),
    __APP_VERSION__: JSON.stringify(version),
  },
})
