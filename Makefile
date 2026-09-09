BINARY := quarks
PKG    := ./cmd/quarks

.PHONY: build run fakefeed dev open test vet tidy install clean

ADDR ?= http://localhost:7373

build:
	go build -o $(BINARY) $(PKG)

run: build
	./$(BINARY)

# Local fake-feed server for UI work (YouTube / news / Reddit shaped feeds).
fakefeed:
	go run ./cmd/fakefeed

# Run against the fake feeds — start `make fakefeed` in another terminal first.
# This only starts the server; open the dashboard with `make open` or a browser.
dev: build
	./$(BINARY) --config config.fake.yaml

# Open the dashboard in a chromeless Chrome app-window.
open:
	flatpak run com.google.Chrome --app=$(ADDR) >/dev/null 2>&1 &

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy

# Install for the current user (binary + systemd --user service + .desktop).
install:
	./packaging/install.sh

clean:
	rm -f $(BINARY)
