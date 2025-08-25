
fmt:
	@echo "🧹 Running go fmt..."
	@go fmt ./...

goimports:
	@echo "📦 Running goimports..."
	@goimports -w .

build: fmt goimports
	@echo "🚀 Building ..."
	@go build $(GO_FLAGS) -o bin/signoz-mcp-server ./cmd/server/...
