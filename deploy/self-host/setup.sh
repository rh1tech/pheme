#!/usr/bin/env bash
#
# Sets up a self-hosted Pheme instance: generates every secret, picks a decoy
# site, renders the nginx vhost, and prints the URL to hand to users.
#
#   ./setup.sh
#
# Safe to read before running. It writes node.env in this directory and prints
# an nginx config to stdout-adjacent files; it does not touch anything under
# /etc without telling you the command to run yourself.
#
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
env_file="$here/node.env"

say()  { printf '\n\033[1m%s\033[0m\n' "$*"; }
note() { printf '  %s\n' "$*"; }
die()  { printf '\nsetup: %s\n' "$*" >&2; exit 1; }

command -v openssl >/dev/null || die "openssl is required."
command -v docker  >/dev/null || die "docker is required."

if [[ -e "$env_file" ]]; then
  die "node.env already exists. Move it aside first — regenerating would mint a
     new PHEME_JWT_SECRET and sign every existing user out, and a new path
     prefix that no client knows."
fi

# --- What the operator has to decide -----------------------------------------

read -rp "Public hostname for this instance (e.g. talk.example.com): " api_host
[[ -n "$api_host" ]] || die "a hostname is required."
[[ "$api_host" =~ ^[a-zA-Z0-9.-]+$ ]] || die "that does not look like a hostname."

# SNI carries the hostname in the clear on every connection. A name containing
# "pheme" tells anything watching TLS what the host is, whatever the decoy says,
# and one wildcard rule blocks every instance that picked such a name. The path
# prefix cannot help with this; only the name can.
if [[ "$(printf '%s' "$api_host" | tr '[:upper:]' '[:lower:]')" == *pheme* ]]; then
  echo
  echo "  Warning: '$api_host' contains 'pheme'."
  echo "  The hostname travels in the clear in every TLS handshake, so this name"
  echo "  identifies the host regardless of the decoy site or the path prefix."
  echo "  A neutral name is the single most useful thing you can change."
  read -rp "  Continue anyway? [y/N]: " anyway
  [[ "$anyway" =~ ^[Yy] ]] || die "stopped. Pick a name that says nothing."
fi

read -rp "Admin email (gets the admin role on register): " admin_email
[[ -n "$admin_email" ]] || die "an admin email is required."

read -rp "Also serve the web app on this host? [y/N]: " want_web
want_web=${want_web:-n}

# --- Generated ----------------------------------------------------------------

say "Generating secrets"

# 8 random bytes: 64 bits, and it looks like the opaque path segments real sites
# are full of. Short or word-shaped prefixes are guessable, and render.sh
# rejects them.
path_prefix=$(openssl rand -hex 8)
note "path prefix     $path_prefix"

jwt_secret=$(openssl rand -base64 48 | tr -d '\n=' | tr '+/' '-_')
mongo_pass=$(openssl rand -base64 24 | tr -d '\n=' | tr '+/' '-_')
rabbit_pass=$(openssl rand -base64 24 | tr -d '\n=' | tr '+/' '-_')
note "jwt secret, database passwords: generated"

# VAPID keys for Web Push. cmd/vapidgen is in the API image, so this needs no
# local Go toolchain.
say "Generating Web Push (VAPID) keys"
api_image="${API_IMAGE:-ghcr.io/rh1tech/pheme-api:latest}"
vapid=$(docker run --rm --entrypoint /usr/local/bin/pheme-vapidgen "$api_image" -env) \
  || die "could not run vapidgen from $api_image. Pull the image first:
     docker pull $api_image"
vapid_public=$(sed -n 's/^PHEME_VAPID_PUBLIC=//p'  <<<"$vapid" | tr -d '\r')
vapid_private=$(sed -n 's/^PHEME_VAPID_PRIVATE=//p' <<<"$vapid" | tr -d '\r')
[[ -n "$vapid_public" && -n "$vapid_private" ]] \
  || die "vapidgen output was not in the expected form:
$vapid"
note "vapid keypair: generated"

# This instance's own signing identity. It signs the tokens this host issues,
# and when the host joins a network its PUBLIC half is its nodelist entry --
# what every other host uses to tell this host's word from a forgery.
say "Generating this instance's signing key"
host_key=$(docker run --rm --entrypoint /usr/local/bin/pheme-hostkey "$api_image" -env \
  | sed -n 's/^PHEME_HOST_KEY=//p' | tr -d '\r') \
  || die "could not run pheme-hostkey from $api_image"
[[ -n "$host_key" ]] || die "pheme-hostkey produced nothing"
note "host key: generated (keep it — losing it re-identifies this host)"

