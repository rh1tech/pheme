#!/usr/bin/env bash
#
# Cross-host federation end-to-end test. Stands up TWO full Pheme hosts, federated
# over a signed nodelist and real TLS, and drives the cross-host chat flow through
# the public client API — proving F5d (relay/forward/mirror) and F5b (signed
# ordering chain verified by the follower) against real, separately-running
# servers rather than in-process fakes.
#
# Fully self-contained in Docker: no host nginx, no DNS, no Cloudflare. A small
# fedproxy container terminates TLS for both host domains on an internal network
# and both apps trust its self-signed cert, so host-to-host S2S runs exactly as it
# would in production, on one machine. The two apps also publish a loopback port
# each so this script can drive their client API.
#
#   IMAGE=pheme-api:mytag ./run.sh     # test a prebuilt image
#   ./run.sh                            # build the image from ../../api first
#
# Requires: docker, curl, jq, openssl. Exits non-zero on the first failed check.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"
IMAGE="${IMAGE:-pheme-api:fedtest}"
PROJECT="${PROJECT:-phemefed}"        # compose project prefix + network name
HUB_DOMAIN="hub.fedtest.local"
FOL_DOMAIN="follower.fedtest.local"
HUB_PORT="${HUB_PORT:-18191}"
FOL_PORT="${FOL_PORT:-18291}"
NET="${PROJECT}net"
WORK="$(mktemp -d)"

log() { printf '\n\033[1;36m== %s ==\033[0m\n' "$*"; }
fail() { printf '\033[1;31mFAIL: %s\033[0m\n' "$*" >&2; exit 1; }

