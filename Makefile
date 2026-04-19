
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

fmt:
	@echo "🧹 Running go fmt..."
	@go fmt ./...

goimports:
	@echo "📦 Running goimports..."
	@if ! command -v goimports > /dev/null; then \
		echo "goimports not found on PATH."; \
		echo "Install it with: go install golang.org/x/tools/cmd/goimports@latest"; \
		exit 1; \
	else \
		goimports -w .; \
	fi

build: fmt goimports
	@echo "🚀 Building ..."
	@go build $(GO_FLAGS) -ldflags "-X github.com/SigNoz/signoz-mcp-server/pkg/version.Version=$(VERSION)" -o bin/signoz-mcp-server ./cmd/server/...

test:
	@echo "🧪 Running all tests..."
	@go test -v ./...

bundle:
	@echo "🚀 Building SigNoz Claude MCP extension..."
	@mkdir -p bundle/server
	@GOOS=darwin GOARCH=arm64 go build -o bundle/server/signoz-mcp-server ./cmd/server/
	@GOOS=windows GOARCH=amd64 go build -o bundle/server/signoz-mcp-server.exe ./cmd/server/
	@cp ./manifest.json bundle/
	@cp ./assets/signoz_icon.png bundle/
	@echo "📦 Installing MCPB CLI..."
	@npm install -g @anthropic-ai/mcpb > /dev/null 2>&1
	@echo "🧩 Packing MCP bundle..."
	cd bundle && mcpb pack
