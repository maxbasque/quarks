BINARY := quarks
PKG    := ./cmd/quarks

.PHONY: build run fakefeed dev open test vet tidy install uninstall window clean

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

# Open the dashboard window (quarks-window on Linux; Chrome app-window on macOS from source).
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

# Just the native dashboard window (WebKitGTK); `make install` builds it too.
# Builds natively if the WebKitGTK headers are installed, else in a toolbox
# (created on first use) — see packaging/linux/build-window.sh.
window:
	./packaging/linux/build-window.sh $(CURDIR)/quarks-window
	install -m 755 quarks-window $(HOME)/.local/bin/quarks-window

clean:
	rm -f $(BINARY) quarks-window
