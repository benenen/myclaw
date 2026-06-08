# Build stage
FROM golang:1.23-alpine AS builder

RUN apk add --no-cache gcc musl-dev sqlite-dev curl

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -ldflags="-s -w" -o /myclaw .

# ------------------------------------------------------------------
# Tool downloads
# ------------------------------------------------------------------
ARG TARGETARCH
ARG CODEX_VERSION=rust-v0.137.0
ARG OPENCODE_VERSION=v1.16.2
ARG CLAUDE_VERSION=v2.1.168

FROM builder AS with-codex
RUN \
  CODEX_ARCH=$(if [ "$TARGETARCH" = "amd64" ]; then echo "x86_64-unknown-linux-musl"; else echo "aarch64-unknown-linux-musl"; fi); \
  curl -fsSL "https://github.com/openai/codex/releases/download/${CODEX_VERSION}/codex-${CODEX_ARCH}.tar.gz" -o /tmp/codex.tar.gz && \
  tar -xzf /tmp/codex.tar.gz -C /tmp && \
  mv /tmp/codex /usr/local/bin/codex && \
  chmod +x /usr/local/bin/codex && \
  rm /tmp/codex.tar.gz

FROM builder AS with-opencode
RUN \
  OPENCODE_ARCH=$(if [ "$TARGETARCH" = "amd64" ]; then echo "linux-x64-musl"; else echo "linux-arm64-musl"; fi); \
  curl -fsSL "https://github.com/sst/opencode/releases/download/${OPENCODE_VERSION}/opencode-${OPENCODE_ARCH}.tar.gz" -o /tmp/opencode.tar.gz && \
  tar -xzf /tmp/opencode.tar.gz -C /tmp && \
  mv /tmp/opencode /usr/local/bin/opencode && \
  chmod +x /usr/local/bin/opencode && \
  rm /tmp/opencode.tar.gz

FROM builder AS with-cc
RUN \
  CLAUDE_ARCH=$(if [ "$TARGETARCH" = "amd64" ]; then echo "linux-x64-musl"; else echo "linux-arm64-musl"; fi); \
  curl -fsSL "https://github.com/anthropics/claude-code/releases/download/${CLAUDE_VERSION}/claude-${CLAUDE_ARCH}.tar.gz" -o /tmp/claude.tar.gz && \
  tar -xzf /tmp/claude.tar.gz -C /tmp && \
  mv /tmp/claude /usr/local/bin/claude && \
  chmod +x /usr/local/bin/claude && \
  rm /tmp/claude.tar.gz

# ------------------------------------------------------------------
# Runtime base
# ------------------------------------------------------------------
FROM alpine:3.20 AS runtime-base
RUN apk add --no-cache ca-certificates sqlite-libs tzdata ripgrep
WORKDIR /app
COPY --from=builder /myclaw .
EXPOSE 8080
ENV CHANNEL_HTTP_ADDR=:8080 CHANNEL_SQLITE_PATH=/app/data/channel.db
VOLUME ["/app/data"]
ENTRYPOINT ["./myclaw"]

# ------------------------------------------------------------------
# Variants
# ------------------------------------------------------------------
FROM runtime-base AS myclaw-codex
COPY --from=with-codex /usr/local/bin/codex /usr/local/bin/

FROM runtime-base AS myclaw-opencode
COPY --from=with-opencode /usr/local/bin/opencode /usr/local/bin/

FROM runtime-base AS myclaw-cc
COPY --from=with-cc /usr/local/bin/claude /usr/local/bin/
