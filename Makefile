CHANNEL_MASTER_KEY ?= MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=

test:
	go test ./...

run:
	CHANNEL_MASTER_KEY=$(CHANNEL_MASTER_KEY) go run ./cmd/server

watch:
	@if command -v air >/dev/null 2>&1; then \
		CHANNEL_MASTER_KEY=$(CHANNEL_MASTER_KEY) air --build.cmd "go build -o ./tmp/myclaw ./cmd/server" --build.bin "./tmp/myclaw"; \
	else \
		echo "air not found, installing..."; \
		GOBIN=$$(go env GOPATH)/bin go install github.com/air-verse/air@latest && \
		PATH="$$PATH:$$(go env GOPATH)/bin" CHANNEL_MASTER_KEY=$(CHANNEL_MASTER_KEY) air --build.cmd "go build -o ./tmp/myclaw ./cmd/server" --build.bin "./tmp/myclaw"; \
	fi
