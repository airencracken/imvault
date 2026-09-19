# Build stage. The SQLite driver is pure Go, so no cgo toolchain is needed.
FROM golang:1.27-alpine AS build

WORKDIR /src

# Cache dependencies separately from the source.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/imvault ./cmd/imvault

# Runtime stage. Alpine (rather than a distroless image) is used because
# multipart uploads spill to a temporary directory, and /tmp must be writable.
#
# ffmpeg provides clip poster frames and duration checks. It is optional: drop
# it from this line (and set IMVAULT_FFMPEG/IMVAULT_FFPROBE to nothing) to
# shrink the image, and clips will be accepted with placeholder posters.
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata ffmpeg \
 && adduser -D -u 10001 -h /data imvault \
 && chown -R imvault:imvault /data

COPY --from=build /out/imvault /usr/local/bin/imvault

# Temporary files go to /tmp, deliberately not to /data.
#
# A multipart body past 8 MiB spills to TMPDIR, and one request may carry up to
# twenty files. Leaving that on /data put a runaway spill and the SQLite
# database on the same filesystem, so filling the first took the second with it.
# Keeping them apart means a spill that runs out of room fails an upload rather
# than the instance.
ENV IMVAULT_DATA_DIR=/data \
    IMVAULT_ADDR=:8080 \
    TMPDIR=/tmp

VOLUME ["/data"]
EXPOSE 8080

USER imvault
ENTRYPOINT ["/usr/local/bin/imvault"]
