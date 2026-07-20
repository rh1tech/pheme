#!/usr/bin/env bash
#
# Renders pheme-api.conf.template into a usable nginx vhost.
#
#   ./render.sh > /etc/nginx/sites-available/api.example.com.conf
#
# Every value comes from the environment so the rendered file — which contains
# the path prefix — never has to be committed anywhere. Reads the same variable
# names the Pheme stack.env uses, so you can source that file first:
#
#   set -a; . /opt/pheme/prod/stack.env; set +a; ./render.sh
#
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
template="$here/pheme-api.conf.template"

: "${PHEME_API_HOST:?set PHEME_API_HOST, e.g. api.example.com}"
: "${PHEME_PATH_PREFIX:?set PHEME_PATH_PREFIX — generate one with: openssl rand -hex 8}"
: "${PHEME_DECOY_DIR:?set PHEME_DECOY_DIR, the directory under /var/www holding the decoy site}"
: "${PHEME_SSL_SNIPPET:?set PHEME_SSL_SNIPPET, e.g. /etc/nginx/snippets/ssl.conf}"
APP_HOST_PORT="${APP_HOST_PORT:-8191}"
INGEST_HOST_PORT="${INGEST_HOST_PORT:-8192}"

# A prefix that is short, or made of words, is one a scanner can guess or that a
# human can recognise on sight. Eight random hex bytes is 64 bits and looks like
# the opaque path segments real sites are full of.
if [[ ! "$PHEME_PATH_PREFIX" =~ ^[A-Za-z0-9_-]{12,}$ ]]; then
  echo "render.sh: PHEME_PATH_PREFIX must be at least 12 URL-safe characters." >&2
  echo "           Generate one with: openssl rand -hex 8" >&2
  exit 1
fi

# The prefix appears in the URL of every request. Reusing something that already
# identifies the deployment gives away exactly what the prefix exists to hide.
for leak in "$PHEME_API_HOST" pheme Pheme PHEME; do
  if [[ "$PHEME_PATH_PREFIX" == *"$leak"* ]]; then
    echo "render.sh: PHEME_PATH_PREFIX must not contain '$leak' — it travels in every URL." >&2
    exit 1
  fi
done

rendered=$(
  sed \
    -e "s|__PHEME_API_HOST__|${PHEME_API_HOST}|g" \
    -e "s|__PHEME_PATH_PREFIX__|${PHEME_PATH_PREFIX}|g" \
    -e "s|__PHEME_DECOY_DIR__|${PHEME_DECOY_DIR}|g" \
    -e "s|__PHEME_SSL_SNIPPET__|${PHEME_SSL_SNIPPET}|g" \
    -e "s|__PHEME_APP_PORT__|${APP_HOST_PORT}|g" \
    -e "s|__PHEME_INGEST_PORT__|${INGEST_HOST_PORT}|g" \
    "$template"
)

# The whole point of the prefix is defeated by a config that still carries a
# placeholder, and nginx would start on one without complaint: __PHEME_PATH_
# PREFIX__ is a perfectly valid location. Fail loudly instead.
if grep -q '__[A-Z_]*__' <<<"$rendered"; then
  echo "render.sh: unsubstituted placeholders remain:" >&2
  grep -o '__[A-Z_]*__' <<<"$rendered" | sort -u >&2
  exit 1
fi

printf '%s\n' "$rendered"