cleanup() {
  log "teardown"
  docker rm -f "${PROJECT}-fedproxy" >/dev/null 2>&1 || true
  docker compose -p "${PROJECT}-hub" -f "$WORK/compose.yml" --env-file "$WORK/hub.env" down -v >/dev/null 2>&1 || true
  docker compose -p "${PROJECT}-fol" -f "$WORK/compose.yml" --env-file "$WORK/fol.env" down -v >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

for tool in docker curl jq openssl; do command -v "$tool" >/dev/null || fail "missing required tool: $tool"; done

# --- build the image if not provided ------------------------------------------
if [ -z "${IMAGE_PREBUILT:-}" ] && ! docker image inspect "$IMAGE" >/dev/null 2>&1; then
  log "building $IMAGE from $ROOT/api"
  docker build -q --build-arg VERSION=fedtest -t "$IMAGE" "$ROOT/api" >/dev/null
fi

run_tool() { docker run --rm --entrypoint "$1" -v "$WORK/coord:/w" -w /w "$IMAGE" "${@:2}"; }

# --- keys + signed nodelist ---------------------------------------------------
log "coordinator key, host keys, signed nodelist"
mkdir -p "$WORK/coord" && chmod 777 "$WORK/coord"
run_tool /usr/local/bin/pheme-hostkey > "$WORK/coord/hub.txt"
run_tool /usr/local/bin/pheme-hostkey > "$WORK/coord/fol.txt"
HUB_PRIV=$(sed -n 's/.*PHEME_HOST_KEY): *//p' "$WORK/coord/hub.txt" | tr -d ' ')
HUB_PUB=$(sed -n 's/.*nodelist entry): *//p' "$WORK/coord/hub.txt" | tr -d ' ')
FOL_PRIV=$(sed -n 's/.*PHEME_HOST_KEY): *//p' "$WORK/coord/fol.txt" | tr -d ' ')
FOL_PUB=$(sed -n 's/.*nodelist entry): *//p' "$WORK/coord/fol.txt" | tr -d ' ')
run_tool /usr/local/bin/pheme-nodelist init >/dev/null
COORD_PUB=$(run_tool /usr/local/bin/pheme-nodelist pubkey | tr -d '\r')
run_tool /usr/local/bin/pheme-nodelist add "$HUB_DOMAIN" "$HUB_PUB" >/dev/null
run_tool /usr/local/bin/pheme-nodelist add "$FOL_DOMAIN" "$FOL_PUB" >/dev/null
run_tool /usr/local/bin/pheme-nodelist sign --days 1 > "$WORK/coord/nodelist.json"
grep -q '"sig"' "$WORK/coord/nodelist.json" || fail "nodelist did not sign"

# --- self-signed cert covering both host domains ------------------------------
log "S2S TLS cert"
mkdir -p "$WORK/tls"
cat > "$WORK/tls/san.cnf" <<CNF
[req]
distinguished_name = dn
x509_extensions = v3
prompt = no
[dn]
CN = pheme-fedtest
[v3]
basicConstraints = critical,CA:TRUE
keyUsage = critical,digitalSignature,keyCertSign
subjectAltName = @alt
[alt]
DNS.1 = $HUB_DOMAIN
DNS.2 = $FOL_DOMAIN
CNF
openssl req -x509 -newkey rsa:2048 -nodes -days 2 \
  -keyout "$WORK/tls/fed.key" -out "$WORK/tls/fed.crt" -config "$WORK/tls/san.cnf" >/dev/null 2>&1
chmod 644 "$WORK/tls/fed.crt" "$WORK/tls/fed.key"

# --- fedproxy config ----------------------------------------------------------
cat > "$WORK/tls/fedproxy.conf" <<CONF
server {
  listen 443 ssl;
  server_name $HUB_DOMAIN;
  ssl_certificate /fed.crt; ssl_certificate_key /fed.key;
  client_max_body_size 100M;
  location /.well-known/pheme-federation { proxy_pass http://${PROJECT}-hub-app-1:8080; proxy_set_header Host \$host; }
  location /federation/ { proxy_pass http://${PROJECT}-hub-app-1:8080; proxy_set_header Host \$host; proxy_set_header X-Forwarded-Proto https; }
}
server {
  listen 443 ssl;
  server_name $FOL_DOMAIN;
  ssl_certificate /fed.crt; ssl_certificate_key /fed.key;
  client_max_body_size 100M;
  location /.well-known/pheme-federation { proxy_pass http://${PROJECT}-fol-app-1:8080; proxy_set_header Host \$host; }
  location /federation/ { proxy_pass http://${PROJECT}-fol-app-1:8080; proxy_set_header Host \$host; proxy_set_header X-Forwarded-Proto https; }
}
CONF

# --- compose + env for each host ----------------------------------------------
cp "$ROOT/test/federation-e2e/compose.yml" "$WORK/compose.yml"
mk_env() { # domain hostkey port emailuser -> stdout
  cat <<EOF
IMAGE=$IMAGE
MONGO_USER=pheme
MONGO_PASS=$(openssl rand -hex 12)
PHEME_JWT_SECRET=$(openssl rand -hex 24)
PHEME_HOST_DOMAIN=$1
PHEME_HOST_KEY=$2
PHEME_NODELIST_COORD_KEY=$COORD_PUB
PHEME_PUBLIC_API_URL=https://$1
PHEME_SEED_ADMIN_EMAIL=$4@$1
PHEME_SEED_ADMIN_PASSWORD=$(openssl rand -hex 10)
APP_HOST_PORT=$3
FEDNET=$NET
EOF
}
mk_env "$HUB_DOMAIN" "$HUB_PRIV" "$HUB_PORT" alice > "$WORK/hub.env"
mk_env "$FOL_DOMAIN" "$FOL_PRIV" "$FOL_PORT" bob   > "$WORK/fol.env"
HUB_PASS=$(sed -n 's/PHEME_SEED_ADMIN_PASSWORD=//p' "$WORK/hub.env")
FOL_PASS=$(sed -n 's/PHEME_SEED_ADMIN_PASSWORD=//p' "$WORK/fol.env")

# nodelist + CA are mounted read-only into each app; the compose references them
export FED_NODELIST="$WORK/coord/nodelist.json" FED_CA="$WORK/tls/fed.crt"

# --- bring it all up ----------------------------------------------------------
log "starting hub + follower + fedproxy"
docker network create "$NET" >/dev/null 2>&1 || true
FED_NODELIST="$FED_NODELIST" FED_CA="$FED_CA" docker compose -p "${PROJECT}-hub" -f "$WORK/compose.yml" --env-file "$WORK/hub.env" up -d >/dev/null 2>&1
FED_NODELIST="$FED_NODELIST" FED_CA="$FED_CA" docker compose -p "${PROJECT}-fol" -f "$WORK/compose.yml" --env-file "$WORK/fol.env" up -d >/dev/null 2>&1
docker run -d --name "${PROJECT}-fedproxy" --network "$NET" \
  --network-alias "$HUB_DOMAIN" --network-alias "$FOL_DOMAIN" \
  -v "$WORK/tls/fedproxy.conf:/etc/nginx/conf.d/default.conf:ro" \
  -v "$WORK/tls/fed.crt:/fed.crt:ro" -v "$WORK/tls/fed.key:/fed.key:ro" \
  nginx:alpine >/dev/null

HUB=http://127.0.0.1:$HUB_PORT
FOL=http://127.0.0.1:$FOL_PORT

# --- wait for both apps to be listening ---------------------------------------
log "waiting for both hosts"
for base in "$HUB" "$FOL"; do
  for i in $(seq 1 60); do
    code=$(curl -s -o /dev/null -w '%{http_code}' "$base/v1/meta" || true)
    [ "$code" = "401" ] && break     # 401 = up and requiring auth (expected)
    [ "$i" = 60 ] && fail "$base never came up (last code $code)"
    sleep 1
  done
done

b64() { printf '%s' "$1" | base64; }
jqget() { jq -r "$1"; }

# --- the actual cross-host flow ----------------------------------------------
log "login alice@hub and bob@follower"
ATOK=$(curl -s "$HUB/v1/auth/login" -H 'content-type: application/json' -d "{\"email\":\"alice@$HUB_DOMAIN\",\"password\":\"$HUB_PASS\"}" | jqget .accessToken)
BRESP=$(curl -s "$FOL/v1/auth/login" -H 'content-type: application/json' -d "{\"email\":\"bob@$FOL_DOMAIN\",\"password\":\"$FOL_PASS\"}")
BTOK=$(echo "$BRESP" | jqget .accessToken); BID=$(echo "$BRESP" | jqget .userId)
[ -n "$ATOK" ] && [ "$ATOK" != null ] || fail "alice login failed"
[ -n "$BTOK" ] && [ "$BTOK" != null ] || fail "bob login failed"

log "alice creates a group and adds bob@follower (S2S mirror provision)"
CID=$(curl -s "$HUB/v1/conversations" -H "authorization: Bearer $ATOK" -H 'content-type: application/json' \
  -d '{"kind":"group","title":"fed-e2e","memberIds":[]}' | jqget .id)
[ -n "$CID" ] && [ "$CID" != null ] || fail "conversation not created"
addcode=$(curl -s -o /dev/null -w '%{http_code}' "$HUB/v1/conversations/$CID/members" -H "authorization: Bearer $ATOK" \
  -H 'content-type: application/json' -d "{\"userId\":\"mimi://$FOL_DOMAIN/u/$BID\"}")
[ "$addcode" = "201" ] || fail "add remote member returned $addcode (S2S provision failed)"

log "follower mirrors the conversation with the right hub"
hub_of_mirror=$(curl -s "$FOL/v1/conversations/$CID" -H "authorization: Bearer $BTOK" | jqget .hubDomain)
[ "$hub_of_mirror" = "$HUB_DOMAIN" ] || fail "mirror hubDomain=$hub_of_mirror, want $HUB_DOMAIN"

log "alice posts on the hub -> relayed to the follower's mirror"
curl -s -o /dev/null "$HUB/v1/conversations/$CID/messages" -H "authorization: Bearer $ATOK" -H 'content-type: application/json' \
  -d "{\"ciphertext\":\"$(b64 'hello-from-alice')\",\"contentType\":\"application/octet-stream\"}"
sleep 1
seen=$(curl -s "$FOL/v1/conversations/$CID/messages" -H "authorization: Bearer $BTOK" | jq -r '(if type=="array" then . else .messages end)[]?|(.ciphertext|@base64d)' | grep -c 'hello-from-alice' || true)
[ "$seen" -ge 1 ] || fail "alice's message did not reach the follower's mirror"

log "bob posts on the mirror -> forwarded to the hub -> back to both"
curl -s -o /dev/null "$FOL/v1/conversations/$CID/messages" -H "authorization: Bearer $BTOK" -H 'content-type: application/json' \
  -d "{\"ciphertext\":\"$(b64 'hi-from-bob')\",\"contentType\":\"application/octet-stream\"}"
sleep 1
at_hub=$(curl -s "$HUB/v1/conversations/$CID/messages" -H "authorization: Bearer $ATOK" | jq -r '(if type=="array" then . else .messages end)[]?|(.ciphertext|@base64d)' | grep -c 'hi-from-bob' || true)
[ "$at_hub" -ge 1 ] || fail "bob's forwarded message did not reach the hub"

log "bob commits on the mirror -> hub signs the ordering-chain link -> follower verifies it (F5b)"
ccode=$(curl -s -o /dev/null -w '%{http_code}' "$FOL/v1/conversations/$CID/mls/commit" -H "authorization: Bearer $BTOK" \
  -H 'content-type: application/json' -d "{\"groupId\":\"grp-e2e\",\"baseEpoch\":0,\"commit\":\"$(b64 'commit-bytes')\",\"welcome\":\"$(b64 'welcome-bytes')\"}")
[ "$ccode" = "200" ] || fail "forwarded commit returned $ccode"
sleep 1
hub_epoch=$(curl -s "$HUB/v1/conversations/$CID/mls" -H "authorization: Bearer $ATOK" | jqget .epoch)
fol_epoch=$(curl -s "$FOL/v1/conversations/$CID/mls" -H "authorization: Bearer $BTOK" | jqget .epoch)
[ "$hub_epoch" = "1" ] || fail "hub epoch=$hub_epoch, want 1"
# The follower only reaches epoch 1 if it recomputed the hub's chain hash AND
# verified the hub's signature — a mismatch or forged sig leaves it at 0.
[ "$fol_epoch" = "1" ] || fail "follower epoch=$fol_epoch, want 1 (signed ordering chain did not verify)"

log "PASS — cross-host relay (F5d) and signed ordering chain (F5b) verified on two live hosts"
