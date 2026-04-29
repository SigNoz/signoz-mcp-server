
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GOBIN ?= $(shell go env GOBIN)
GOPATH ?= $(shell go env GOPATH)
GO_BIN_DIR ?= $(if $(GOBIN),$(GOBIN),$(GOPATH)/bin)
GOIMPORTS ?= $(shell command -v goimports 2>/dev/null || echo $(GO_BIN_DIR)/goimports)

fmt:
	@echo "🧹 Running go fmt..."
	@go fmt ./...

goimports:
	@echo "📦 Running goimports..."
	@if [ ! -x "$(GOIMPORTS)" ]; then \
		echo "goimports not found at $(GOIMPORTS) or on PATH."; \
		echo "Install it with: go install golang.org/x/tools/cmd/goimports@latest"; \
		exit 1; \
	else \
		$(GOIMPORTS) -w .; \
	fi

build: fmt goimports
	@echo "🚀 Building ..."
	@go build $(GO_FLAGS) -ldflags "-X github.com/SigNoz/signoz-mcp-server/pkg/version.Version=$(VERSION)" -o bin/signoz-mcp-server ./cmd/server/...

test:
	@echo "🧪 Running all tests..."
	@go test -v ./...

docs-index:
	@echo "📚 Rebuilding embedded SigNoz docs corpus (fail-loud if signoz.io/docs/sitemap.md is unreachable)..."
	@go run ./cmd/build-docs-index
	@echo "✅ corpus.gob.gz + corpus.manifest.json regenerated. Diff the manifest and commit both files."

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
