# Build all three Pheme service binaries into one small image. The compose file
# selects which binary each container runs via `entrypoint`.

FROM golang:1.25-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/pheme-app ./cmd/app \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/pheme-ingest ./cmd/ingest \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/pheme-dispatcher ./cmd/dispatcher

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/pheme-app /out/pheme-ingest /out/pheme-dispatcher /usr/local/bin/
USER nonroot:nonroot
# Default to the App API; ingest and dispatcher override entrypoint in compose.
ENTRYPOINT ["/usr/local/bin/pheme-app"]
