VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PKG     := github.com/ape-dev-de/acat/internal/version
LDFLAGS := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).Date=$(DATE)
BIN     := bin

.PHONY: all build client relay demo snapshot video video-test test-electron test-load package testtest-integration lint vuln fmt tidy docker-relay clean

all: build test lint

build: client relay

client:
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN)/acat ./cmd/acat

relay:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN)/acat-relay ./cmd/acat-relay

demo: client
	$(BIN)/acat -demo

# Render the demo UI to a PNG (headless): make snapshot OUT=/tmp/acat.png
snapshot:
	ACAT_SNAPSHOT=$(abspath $(or $(OUT),bin/snapshot.png)) go test -count=1 -run TestSnapshot ./internal/ui/

video:
	cd video && npm ci && npm run build

video-test:
	cd video && npm test

# Real Electron helper end-to-end (after 'make video').
test-electron:
	go test -tags electron -count=1 ./internal/video/

# Relay load test, 10k WebSockets by default (raise the fd limit first: ulimit -n 65536).
LOAD_CONNS ?= 10000
test-load:
	ACAT_LOAD_CONNS=$(LOAD_CONNS) go test -tags load -count=1 -run TestLoad -v ./relay/

# Release bundle for this OS into dist/: make package VERSION=v0.2.0
package:
	scripts/package.sh $(VERSION)

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
