BINARY := quarks
PKG    := ./cmd/quarks

.PHONY: build run fakefeed dev open test vet tidy install uninstall clean

ADDR ?= http://localhost:7373

build:
	go build -trimpath -ldflags="-s -w" -o $(BINARY) $(PKG)

run: build
	./$(BINARY)

# Local fake-feed server for UI work (YouTube / news / Reddit shaped feeds).
fakefeed:
	go run ./cmd/fakefeed

# Run against the fake feeds — start `make fakefeed` in another terminal first.
# This only starts the server; open the dashboard with `make open` or a browser.
dev: build
	./$(BINARY) --config config.fake.yaml

# Open the dashboard in a chromeless app-window (any Chromium-family browser).
open:
	./packaging/quarks-open $(ADDR)

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy

# Install for the current user (binary + open helper + systemd --user service + launcher).
install:
	./packaging/install.sh

uninstall:
	./packaging/uninstall.sh

clean:
	rm -f $(BINARY)
