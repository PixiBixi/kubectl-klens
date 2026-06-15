.PHONY: build lint test snapshot clean

build:
	go build -ldflags "-s -w" -o kubectl-klens .

lint:
	go vet ./...
	staticcheck ./...

test:
	go test -race ./...

snapshot:
	goreleaser release --snapshot --clean

clean:
	rm -f kubectl-klens
	rm -rf dist/
