BINARY := quarks
PKG    := ./cmd/quarks

.PHONY: build run fakefeed dev test vet tidy install clean

build:
	go build -o $(BINARY) $(PKG)

run: build
	./$(BINARY)

# Local fake-feed server for UI work (YouTube / news / Reddit shaped feeds).
fakefeed:
	go run ./cmd/fakefeed

# Run against the fake feeds — start `make fakefeed` in another terminal first.
dev: build
	./$(BINARY) --config config.fake.yaml

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
