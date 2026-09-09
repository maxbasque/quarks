BINARY := quarks
PKG    := ./cmd/quarks

.PHONY: build run test tidy clean

build:
	go build -o $(BINARY) $(PKG)

run: build
	./$(BINARY)

test:
	go test ./...

tidy:
	go mod tidy

clean:
	rm -f $(BINARY)
