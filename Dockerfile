# Build stage
FROM golang:1.23-alpine AS builder

RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -ldflags="-s -w" -o /myclaw .

# Runtime stage
FROM alpine:3.20

RUN apk add --no-cache ca-certificates sqlite-libs tzdata

WORKDIR /app
COPY --from=builder /myclaw .

EXPOSE 8080

ENV CHANNEL_HTTP_ADDR=:8080 \
    CHANNEL_SQLITE_PATH=/app/data/channel.db

VOLUME ["/app/data"]

ENTRYPOINT ["./myclaw"]
