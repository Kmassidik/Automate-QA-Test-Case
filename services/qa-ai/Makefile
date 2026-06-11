BINARY := qa-ai
PKG     := ./cmd/qa-ai

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-10s\033[0m %s\n",$$1,$$2}'

.PHONY: dev
dev: ## Run with hot reload (requires air + sourced .env)
	air

.PHONY: build
build: ## Build a static, stripped binary into ./bin
	CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/$(BINARY) $(PKG)

.PHONY: test
test: ## Run tests
	go test ./...

.PHONY: lint
lint: ## Run golangci-lint
	golangci-lint run ./...

.PHONY: tidy
tidy: ## Tidy go.mod
	go mod tidy

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf bin tmp
