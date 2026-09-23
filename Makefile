
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GOBIN ?= $(shell go env GOBIN)
GOPATH ?= $(shell go env GOPATH)
GO_BIN_DIR ?= $(if $(GOBIN),$(GOBIN),$(GOPATH)/bin)
GOIMPORTS ?= $(shell command -v goimports 2>/dev/null || echo $(GO_BIN_DIR)/goimports)
GOIMPORTS_VERSION ?= v0.48.0
# Keep in sync with GO_LINT in signoz/primus src/make/cmd/go.mk, which the ci / lint job uses.
GOLANGCI_LINT_VERSION ?= v2.12.2
GOLANGCI_LINT ?= go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
BASE ?= origin/main

# Tracked and untracked Go files, skipping ignored paths such as .claude/worktrees.
GO_FILES = $(wildcard $(shell git ls-files -co --exclude-standard '*.go'))

.PHONY: fmt goimports install-goimports require-goimports build test ci check-fmt lint check-deps check-build test-race \
	check-guardrails mcp-ci-install check-protocol check-conformance check-e2e-style check-repo-docs

fmt:
	@echo "🧹 Running gofmt -s..."
	@gofmt -s -w $(GO_FILES)

goimports: require-goimports
	@echo "📦 Running goimports..."
	@$(GOIMPORTS) -w $(GO_FILES)

install-goimports:
	@go install golang.org/x/tools/cmd/goimports@$(GOIMPORTS_VERSION)

require-goimports:
	@if [ ! -x "$(GOIMPORTS)" ]; then \
		echo "goimports not found at $(GOIMPORTS) or on PATH."; \
		echo "Install it with: make install-goimports"; \
		exit 1; \
	fi

build: fmt goimports
	@echo "🚀 Building ..."
	@go build $(GO_FLAGS) -ldflags "-X github.com/SigNoz/signoz-mcp-server/pkg/version.Version=$(VERSION)" -o bin/signoz-mcp-server ./cmd/server/...

test:
	@echo "🧪 Running all tests..."
	@go test -v ./...

##@ CI

# Everything the PR gate runs except the live e2e suite. Needs Node, uv, and goimports.
ci: check-fmt lint check-deps check-build test-race check-guardrails check-protocol check-conformance check-e2e-style check-repo-docs
	@echo "✅ All PR-gate checks passed."

# Read-only: lists files that fmt or goimports would rewrite.
check-fmt: require-goimports
	@echo "🧹 Checking formatting..."
	@out=$$(gofmt -s -l $(GO_FILES); $(GOIMPORTS) -l $(GO_FILES)); \
	if [ -n "$$out" ]; then \
		printf '%s\n' "$$out" | sort -u; \
		echo "$${GITHUB_ACTIONS:+::error::}Go files need formatting. Run 'make fmt goimports' and commit."; \
		exit 1; \
	fi

lint:
	@echo "🔍 Running golangci-lint $(GOLANGCI_LINT_VERSION)..."
	@$(GOLANGCI_LINT) run --timeout 10m0s

check-deps:
	@echo "📦 Checking go.mod/go.sum..."
	@go mod tidy -diff || { echo "$${GITHUB_ACTIONS:+::error::}go.mod/go.sum are not tidy. Run 'go mod tidy' and commit."; exit 1; }
	@go mod verify

check-build:
	@echo "🚀 Building all packages..."
	@go build ./...

test-race:
	@echo "🧪 Running all tests with the race detector..."
	@go test -race -count=1 ./...

check-guardrails:
	@echo "🛡️  Checking guardrail inventory..."
	@LC_ALL=C sort -u guardrails/tests.txt | diff -u guardrails/tests.txt - || { \
		echo "$${GITHUB_ACTIONS:+::error::}guardrails/tests.txt must be sorted and contain no duplicates."; exit 1; }
	@actual=$$(go test -list '^TestGuardrail_' ./... | sed -n '/^TestGuardrail_/p' | LC_ALL=C sort); \
	printf '%s\n' "$$actual" | diff -u guardrails/tests.txt - || { \
		echo "$${GITHUB_ACTIONS:+::error::}Guardrail test inventory changed. Review the change and update guardrails/tests.txt."; exit 1; }; \
	printf 'Discovered %s guarded tests.\n' "$$(printf '%s\n' "$$actual" | wc -l | tr -d ' ')"
	@go test -count=1 -run '^TestGuardrail_' ./...

mcp-ci-install:
	@npm ci --ignore-scripts --no-audit --no-fund --prefix tools/mcp-ci >/dev/null

check-protocol: mcp-ci-install
	@bash -n scripts/test-mcp-protocol.sh
	@scripts/test-mcp-protocol.sh

check-conformance: mcp-ci-install
	@bash -n scripts/test-mcp-conformance.sh
	@scripts/test-mcp-conformance.sh

check-e2e-style:
	@echo "🐍 Checking e2e Python style..."
	@cd tests && uv run ruff format --check . && uv run ruff check .

# READY=1 applies the ready-for-review plan rules to plans changed since BASE.
check-repo-docs:
	@go run ./cmd/check-repo-docs $(if $(READY),-ready -base $(BASE))

##@ E2E

E2E_FLAGS ?=

test-e2e: ## Runs the e2e suite against a freshly cast SigNoz, tearing it down afterwards.
	@echo "🧪 Running e2e suite..."
	@cd tests && uv sync && uv run pytest --basetemp=./tmp/ e2e/tests $(E2E_FLAGS)

test-e2e-reuse: ## Runs the e2e suite reusing the cached SigNoz environment.
	@echo "🧪 Running e2e suite (--reuse)..."
	@cd tests && uv sync && uv run pytest --basetemp=./tmp/ --reuse e2e/tests $(E2E_FLAGS)

setup-e2e-env: ## Brings the e2e environment up and keeps it for --reuse runs.
	@echo "🚀 Setting up the e2e environment..."
	@cd tests && uv sync && uv run pytest --basetemp=./tmp/ --reuse e2e/bootstrap/setup.py::test_setup $(E2E_FLAGS)

cleanup-test-e2e: ## Tears down the cached e2e environment.
	@echo "🧹 Tearing down the e2e environment..."
	@cd tests && uv sync && uv run pytest --basetemp=./tmp/ --teardown e2e/bootstrap/setup.py::test_teardown $(E2E_FLAGS)

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
