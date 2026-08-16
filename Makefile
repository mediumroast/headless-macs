BINARY   := headless-macs
CMD      := ./cmd/headless-macs
INSTALL  := /usr/local/bin/$(BINARY)
GOFLAGS  := -ldflags="-s -w"

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
