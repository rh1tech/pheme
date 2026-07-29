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
EXPOSE 80
