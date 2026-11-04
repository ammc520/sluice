# Build the Sluice binary with the Go toolchain, then ship it on a small
# Alpine base so the container has ca-certificates, tzdata, and wget for
# Docker health checks. The process runs as an unprivileged user.
FROM golang:1.23-alpine AS build

# Allow builds from networks where proxy.golang.org is unreachable. Module
# checksums are still verified against go.sum.
ARG GOPROXY=https://goproxy.cn,https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/sluice \
    ./cmd/sluice \
    && CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/migrate \
    ./cmd/migrate

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata wget \
    && addgroup -S -g 10001 sluice \
    && adduser -S -D -H -u 10001 -G sluice sluice \
    && mkdir -p /app /var/lib/sluice/spool \
    && chown -R sluice:sluice /app /var/lib/sluice

ENV TZ=Asia/Shanghai

WORKDIR /app

COPY --from=build /out/sluice /app/sluice
COPY --from=build /out/migrate /app/migrate
# Migrations are embedded in both binaries; the files are kept in the image
# for incident review.
COPY --from=build /src/migrations /app/migrations

USER sluice

EXPOSE 8080

ENTRYPOINT ["/app/sluice"]
