
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GOBIN ?= $(shell go env GOBIN)
GOPATH ?= $(shell go env GOPATH)
GO_BIN_DIR ?= $(if $(GOBIN),$(GOBIN),$(GOPATH)/bin)
GOIMPORTS ?= $(shell command -v goimports 2>/dev/null || echo $(GO_BIN_DIR)/goimports)

SIGNOZ_SPEC ?= ../signoz/docs/api/openapi.yml

gen:
	@echo "🤖 Generating MCP tools from OpenAPI spec at $(SIGNOZ_SPEC)..."
	@go run ./cmd/gen-tools \
		--spec $(SIGNOZ_SPEC) \
		--handlers-dir internal/handler/tools \
		--root-dir pkg/types/gentools \
		--gentools-import github.com/SigNoz/signoz-mcp-server/pkg/types/gentools \
		--manifest manifest.json

gen-check: gen
	@git diff --exit-code internal/handler/tools/zz_generated_*.go pkg/types/gentools/zz_generated_*.go manifest.json \
		|| (echo "❌ Generated code is out of sync with the OpenAPI spec. Run 'make gen' and commit the result."; exit 1)

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
