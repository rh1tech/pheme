# Build the SPA, then serve the static files with nginx. The API base URL is not
# baked in: it is written to /config.js at container start from PHEME_API_BASE,
# so the same image works for every environment.

FROM node:22-bookworm AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
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
EXPOSE 80
