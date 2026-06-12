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
#
# NOTE: ARGs do not cross build stages, so the version/arch ARGs are
# (re)declared inside each stage that consumes them. The extracted
# binary is resolved by glob because these release tarballs unpack to
# an arch-suffixed name (e.g. codex-x86_64-unknown-linux-musl), not a
# bare "codex".
# ------------------------------------------------------------------
FROM builder AS with-codex
ARG TARGETARCH
ARG CODEX_VERSION=rust-v0.137.0
RUN set -eux; \
  CODEX_ARCH=$(if [ "$TARGETARCH" = "amd64" ]; then echo "x86_64-unknown-linux-musl"; else echo "aarch64-unknown-linux-musl"; fi); \
  curl -fsSL "https://github.com/openai/codex/releases/download/${CODEX_VERSION}/codex-${CODEX_ARCH}.tar.gz" -o /tmp/codex.tar.gz; \
  mkdir -p /tmp/codex-x; tar -xzf /tmp/codex.tar.gz -C /tmp/codex-x; \
  bin=$(find /tmp/codex-x -type f -name 'codex*' | head -n1); \
  install -m 0755 "$bin" /usr/local/bin/codex; \
  rm -rf /tmp/codex.tar.gz /tmp/codex-x

FROM builder AS with-opencode
ARG TARGETARCH
ARG OPENCODE_VERSION=v1.16.2
RUN set -eux; \
  OPENCODE_ARCH=$(if [ "$TARGETARCH" = "amd64" ]; then echo "linux-x64-musl"; else echo "linux-arm64-musl"; fi); \
  curl -fsSL "https://github.com/sst/opencode/releases/download/${OPENCODE_VERSION}/opencode-${OPENCODE_ARCH}.tar.gz" -o /tmp/opencode.tar.gz; \
  mkdir -p /tmp/opencode-x; tar -xzf /tmp/opencode.tar.gz -C /tmp/opencode-x; \
  bin=$(find /tmp/opencode-x -type f -name 'opencode*' | head -n1); \
  install -m 0755 "$bin" /usr/local/bin/opencode; \
  rm -rf /tmp/opencode.tar.gz /tmp/opencode-x

FROM builder AS with-cc
ARG TARGETARCH
ARG CLAUDE_VERSION=v2.1.168
RUN set -eux; \
  CLAUDE_ARCH=$(if [ "$TARGETARCH" = "amd64" ]; then echo "linux-x64-musl"; else echo "linux-arm64-musl"; fi); \
  curl -fsSL "https://github.com/anthropics/claude-code/releases/download/${CLAUDE_VERSION}/claude-${CLAUDE_ARCH}.tar.gz" -o /tmp/claude.tar.gz; \
  mkdir -p /tmp/claude-x; tar -xzf /tmp/claude.tar.gz -C /tmp/claude-x; \
  bin=$(find /tmp/claude-x -type f -name 'claude*' | head -n1); \
  install -m 0755 "$bin" /usr/local/bin/claude; \
  rm -rf /tmp/claude.tar.gz /tmp/claude-x

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
