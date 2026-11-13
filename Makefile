.PHONY: run build test check
GO ?= go

run:
	$(GO) run ./cmd/server

build:
	mkdir -p bin
	CGO_ENABLED=0 $(GO) build -buildvcs=false -trimpath -o bin/golden-hour-server ./cmd/server

test:
	$(GO) test ./...
	npm test

check:
	$(GO) vet ./...
	node --check client/pvp.js
	node --check client/engine.js
	node --check client/net.js
