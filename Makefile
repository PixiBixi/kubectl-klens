.PHONY: help build lint test bench snapshot clean
.DEFAULT_GOAL := help

build: ## Build the local kubectl-klens binary
	go build -ldflags "-s" -o kubectl-klens .

lint: ## Run golangci-lint (config: .golangci.yml)
	golangci-lint run

test: ## Run tests with the race detector
	go test -race ./...

bench: ## Run benchmarks (BENCH=<regexp> to filter, COUNT=<n> for benchstat runs)
	go test -run XXX -bench '$(or $(BENCH),.)' -benchtime $(or $(BENCHTIME),20x) -count $(or $(COUNT),1) ./internal/...

snapshot: ## Build a goreleaser snapshot (dry-run release)
	goreleaser release --snapshot --clean

clean: ## Remove build artifacts
	rm -f kubectl-klens
	rm -rf dist/

help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'
