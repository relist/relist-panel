FROM node:24-alpine AS web-builder
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml ./
RUN corepack enable && pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

FROM golang:1.27-alpine AS go-builder
ARG VERSION=dev
# The release public key (base64 ed25519). Empty builds refuse every Update
# manifest; scripts/probe-panel-update.sh builds with a throwaway key.
ARG RELEASE_KEY=
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
# Every root .go file: main, its subcommands and helpers.
COPY *.go ./
# Embedded as the Install file the binary ships.
COPY docker-compose.yml ./
COPY internal/ ./internal/
COPY cmd/ ./cmd/
COPY --from=web-builder /src/internal/webassets/dist ./internal/webassets/dist
# The key is passed only when given: an empty -X would override the committed one.
RUN ldflags="-X main.version=${VERSION}"; \
    if [ -n "${RELEASE_KEY}" ]; then ldflags="${ldflags} -X relist-panel/internal/panelupdate.releasePublicKey=${RELEASE_KEY}"; fi; \
    CGO_ENABLED=0 go build -ldflags "${ldflags}" -o /out/relist-panel .
# The Volume helper is its own small binary — not a subcommand of the panel —
# because it must not link the panel's sqlite and net/http dependencies
# (the spec's weight measurement). /usr/local/bin/relist-volhelper is the
# path dockerexec.volume_runner.go invokes as the container entrypoint.
RUN CGO_ENABLED=0 go build -o /out/relist-volhelper ./cmd/relist-volhelper

# docker:*-cli already bundles the docker CLI and the docker compose plugin,
# which the panel shells out to over the mounted host socket (ADR-0002).
FROM docker:29-cli
# Picks up Alpine security fixes released after the base image was built.
RUN apk upgrade --no-cache
COPY --from=go-builder /out/relist-panel /usr/local/bin/relist-panel
COPY --from=go-builder /out/relist-volhelper /usr/local/bin/relist-volhelper
EXPOSE 8080
ENTRYPOINT ["relist-panel"]
