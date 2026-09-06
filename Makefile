BIN_DIR := bin
BINARY := $(BIN_DIR)/bp2ninja

# Detect Go compiler: allow user override via GO=...
# Priority: 1. GO variable, 2. Prebuilt Go in nearby tree, 3. System go
GO ?= $(shell python3 scripts/convert_plugin.py --get-go 2>/dev/null || which go || echo go)

.PHONY: all build plugins clean test

all: build plugins

build:
	@mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -o $(BINARY) ./cmd/bp2ninja
	@echo "[OK] Built $(BINARY)"

plugins:
	@python3 scripts/convert_plugin.py --all
	@echo "[OK] Built plugins in plugins/"

test:
	$(GO) test -v ./...

clean:
	rm -rf $(BIN_DIR) plugins/*.so plugins/*/ bp2ninja out/

