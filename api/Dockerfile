# Build all three Pheme service binaries into one small image. The compose file
# selects which binary each container runs via `entrypoint`.

FROM golang:1.25-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
ARG VERSION=dev
# The two cache mounts are what make this quick, and their absence is what made it slow: the
# layer cache above only helps while go.mod is untouched, and it never covered the COMPILER's
# output. Every release therefore rebuilt every package of every dependency from source — around
# 160 seconds of a four-minute deploy, spent recompiling code that had not changed.
#
# /root/.cache/go-build keeps the compiled package objects; /go/pkg/mod keeps the module sources.
# Neither is baked into a layer, so nothing here bloats the image, and both survive between builds
# in the daemon's BuildKit state (see deploy.yml, which deliberately uses a persistent builder —
# a fresh builder per run would throw these away and put the 160 seconds straight back).
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/pheme-app ./cmd/app \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/pheme-ingest ./cmd/ingest \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/pheme-dispatcher ./cmd/dispatcher

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/pheme-app /out/pheme-ingest /out/pheme-dispatcher /usr/local/bin/
USER nonroot:nonroot
# Default to the App API; ingest and dispatcher override entrypoint in compose.
ENTRYPOINT ["/usr/local/bin/pheme-app"]
