PREFIX ?= $(HOME)/.local
BIN := $(PREFIX)/bin/claude-steps

.PHONY: build install check test
build:
	go build -o claude-steps ./cmd/claude-steps

install:
	@mkdir -p "$(PREFIX)/bin"
	@tmp=$$(mktemp "$(BIN).XXXXXX"); trap 'rm -f "$$tmp"' EXIT; \
	go build -o "$$tmp" ./cmd/claude-steps && chmod 755 "$$tmp" && mv -f "$$tmp" "$(BIN)"

test:
	go test -race ./...

check: test
	go vet ./...
	@test -z "$$(gofmt -l cmd internal)"