# A decoy that differs per deployment. The same decoy on every Pheme host would
# itself be the fingerprint the decoy exists to remove.
# Portable: find -printf and mapfile are GNU/bash-4 only, and a self-hoster on
# a BSD or a mac would hit both.
decoys=()
for d in "$here"/decoys/*/; do
  [[ -d "$d" ]] || continue
  decoys+=("$(basename "$d")")
done
[[ ${#decoys[@]} -gt 0 ]] || die "no decoy sites found in $here/decoys."
decoy=${decoys[$((RANDOM % ${#decoys[@]}))]}

say "Decoy site"
note "chose '$decoy' of ${#decoys[@]} available"
note "EDIT IT before you go live. A decoy that is recognisably one of the"
note "handful shipped with Pheme is a fingerprint, not a disguise."

api_base="https://$api_host/$path_prefix"
web_origin=""
if [[ "$want_web" =~ ^[Yy] ]]; then
  web_origin="https://$api_host"
fi

# --- node.env -----------------------------------------------------------------

umask 077
cat > "$env_file" <<EOF
# Generated by setup.sh on $(date -u +%Y-%m-%dT%H:%M:%SZ). Contains secrets.
#
# Losing PHEME_JWT_SECRET signs every user out. Losing PHEME_PATH_PREFIX makes
# the instance unreachable until you re-render nginx with a new one and tell
# every user. Losing PHEME_HOST_KEY changes who this host IS to every other
# host on the network. Back this file up.

PHEME_API_HOST=$api_host
PHEME_PATH_PREFIX=$path_prefix
PHEME_API_BASE=$api_base
PHEME_WEB_ORIGIN=$web_origin
PHEME_DECOY_DIR=$decoy

PHEME_JWT_SECRET=$jwt_secret

# This instance sits behind the nginx vhost rendered by setup.sh, which overwrites
# X-Forwarded-For — so the API can trust it to rate-limit per real client address.
PHEME_TRUST_PROXY_HEADERS=true
PHEME_ADMIN_EMAILS=$admin_email

PHEME_VAPID_PUBLIC=$vapid_public
PHEME_VAPID_PRIVATE=$vapid_private

# This instance's signing identity. Tokens are signed EdDSA under this key and
# stamped with PHEME_API_HOST as issuer, rather than HS256 under a shared
# secret -- which cannot survive federation, because a token's subject is a bare
# user id and two hosts sharing a secret would each accept the other's tokens.
PHEME_HOST_KEY=$host_key

MONGO_USER=pheme
MONGO_PASS=$mongo_pass
RABBIT_USER=pheme
RABBIT_PASS=$rabbit_pass

APP_HOST_PORT=8191
INGEST_HOST_PORT=8192
WEB_HOST_PORT=8190

# Verification codes print to the container log by default. Point these at a
# relay to send real mail; see README.md.
PHEME_MAIL_DRIVER=log
# PHEME_MAIL_DRIVER=smtp
# PHEME_SMTP_HOST=
# PHEME_SMTP_FROM=Pheme <noreply@$api_host>

# 1:1 voice calls need your own TURN relay. Empty disables calling.
PHEME_TURN_URLS=
PHEME_TURN_SECRET=
EOF

say "Wrote $env_file (mode 0600)"

# --- nginx --------------------------------------------------------------------

vhost="$here/$api_host.conf"
# shellcheck source=/dev/null  # generated just above
set -a; . "$env_file"; set +a
PHEME_SSL_SNIPPET="${PHEME_SSL_SNIPPET:-/etc/nginx/snippets/pheme-ssl.conf}" \
  "$here/../nginx/render.sh" > "$vhost"

say "Wrote $vhost"

# --- What is left for the operator -------------------------------------------

say "Next steps"
cat <<EOF
  1. Point DNS for $api_host at this host.

  2. Install the decoy site and the vhost:

       sudo cp -r $here/decoys/$decoy /var/www/$decoy
       sudo cp $vhost /etc/nginx/sites-available/$api_host.conf
       sudo ln -s /etc/nginx/sites-available/$api_host.conf /etc/nginx/sites-enabled/

  3. Get a certificate, and write the snippet the vhost includes:

       sudo certbot certonly --webroot -w /var/www/$decoy -d $api_host

       sudo tee /etc/nginx/snippets/pheme-ssl.conf >/dev/null <<'SNIP'
       ssl_certificate     /etc/letsencrypt/live/$api_host/fullchain.pem;
       ssl_certificate_key /etc/letsencrypt/live/$api_host/privkey.pem;
       ssl_protocols TLSv1.2 TLSv1.3;
       ssl_prefer_server_ciphers off;
       SNIP

       sudo nginx -t && sudo systemctl reload nginx

  4. Start the stack:

       docker compose --env-file node.env up -d$( [[ -n "$web_origin" ]] && printf ' --profile web' )

  5. Register $admin_email in the app. With PHEME_MAIL_DRIVER=log the
     verification code is in the container log, not your inbox:

       docker compose --env-file node.env logs app | grep -i code

  6. Check the disguise holds, from somewhere that is not this host:

       ./verify.sh https://$api_host $path_prefix
EOF

say "Hand this to your users"
note "$api_base"
if command -v qrencode >/dev/null; then
  printf '\n'
  qrencode -t ANSIUTF8 "$api_base"
  qrencode -o "$here/$api_host.png" -s 8 "$api_base"
  note "also written to $here/$api_host.png"
  note "In the app: Settings -> Server -> Scan."
else
  note "(install qrencode to also print a scannable QR code)"
  note "In the app: Settings -> Server -> paste the URL above."
fi
printf '\n'
