FROM node:22-alpine AS ui
WORKDIR /source
ARG NPM_REGISTRY=https://registry.npmjs.org
COPY --from=admin-ui / /source/
RUN --mount=type=cache,target=/root/.npm npm ci \
    --ignore-scripts --no-audit --no-fund --registry=$NPM_REGISTRY
RUN npm run build

FROM golang:1.26.8-alpine AS build
WORKDIR /source
ARG GOPROXY=https://proxy.golang.org,direct
ENV CGO_ENABLED=0
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . ./
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /admin ./cmd/admin

FROM build AS verify
RUN apk add --no-cache gcc musl-dev redis
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    redis-server --bind 127.0.0.1 --port 16389 --save '' --appendonly no --daemonize yes && \
    TELEMETRY_TEST_REDIS=127.0.0.1:16389 UPDATE_PEER_TEST_REDIS=127.0.0.1:16389 \
    CGO_ENABLED=1 go test -race ./... && go vet ./...

FROM scratch AS runtime
WORKDIR /app
ENV NODE_ENV=production LISTEN_PORT=8080
COPY --from=ui /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=ui /source/dist ./public
COPY --from=build /admin /admin
EXPOSE 8080 3478/udp
ENTRYPOINT ["/admin"]
