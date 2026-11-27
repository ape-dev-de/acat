VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PKG     := github.com/ape-dev-de/acat/internal/version
LDFLAGS := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).Date=$(DATE)
BIN     := bin

.PHONY: all build client relay test test-integration lint vuln fmt tidy docker-relay clean

all: build test lint

build: client relay

client:
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN)/acat ./cmd/acat

relay:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN)/acat-relay ./cmd/acat-relay

test:
	go test -race -count=1 ./...

# Real Graph/ACS calls; needs ACAT_TEST_* env vars (see docs/setup.md).
test-integration:
	go test -tags integration -count=1 ./...

lint:
	golangci-lint run ./...

vuln:
	govulncheck ./...

fmt:
	gofmt -w .

tidy:
	go mod tidy

docker-relay:
	docker build -f deploy/Dockerfile.relay -t ghcr.io/ape-dev-de/acat-relay:$(VERSION) .

clean:
	rm -rf $(BIN)
