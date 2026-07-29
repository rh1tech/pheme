# nginx vhosts

Host-level nginx terminates TLS and reverse-proxies into the containers, which
bind loopback only. These files are applied by hand — the deploy workflow ships
`docker-compose.yml`, `turnserver.conf` and `deploy.sh`, not nginx config.

## The API vhost is generated, not committed

`pheme-api.conf.template` + `render.sh` build the App API vhost. The rendered
file carries the deployment's path prefix, so it is never committed.

```sh
set -a; . /opt/pheme/stack.env; set +a
PHEME_API_HOST=chat.example.com \
PHEME_DECOY_DIR=example-decoy \
PHEME_SSL_SNIPPET=/etc/nginx/snippets/pheme-ssl.conf \
  ./render.sh | sudo tee /etc/nginx/sites-available/chat.example.com.conf
sudo nginx -t && sudo systemctl reload nginx
```

The destination differs by distribution. Check where your host loads virtual
hosts before copying the command above.

`render.sh` refuses to emit a config that still contains a placeholder, and
rejects a prefix that is short, guessable, or contains the hostname or the word
"pheme" — the prefix travels in every URL, so anything in it that identifies the
deployment gives away what it exists to hide.

## What the prefix buys, and what it does not

The API is mounted under a random path; everything else on the host is a static
decoy site. An unauthenticated prober — the automated kind that sweeps address
space to build blocklists — gets a small website and nginx's stock 404 for every
Pheme path it knows. It cannot distinguish this host from any other static host.

It is **not** a security boundary. Every endpoint behind it authenticates exactly
as before. It is **not** a secret either: it is in the URL of every request, so
any TLS-terminating middlebox in front of the host sees it. Treat it as
*unlisted*.

**And it does nothing about the hostname.** SNI can expose the hostname on a
connection, so a product-specific name may identify the deployment regardless
of the decoy. The prefix hides the endpoints; a neutral hostname can reduce the
deployment fingerprint. `setup.sh` refuses a hostname containing "pheme" for
this reason.

The three things that used to identify a Pheme host in one unauthenticated
request — a `/healthz` naming the service, an unauthenticated `/v1/meta`, and a
wildcard CORS header — were removed separately (see `internal/httpx`,
`internal/channel/app.go`, `cmd/app/main.go`). The prefix is what makes the rest
of the surface unreachable; those changes are what stop the surface talking if
someone finds it.

## The decoy must be real, and must differ per deployment

A blank page, nginx's default welcome, or the same decoy on every Pheme host are
each a fingerprint in their own right. Serve a plausible small site, and do not
use the same one as anyone else. `deploy/self-host/decoys/` carries several
unrelated starting points.

## The web SPA stays at `/` on its own host

Only the API is prefixed. The SPA's assets are root-relative (`vite.config.ts`
sets no `base`), so serving it under a path would require rebuilding per
deployment and would defeat the runtime-config design in `web/deploy/`.

This is a real limit: a publicly served web app is identifiable by its content
no matter where it sits, so a deployment that wants the host to look like
nothing in particular should serve the API and the decoy only, and point users
at the mobile app. `deploy/self-host/` defaults to exactly that, with the web
app opt-in.

## Changing the prefix

Rotate it as one step, or the deployment splits in half:

1. `PHEME_PATH_PREFIX` and `PHEME_API_BASE` in `/opt/pheme/<env>/stack.env`
2. re-render and reload nginx
3. `PROD_API_URL` in the repo's GitHub secrets (the deploy smoke test)
4. restart the stack so the web container rewrites `/config.js`

Mobile clients hold the base URL in secure storage and will keep using the old
one until the user updates it — so if you rotate, keep the previous prefix
mounted alongside the new one until they have.
