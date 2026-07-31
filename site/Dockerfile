# The marketing site: static files and nginx. No build step and nothing to compile — the HTML in
# this directory is the artefact, which is the point. A landing page that needs a toolchain to
# render is a landing page that breaks in three years when the toolchain does.
FROM nginx:1.27-alpine

# Privilege model matches the web container: the master binds port 80 as root (the OS requires it
# for ports < 1024) and workers drop to the `nginx` user. Files are copied root-owned and
# world-readable, so workers can read them and cannot write them.
COPY --chown=root:root . /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf

# The container image is not the source tree. Anything that is deployment machinery rather than
# something a browser should be able to fetch is removed after the copy.
RUN rm -f /usr/share/nginx/html/Dockerfile \
          /usr/share/nginx/html/nginx.conf \
          /usr/share/nginx/html/README.md \
    && find /usr/share/nginx/html -type f -exec chmod 644 {} + \
    && find /usr/share/nginx/html -type d -exec chmod 755 {} +

EXPOSE 80

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
  CMD wget -qO /dev/null http://127.0.0.1/ || exit 1
