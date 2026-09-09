BINARY := quarks
PKG    := ./cmd/quarks

.PHONY: build run test vet tidy install clean

build:
	go build -o $(BINARY) $(PKG)

run: build
	./$(BINARY)

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
