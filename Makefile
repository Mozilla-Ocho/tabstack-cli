BINARY  := tabstack
PKG     := github.com/Mozilla-Ocho/tabstack-cli
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-s -w -X $(PKG)/cmd.version=$(VERSION)"

GOBIN   := $(shell go env GOPATH)/bin

.PHONY: all build install run fmt vet tidy test lint lint-copy clean smoke snapshot

all: build

build: ## Build the binary into ./bin
	go build $(LDFLAGS) -o bin/$(BINARY) ./cmd/tabstack

install: ## Install into $GOPATH/bin
	go install $(LDFLAGS) ./cmd/tabstack

.PHONY: install-local
install-local: build ## install binary to /usr/local/bin (works without Go PATH setup)
	sudo mkdir -p /usr/local/bin
	sudo install -m 755 bin/tabstack /usr/local/bin/tabstack
	@echo "Installed to /usr/local/bin/tabstack"

run: ## Build and run; pass args with ARGS="..."
	go run ./cmd/tabstack $(ARGS)

fmt: ## Format all packages
	gofmt -w .

vet: ## Vet all packages
	go vet ./...

tidy: ## Tidy go.mod/go.sum
	go mod tidy

test: ## Run tests
	go test ./...

smoke: build ## Run live API smoke test (needs an API key; SKIP_AGENT=1 to skip costly calls)
	./scripts/smoke-test.sh

lint-copy: ## Check docs/Go copy for em dashes and banned terms
	./scripts/lint-copy.sh

lint: fmt vet lint-copy ## Format, vet, then check copy

snapshot: ## Build a local release snapshot with goreleaser (no publish)
	goreleaser release --snapshot --clean

clean: ## Remove build artifacts
	rm -rf bin dist

help: ## List targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'
