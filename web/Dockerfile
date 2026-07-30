# Build the SPA, then serve the static files with nginx. The API base URL is not
# baked in: it is written to /config.js at container start from PHEME_API_BASE,
# so the same image works for every environment.

FROM node:22-bookworm AS build
WORKDIR /app
COPY package.json package-lock.json ./
# The npm download cache, kept out of the image. The layer above already skips `npm ci` entirely
# while the lockfile is unchanged; this is for the case that actually costs time — a dependency
# bump, where without it every package is fetched from the network again.
RUN --mount=type=cache,target=/root/.npm npm ci
COPY . .
# The release to stamp into the bundle (deploy.yml passes the git tag). Declared here, after the
# dependency layers, so a new version does not invalidate `npm ci` — only the build below, which is
# exactly what must re-run: a cached bundle would ship the previous release's version and build id.
ARG PHEME_VERSION=dev
ENV PHEME_VERSION=${PHEME_VERSION}
RUN npm run build

FROM nginx:1.27-alpine
# Privilege model: the nginx master process binds port 80 as root (required by the OS for
# ports < 1024). Worker processes immediately drop to the `nginx` user defined in the base
# image's nginx.conf. Served files are copied below with default root:root ownership and
# 644 permissions — workers read them but cannot write. This is the standard nginx-alpine
# privilege split; a non-root master would require a higher port and a reverse-proxy in front.
COPY --from=build /app/dist /usr/share/nginx/html
COPY deploy/nginx.conf /etc/nginx/conf.d/default.conf
COPY deploy/docker-entrypoint.sh /docker-entrypoint.d/40-pheme-config.sh
RUN chmod +x /docker-entrypoint.d/40-pheme-config.sh
# A restrictive fallback so the config always parses, even if the entrypoint is
# bypassed. The entrypoint overwrites it at start with the policy that names this
# deployment's API. Erring same-origin-only means a misconfigured container fails
# visibly rather than serving with no policy at all.
RUN printf '%s\n' \
    "add_header Content-Security-Policy \"default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'\" always;" \
    "add_header X-Content-Type-Options \"nosniff\" always;" \
    "add_header X-Frame-Options \"DENY\" always;" \
    "add_header Referrer-Policy \"strict-origin-when-cross-origin\" always;" \
    > /etc/nginx/conf.d/pheme-security-headers.conf
# 127.0.0.1, NOT localhost. nginx binds 0.0.0.0:80 — IPv4 only — while `localhost` inside this
# image resolves to ::1 first, so the probe was refused on every run and the container reported
# unhealthy from the moment this healthcheck was added, with nginx serving perfectly throughout.
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1/ || exit 1
EXPOSE 80
