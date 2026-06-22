#!/bin/sh
# Write the runtime config consumed by the SPA. nginx's image runs every script
# in /docker-entrypoint.d before starting nginx, so this regenerates config.js
# from the environment on each container start.
set -e

API_BASE="${PHEME_API_BASE:-}"
cat > /usr/share/nginx/html/config.js <<EOF
window.__PHEME_CONFIG = { apiBase: "${API_BASE}" };
EOF

echo "pheme: wrote /config.js with apiBase=${API_BASE}"
