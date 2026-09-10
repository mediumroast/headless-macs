BINARY       := headless-macs
CMD          := ./cmd/headless-macs
DEBUG_BINARY := headless-macs-debug
DEBUG_CMD    := ./cmd/headless-macs-debug
DEBUG_ASSET  := internal/ops/assets/headless-macs-debug
INSTALL      := /usr/local/bin/$(BINARY)
DEBUG_INSTALL := /usr/local/bin/$(DEBUG_BINARY)
# git describe gives "v2.1.1" on an exact tag, "v2.1.1-14-g8a21af6" when
# ahead of the last tag, and appends "-dirty" with uncommitted changes —
# so a build always says what it actually is, not a hand-maintained guess.
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GOFLAGS  := -ldflags="-s -w -X main.version=$(VERSION)"

.PHONY: build debug-binary install clean lint test

# The main binary embeds headless-macs-debug via go:embed, so the debug
# binary must exist at $(DEBUG_ASSET) *before* $(CMD) is compiled — that
# dependency (not just parallel convenience) is why this is a prerequisite,
# not a separate step someone might skip. build also leaves a top-level
# copy of the debug binary (not just the embedded asset) so install has
# something to install — see install: below; ops.RunDebugTools() still
# does its own embedded-asset install too, since that path is also what
# applies the sudo NOPASSWD grant, which install intentionally does not.
build: debug-binary
	go build $(GOFLAGS) -o $(BINARY) $(CMD)
	cp $(DEBUG_ASSET) $(DEBUG_BINARY)

# Always rebuilt fresh — the copy committed at $(DEBUG_ASSET) is only a
# bootstrapping fallback so `go build ./...` works on a clean checkout
# without this step; a real `make build` should never ship a stale one.
debug-binary:
	go build -ldflags="-s -w" -o $(DEBUG_ASSET) $(DEBUG_CMD)

# Installs both binaries — this project makes two tools, so `make install`
# puts both on PATH, not just one. What this does NOT do is grant sudo
# NOPASSWD access for headless-macs-debug: that's a privilege-escalation
# decision, not a file-copy, and stays an explicit opt-in via Edit Config +
# `sudo headless-macs debug-tools` (see README's Debugging Tools section).
install: build
	sudo cp $(BINARY) $(INSTALL)
	sudo chmod 755 $(INSTALL)
	sudo cp $(DEBUG_BINARY) $(DEBUG_INSTALL)
	sudo chmod 755 $(DEBUG_INSTALL)
	@echo "Installed to $(INSTALL) and $(DEBUG_INSTALL)"
	@echo "Note: passwordless sudo for $(DEBUG_BINARY) is opt-in, not part of"
	@echo "this install — enable it in Edit Config (DEBUG section), then run"
	@echo "'sudo headless-macs debug-tools' to apply it."

clean:
	rm -f $(BINARY) $(DEBUG_BINARY)

lint:
	go vet ./...

test:
	go test ./...
