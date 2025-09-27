# syntax=docker/dockerfile:1.7-labs

# Multi-stage build to produce a tiny, static image
ARG GO_VERSION=1.25.1

FROM golang:${GO_VERSION}-alpine AS builder
ARG TARGETOS
ARG TARGETARCH
ARG APP_NAME=svc-downloader
ARG MAIN_PKG=./cmd/server
ENV CGO_ENABLED=0
RUN apk add --no-cache ca-certificates tzdata upx
WORKDIR /src
# Cache modules
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
# Copy the rest of the source
COPY . .
# Build for the requested target
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/${APP_NAME} ${MAIN_PKG} && \
    upx -q --best /out/${APP_NAME} || true

FROM scratch AS runtime
ARG APP_NAME=svc-downloader
# Minimal CA certs and timezone data for TLS and time formatting
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /out/${APP_NAME} /app/${APP_NAME}
# Run as non-root
USER 65532:65532
ENTRYPOINT ["/app/svc-downloader"]

