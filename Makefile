OUT  ?= quarks
DENO ?= deno

# What the compiled binaries may do. The server needs the network, its config
# and cache files, the environment (${VAR} tokens, HOME) and to run xdg-open /
# systemctl; the window (`quarks window`) also loads libwebview through FFI.
PERMS     := --allow-net --allow-read --allow-write --allow-env --allow-run
SERVER    := $(PERMS) --allow-ffi --include src/web/static
MAC_PERMS := $(PERMS) --allow-ffi --include src/web/static --include src/macos/server_worker.ts --include config.example.yaml

.PHONY: build run fakefeed dev open test check fmt install uninstall window mac clean

ADDR ?= http://localhost:7373

build:
	$(DENO) compile $(SERVER) --output $(OUT) cmd/quarks.ts

run:
	$(DENO) run $(SERVER) cmd/quarks.ts

# Local fake-feed server for UI work (YouTube / news / Reddit shaped feeds).
fakefeed:
	$(DENO) run --allow-net cmd/fakefeed.ts

# Run against the fake feeds — start `make fakefeed` in another terminal first.
# This only starts the server; open the dashboard with `make open` or a browser.
dev:
	$(DENO) run $(SERVER) cmd/quarks.ts --config config.fake.yaml

# Open the dashboard window (quarks-window on Linux; Chrome app-window on macOS from source).
open:
	./packaging/quarks-open $(ADDR)

test:
	$(DENO) test --allow-all

# type-check and lint everything (what `go vet` was)
check:
	$(DENO) check cmd/ src/
	$(DENO) lint

fmt:
	$(DENO) fmt

# Install for the current user (binary + open helper + systemd --user service + launcher).
install:
	./packaging/install.sh

uninstall:
	./packaging/uninstall.sh

# Just the native dashboard window: the wrapper and the pinned libwebview.
# `make install` does this too.
window:
	install -m 755 packaging/quarks-window $(HOME)/.local/bin/quarks-window
	./packaging/fetch-libwebview.sh $(HOME)/.local/lib/quarks libwebview.$(shell uname -m | sed 's/arm64/aarch64/').so

# The macOS app binary for one architecture (TARGET=aarch64-apple-darwin or
# x86_64-apple-darwin); packaging/macos/build-app.sh builds both into the .app.
mac:
	$(DENO) compile $(MAC_PERMS) --target $(TARGET) --output $(OUT) cmd/quarks_mac.ts

clean:
	rm -f quarks quarks-window
