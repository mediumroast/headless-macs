BINARY   := headless-macs
CMD      := ./cmd/headless-macs
INSTALL  := /usr/local/bin/$(BINARY)
# git describe gives "v2.1.1" on an exact tag, "v2.1.1-14-g8a21af6" when
# ahead of the last tag, and appends "-dirty" with uncommitted changes —
# so a build always says what it actually is, not a hand-maintained guess.
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GOFLAGS  := -ldflags="-s -w -X main.version=$(VERSION)"

.PHONY: build install clean lint test

build:
	go build $(GOFLAGS) -o $(BINARY) $(CMD)

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
