BINARY       := headless-macs
CMD          := ./cmd/headless-macs
DEBUG_BINARY := headless-macs-debug
DEBUG_CMD    := ./cmd/headless-macs-debug
DEBUG_ASSET  := internal/ops/assets/headless-macs-debug
INSTALL      := /usr/local/bin/$(BINARY)
# git describe gives "v2.1.1" on an exact tag, "v2.1.1-14-g8a21af6" when
# ahead of the last tag, and appends "-dirty" with uncommitted changes —
# so a build always says what it actually is, not a hand-maintained guess.
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GOFLAGS  := -ldflags="-s -w -X main.version=$(VERSION)"

.PHONY: build debug-binary install clean lint test

# The main binary embeds headless-macs-debug via go:embed, so the debug
# binary must exist at $(DEBUG_ASSET) *before* $(CMD) is compiled — that
# dependency (not just parallel convenience) is why this is a prerequisite,
# not a separate step someone might skip.
build: debug-binary
	go build $(GOFLAGS) -o $(BINARY) $(CMD)

# Always rebuilt fresh — the copy committed at $(DEBUG_ASSET) is only a
# bootstrapping fallback so `go build ./...` works on a clean checkout
# without this step; a real `make build` should never ship a stale one.
debug-binary:
	go build -ldflags="-s -w" -o $(DEBUG_ASSET) $(DEBUG_CMD)

install: build
	sudo cp $(BINARY) $(INSTALL)
	sudo chmod 755 $(INSTALL)
	@echo "Installed to $(INSTALL)"

clean:
	rm -f $(BINARY)

lint:
	go vet ./...

test:
	go test ./...
