FROM node:24-alpine AS web-builder
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml ./
RUN corepack enable && pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

FROM golang:1.27-alpine AS go-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
COPY internal/ ./internal/
COPY --from=web-builder /src/internal/webassets/dist ./internal/webassets/dist
RUN CGO_ENABLED=0 go build -o /out/relist-panel .

# docker:*-cli already bundles the docker CLI and the docker compose plugin,
# which the panel shells out to over the mounted host socket (ADR-0002).
FROM docker:27-cli
COPY --from=go-builder /out/relist-panel /usr/local/bin/relist-panel
EXPOSE 8080
ENTRYPOINT ["relist-panel"]
