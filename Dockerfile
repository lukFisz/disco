# syntax=docker/dockerfile:1

FROM golang:1.22-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /out/disco .

FROM alpine:3.20

# ca-certificates: needed for the TLS connection to Discord's gateway/API.
# httpie: used by scripts/ha_trigger_automation.sh to call the Home
# Assistant REST API. curl: available for any custom scripts you point
# exec: at.
RUN apk add --no-cache ca-certificates httpie curl && \
    addgroup -S disco && adduser -S disco -G disco && \
    mkdir -p /data && chown disco:disco /data

WORKDIR /app
COPY --from=builder /out/disco ./disco
COPY --from=builder /src/scripts ./scripts
COPY --from=builder /src/config.yaml.example ./config.yaml.example

RUN chown -R disco:disco /app

USER disco

# /data is a separate, empty directory (not shadowed by a bind mount over
# /app) meant for persisting the audit log across restarts — point
# audit.file at /data/audit.log in your config.yaml if you mount a volume
# there. See docker-compose.yml and README.md.
VOLUME ["/data"]

ENTRYPOINT ["./disco"]
# No -config arg: the default ("config.yaml") already resolves relative to
# WORKDIR /app, i.e. /app/config.yaml — see docker-compose.yml, which
# mounts your local config.yaml exactly there.
