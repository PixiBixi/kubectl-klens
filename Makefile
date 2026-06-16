.PHONY: help build lint test snapshot clean
.DEFAULT_GOAL := help

build: ## Build the local kubectl-klens binary
	go build -ldflags "-s -w" -o kubectl-klens .

lint: ## Run go vet and staticcheck
	go vet ./...
	staticcheck ./...

test: ## Run tests with the race detector
	go test -race ./...

snapshot: ## Build a goreleaser snapshot (dry-run release)
	goreleaser release --snapshot --clean

clean: ## Remove build artifacts
	rm -f kubectl-klens
	rm -rf dist/

help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'
