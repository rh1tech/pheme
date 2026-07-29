#!/bin/sh
# Write the runtime config consumed by the SPA, and the security headers derived
# from it. nginx's image runs every script in /docker-entrypoint.d before starting
# nginx, so both are regenerated from the environment on each container start.
set -e

API_BASE="${PHEME_API_BASE:-}"
cat > /usr/share/nginx/html/config.js <<EOF
window.__PHEME_CONFIG = { apiBase: "${API_BASE}" };
EOF

# The API's ORIGIN (scheme://host[:port]) is what connect-src needs — a path is
# ignored in a source expression. Empty PHEME_API_BASE means same-origin, which
# 'self' already covers.
#
# `https*://` rather than `https\?://`: the latter is a GNU sed extension and this
# runs under busybox in the nginx:alpine image.
API_ORIGIN=$(printf '%s' "$API_BASE" | sed -n 's,^\(https*://[^/]*\).*,\1,p')

# Content-Security-Policy. This matters more here than in most apps: the tab holds
# the user's MLS private keys (IndexedDB) and tokens (localStorage), so one XSS —
# or one bad transitive dependency — is a break of the end-to-end encryption
# itself, not merely of a session.
#
#   script-src   'self' only, and this is the load-bearing directive: it is what
#                stops injected script from EXECUTING, which is upstream of
#                everything else. The theme block that used to be inline in
#                index.html now lives in /theme-init.js precisely so no
#                'unsafe-inline' and no hash-to-keep-in-sync is needed.
#                'wasm-unsafe-eval' is required to instantiate the MLS WASM module.
#   style-src    'unsafe-inline' is unavoidable: Mantine sets element styles and
#                injects a stylesheet at runtime.
#   frame-ancestors  'none' — nothing embeds this app. It supersedes
#                X-Frame-Options, which is kept alongside for agents predating
#                CSP Level 2.
#
# ON connect-src, AND WHY IT IS NOT TIGHT
#
# The obvious hardening is to allow only this deployment's API, so that injected
# script has nowhere to send the keys. It is not available to us: the server is a
# field on the sign-in form (src/lib/server.ts), because Pheme is federated and a
# person signing in to somebody else's instance must be able to say so. A fixed
# allowlist would break exactly the case the project exists for.
#
# So the default is `https:` — which blocks cleartext and data:/blob: destinations
# but does NOT stop exfiltration to an attacker's own HTTPS host. Be clear-eyed
# about that: against a successful injection, connect-src is not the defence here;
# script-src is.
#
# An operator running a single, closed deployment CAN have the tight version, and
# should: set PHEME_CSP_CONNECT_SRC to the exact origins, e.g.
#   PHEME_CSP_CONNECT_SRC="'self' https://api.example.com"
CONNECT_SRC="${PHEME_CSP_CONNECT_SRC:-'self' https: ${API_ORIGIN}}"
# Images and media come from whichever server the user signed in to, so they carry
# the same constraint as connect-src.
MEDIA_SRC="${PHEME_CSP_CONNECT_SRC:-'self' https: ${API_ORIGIN}}"

CSP="default-src 'self'; \
script-src 'self' 'wasm-unsafe-eval'; \
style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; \
font-src 'self' data: https://fonts.gstatic.com; \
img-src ${MEDIA_SRC} data: blob:; \
media-src ${MEDIA_SRC} blob:; \
connect-src ${CONNECT_SRC}; \
worker-src 'self' blob:; \
manifest-src 'self'; \
object-src 'none'; \
base-uri 'self'; \
form-action 'self'; \
frame-ancestors 'none'"

# `always` on every one of these. Without it nginx omits the header on error
# responses — which is exactly where an injected payload is just as happy to run.
cat > /etc/nginx/conf.d/pheme-security-headers.conf <<EOF
add_header Content-Security-Policy "${CSP}" always;
add_header X-Content-Type-Options "nosniff" always;
add_header X-Frame-Options "DENY" always;
add_header Referrer-Policy "strict-origin-when-cross-origin" always;
add_header Permissions-Policy "camera=(), microphone=(self), geolocation=(), payment=(), usb=()" always;
add_header Cross-Origin-Opener-Policy "same-origin" always;
add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;
EOF

echo "pheme: wrote /config.js with apiBase=${API_BASE}"
echo "pheme: wrote security headers (connect-src ${CONNECT_SRC})"
