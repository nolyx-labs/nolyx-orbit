# Nolyx Orbit control plane: API + web UI + agent gateway + worker, plus agent binaries for /install.sh.
FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/orbit ./cmd/orbit \
 && for arch in amd64 arm64; do \
      CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.version=$VERSION" \
        -o /out/agents/orbit-agent-linux-$arch ./cmd/orbit-agent; \
    done

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go /out/orbit /usr/local/bin/orbit
COPY --from=go /out/agents /opt/orbit/agents
ENV LISTEN_ADDR=:8080 AGENT_DIST=/opt/orbit/agents
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/orbit"]
