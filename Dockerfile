# SPDX-License-Identifier: AGPL-3.0-or-later
# Developer build. Published images use Dockerfile.release and tested archives.
FROM golang:1.26-alpine@sha256:27f2d3b83cfc5803c8078f8e730af0f7c431bf067afaec8f60a36fb763187cba AS build
WORKDIR /src
ENV GOWORK=off CGO_ENABLED=0
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=development
RUN go build -buildvcs=false -trimpath -ldflags="-s -w -X main.version=$VERSION" -o /out/imvault ./cmd/imvault

# SPDX-License-Identifier: AGPL-3.0-or-later
# Update the multi-platform base digest through a reviewed change. A runtime
# security update gets a new application patch release, preserving old tags.
FROM alpine:3.23@sha256:21f3e19bafab57a9ff7aa9dd571dac72f868370472c10f450cc0416cc1aca553
ARG VERSION=development
ARG REVISION=unknown
LABEL org.opencontainers.image.source="https://github.com/airencracken/imvault" \
      org.opencontainers.image.licenses="AGPL-3.0-or-later" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.revision="$REVISION"
RUN apk add --no-cache ca-certificates tzdata zstd ffmpeg \
    && addgroup -S -g 10001 imvault \
    && adduser -S -D -H -u 10001 -G imvault -h /data imvault \
    && mkdir /data \
    && chown 10001:10001 /data \
    && chmod 0700 /data
ENV IMVAULT_DATA_DIR=/data IMVAULT_ADDR=:8080 TMPDIR=/tmp
USER 10001:10001
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD port="${IMVAULT_ADDR##*:}"; wget -q -T 4 -O /dev/null "http://127.0.0.1:${port:-8080}/healthz" || exit 1
ENTRYPOINT ["/usr/local/bin/imvault"]
COPY --from=build --chown=0:0 /out/imvault /usr/local/bin/imvault
COPY LICENSE README.md /usr/share/doc/imvault/
