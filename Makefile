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

mcp-a2a:
	go build -o bin/mcp-a2a ./mcps/a2a

mcps: mcp-echo mcp-ping mcp-boo mcp-a2a

test-mcps:
	go test ./mcps/echo/... ./mcps/ping/... ./mcps/boo/... ./mcps/a2a/...

.PHONY: test run watch mcp-echo mcp-ping mcp-boo mcp-a2a mcps test-mcps
