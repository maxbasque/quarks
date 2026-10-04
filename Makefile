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

# Native dashboard window (WebKitGTK) — replaces the Chromium app window once
# installed; quarks-open prefers it. It needs the WebKitGTK headers, so it's
# built in a toolbox (Bazzite's host is read-only) with the host's Go. One-time:
#   toolbox create --distro fedora --release 44 quarks-build
#   toolbox run -c quarks-build sudo dnf install -y webkit2gtk4.1-devel gtk3-devel gcc gcc-c++
TOOLBOX ?= quarks-build
window:
	toolbox run -c $(TOOLBOX) env PKG_CONFIG_PATH=$(CURDIR)/packaging/linux/pkgconfig CGO_ENABLED=1 \
		/run/host$(shell realpath "$$(command -v go)") build -tags quarkswindow -trimpath -ldflags="-s -w" \
		-o quarks-window ./cmd/quarks-window
	install -m 755 quarks-window $(HOME)/.local/bin/quarks-window

clean:
	rm -f $(BINARY) quarks-window
