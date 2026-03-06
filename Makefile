.PHONY: all lint build test vulncheck

all: lint build test vulncheck

lint:
	golangci-lint run --timeout=5m ./...

build:
	go build -v ./...

test:
	go test -v -race -count=1 -coverprofile=coverage.out -covermode=atomic ./...

vulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...
