CHANNEL_MASTER_KEY ?= MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=

test:
	go test ./...

run:
	CHANNEL_MASTER_KEY=$(CHANNEL_MASTER_KEY) go run .

watch:
	@if command -v air >/dev/null 2>&1; then \
		CHANNEL_MASTER_KEY=$(CHANNEL_MASTER_KEY) air; \
	else \
		echo "air not found, installing..."; \
		GOBIN=$$(go env GOPATH)/bin go install github.com/air-verse/air@latest && \
		PATH="$$PATH:$$(go env GOPATH)/bin" CHANNEL_MASTER_KEY=$(CHANNEL_MASTER_KEY) air; \
	fi

mcp-echo:
	go build -o bin/mcp-echo ./mcps/echo

mcp-ping:
	go build -o bin/mcp-ping ./mcps/ping

mcp-boo:
	go build -o bin/mcp-boo ./mcps/boo

mcps: mcp-echo mcp-ping mcp-boo

test-mcps:
	go test ./mcps/echo/... ./mcps/ping/... ./mcps/boo/...

.PHONY: test run watch mcp-echo mcp-ping mcp-boo mcps test-mcps
