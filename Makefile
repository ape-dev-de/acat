VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PKG     := github.com/ape-dev-de/acat/internal/version
LDFLAGS := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).Date=$(DATE)
BIN     := bin

.PHONY: all build client relay demo snapshot video video-test test-electron test-load package run install test test-integration lint vuln fmt tidy docker-relay clean

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

# Live defaults (public values: the client ID ships in every release anyway).
# Override per call, e.g. make run CLIENT_ID=… RELAY_URL=…
CLIENT_ID  ?= 28019cd6-31b0-4a65-a057-d14c6eff7a54
RELAY_URL  ?= https://relay.acat.ape-dev.de
DEV_VERSION = 0.0.0-dev.$(COMMIT)
UNAME_S    := $(shell uname -s)
ifeq ($(UNAME_S),Darwin)
ELECTRON := video/node_modules/electron/dist/Electron.app/Contents/MacOS/Electron
else
ELECTRON := video/node_modules/electron/dist/electron
endif

# Run the client against the live relay; uses the dev video helper if
# 'make video' was run (otherwise calls stay disabled).
run: client
	@if [ -x "$(ELECTRON)" ] && [ -f video/dist/call.bundle.js ]; then \
		echo "video helper: $(ELECTRON)"; \
		ACAT_VIDEO_HELPER="$(abspath $(ELECTRON))" ACAT_VIDEO_APP="$(abspath video)" \
		ACAT_CLIENT_ID=$(CLIENT_ID) ACAT_RELAY_URL=$(RELAY_URL) $(BIN)/acat; \
	else \
		echo "no video helper (run 'make video' for calls)"; \
		ACAT_CLIENT_ID=$(CLIENT_ID) ACAT_RELAY_URL=$(RELAY_URL) $(BIN)/acat; \
	fi

# macOS: build acat.app (helper embedded, defaults baked in) and copy it to /Applications.
install:
ifneq ($(UNAME_S),Darwin)
	$(error make install is macOS only; use 'make package' elsewhere)
endif
	APP_ONLY=1 ACAT_DEFAULT_CLIENT_ID=$(CLIENT_ID) ACAT_DEFAULT_RELAY_URL=$(RELAY_URL) scripts/package.sh $(DEV_VERSION)
	@if pgrep -xq acat; then echo "acat is running: quit it first"; exit 1; fi
	rm -rf /Applications/acat.app
	ditto dist/acat.app /Applications/acat.app
	@echo "installed /Applications/acat.app ($(DEV_VERSION))"

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
