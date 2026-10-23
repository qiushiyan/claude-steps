PREFIX ?= $(HOME)/.local
BIN := $(PREFIX)/bin/claude-steps

.PHONY: build install test vet fix check golden
build:
	go build -o claude-steps ./cmd/claude-steps

install:
	@mkdir -p "$(PREFIX)/bin"
	@tmp=$$(mktemp "$(BIN).XXXXXX"); trap 'rm -f "$$tmp"' EXIT; \
	go build -o "$$tmp" ./cmd/claude-steps && chmod 755 "$$tmp" && mv -f "$$tmp" "$(BIN)"

test:
	go test -race ./...

vet:
	go vet ./...

# Go 1.27 modernizers must leave nothing to rewrite.
fix:
	@out=$$(go fix -diff ./...); if [ -n "$$out" ]; then echo "$$out"; exit 1; fi

check: vet fix test
	@test -z "$$(gofmt -l cmd internal)"

# Rewrite the golden help pages after an intended change; review the diff.
golden:
	CLIKIT_UPDATE=1 go test ./...
