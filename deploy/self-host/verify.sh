#!/usr/bin/env bash
#
# Checks that a Pheme instance is working AND that it does not look like one.
#
#   ./verify.sh https://talk.example.com a7f3c91e4b2d
#
# Run it from somewhere that is not the server. Run it again after any nginx
# change: the disguise is the thing most likely to be lost quietly, because
# losing it breaks nothing that anyone would notice.
#
set -euo pipefail

origin=${1:-}
prefix=${2:-}
if [[ -z "$origin" || -z "$prefix" ]]; then
  echo "usage: $0 <https://host> <path-prefix>" >&2
  exit 2
fi
origin=${origin%/}
base="$origin/$prefix"

pass=0 fail=0
ok()   { printf '  \033[32mok\033[0m    %s\n' "$*"; pass=$((pass+1)); }
bad()  { printf '  \033[31mFAIL\033[0m  %s\n' "$*"; fail=$((fail+1)); }
code() { curl -s -o /dev/null -w '%{http_code}' --max-time 10 "$@"; }

printf '\n\033[1mThe instance works\033[0m\n'

c=$(code "$base/healthz")
[[ "$c" == 200 ]] && ok "API reachable at the prefix" \
                  || bad "API returned $c at $base/healthz (expected 200)"

c=$(code -X POST "$base/v1/auth/login" -H 'Content-Type: application/json' \
      -d '{"email":"probe@invalid.pheme.test","password":"not-a-real-password"}')
case "$c" in
  # 401 proves the request reached the app AND the app reached its database.
  # A 500 here is the failure a health check does not catch: the service is up
  # and every login on it is failing.
  401) ok "auth path alive (401 on bad credentials)" ;;
  500) bad "login returned 500 — the app is up but its datastore is not" ;;
  *)   bad "login returned $c (expected 401)" ;;
esac

printf '\n\033[1mIt does not look like one\033[0m\n'

# Everything a scanner would try. Each must be answered by the decoy, not the
# app. A 200 is as bad as an API error: it means something here is not static.
for probe in /healthz /v1/meta /v1/stream /version.json /v1/mls/key-packages /config.js; do
  c=$(code "$origin$probe")
  [[ "$c" == 404 ]] && ok "$probe -> 404 from the decoy" \
                    || bad "$probe -> $c without the prefix (expected 404)"
done

c=$(code -X POST "$origin/v1/auth/login" -H 'Content-Type: application/json' \
      -d '{"email":"probe@invalid.pheme.test","password":"x"}')
[[ "$c" == 404 ]] && ok "unprefixed login -> 404" \
                  || bad "unprefixed login -> $c: the API is reachable without the prefix"

c=$(code "$origin/")
[[ "$c" == 200 ]] && ok "the decoy site serves a page at /" \
                  || bad "/ returned $c — a host serving nothing at its root stands out"

# Response headers are part of the fingerprint. Anything here that a static
# nginx would not send says there is an application behind it.
printf '\n\033[1mHeaders give nothing away\033[0m\n'
headers=$(curl -sI --max-time 10 "$origin/" | tr -d '\r')
for leak in 'X-Accel-Buffering' 'Access-Control-Allow-Origin' 'X-Powered-By'; do
  grep -qi "^$leak:" <<<"$headers" \
    && bad "$leak is present on the decoy response" \
    || ok "no $leak header"
done

# A decoy every Pheme host serves identically is a fingerprint in its own right.
printf '\n\033[1mThe decoy is your own\033[0m\n'
body=$(curl -s --max-time 10 "$origin/")
if grep -qiE 'ardsley land surveying|northgate allotment|meridian brass' <<<"$body"; then
  bad "the decoy is still one of the shipped examples, unedited — edit it"
else
  ok "the decoy is not a shipped example verbatim"
fi

printf '\n%d passed, %d failed\n\n' "$pass" "$fail"
[[ "$fail" -eq 0 ]]
