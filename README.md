# myclaw

AI CLI channel plugin — centralized channel management service.

## Requirements

- Go 1.23+
- SQLite (via CGO)

## Environment Variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `CHANNEL_MASTER_KEY` | Yes | — | Base64-encoded 32-byte key for AES-256-GCM credential encryption |
| `CHANNEL_HTTP_ADDR` | No | `:8080` | HTTP listen address |
| `CHANNEL_SQLITE_PATH` | No | `channel.db` | SQLite database file path |
| `WECHAT_REFERENCE_BASE_URL` | No | `http://localhost:9090` | WeChat reference adapter base URL |
| `WECHAT_AUTH_TOKEN` | No | — | Optional auth token for WeChat adapter |

## Run Locally

```bash
# Generate a master key
export CHANNEL_MASTER_KEY=$(openssl rand -base64 32)

# Start the server
go run .

# Or explicitly run the server command
go run . server
```

## Run Tests

```bash
go test ./...
```

## API Endpoints

All endpoints are under `/api/v1` and use `GET`/`POST` only.

### Create Binding Session

```bash
curl -X POST http://localhost:8080/api/v1/channel-bindings/create \
  -H "Content-Type: application/json" \
  -d '{"user_id":"u_123","channel_type":"wechat"}'
```

### Poll Binding Detail

```bash
curl http://localhost:8080/api/v1/channel-bindings/detail?binding_id=bind_xxx
```

### List Channel Accounts

```bash
curl http://localhost:8080/api/v1/channel-accounts/list?user_id=u_123&channel_type=wechat
```

### Fetch Runtime Configuration

```bash
curl http://localhost:8080/api/v1/runtime/config \
  -H "Authorization: Bearer <token>"
```
