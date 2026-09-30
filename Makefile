WINDOWS := $(filter Windows_NT,$(OS))
EXE     := $(if $(WINDOWS),.exe,)
BINARY  := bin/llmbench$(EXE)
AIR_CFG := $(if $(WINDOWS),.air.windows.toml,.air.toml)
# Recursive (=) so git only runs for targets that use it.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  = -s -w -X main.version=$(VERSION)
UI_DIST := internal/ui/dist
# npm rewrites this file on every install; it marks node_modules as current.
NODE_DEPS := web/node_modules/.package-lock.json

.PHONY: dev dev-api dev-web web build test lint docker clean

## dev: run the Vite dev server and the Go backend (air) concurrently
dev:
	$(MAKE) -j2 dev-api dev-web

dev-api:
	air -c $(AIR_CFG)

dev-web: $(NODE_DEPS)
	cd web && npm run dev

# npm install (not npm ci): it only changes what differs, instead of deleting
# node_modules, which fails on Windows while a dev server or editor holds files.
$(NODE_DEPS): web/package.json web/package-lock.json
	cd web && npm install
	@touch $(NODE_DEPS)

## web: build the frontend and copy it into the Go embed directory
web: $(NODE_DEPS)
	cd web && npm run build
	find $(UI_DIST) -mindepth 1 -maxdepth 1 ! -name .gitkeep -exec rm -rf {} +
	cp -R web/build/. $(UI_DIST)/

## build: single self-contained binary with the UI embedded
build: web
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/llmbench

## test: Go tests and frontend type checks
test: $(NODE_DEPS)
	go test ./...
	cd web && npm run check

lint:
	golangci-lint run

docker:
	docker build --build-arg VERSION=$(VERSION) -t llmbench:$(VERSION) -t llmbench:latest .

clean:
	rm -rf bin tmp web/build
	find $(UI_DIST) -mindepth 1 -maxdepth 1 ! -name .gitkeep -exec rm -rf {} +
