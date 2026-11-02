# Build the Vue admin SPA, then serve it from Caddy. The same Caddy process
# terminates TLS and proxies /v1 and /admin/api to the gateway container.
FROM node:24-alpine AS build

WORKDIR /src

COPY web/package.json web/package-lock.json ./
RUN npm ci

COPY web/ ./
RUN npm run build

FROM caddy:2-alpine

# Serve TLS and static files as an unprivileged user. The file capability
# allows binding 80/443 without running the container as root.
RUN addgroup -S -g 10002 caddyuser \
    && adduser -S -D -H -u 10002 -G caddyuser caddyuser \
    && chown -R caddyuser:caddyuser /data /config \
    && setcap cap_net_bind_service=+ep /usr/bin/caddy

COPY deploy/Caddyfile /etc/caddy/Caddyfile
COPY --from=build /src/dist /srv

USER caddyuser

EXPOSE 80 443
