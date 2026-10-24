BIN_DIR := bin
BINARY := $(BIN_DIR)/bp2ninja
GO ?= go

.PHONY: all build clean test gowork clean-gowork
.PHONY: build-android-arm64 build-android-arm64-bionic release
# NOTE: build-linux-% and build-musl-% are pattern targets; they must NOT be
# declared .PHONY or make skips implicit-rule matching for them.

all: build

build:
	@mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -o $(BINARY) ./cmd/bp2ninja
	@echo "[OK] Built $(BINARY)"

gowork: build
	@./$(BINARY) gowork

clean-gowork: build
	@./$(BINARY) clean-gowork

test:
	$(GO) test -v ./...

clean:
	rm -rf $(BIN_DIR) plugins/*.so plugins/*/ bp2ninja out/ .deps/ go.work go.work.sum

PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
MANDIR ?= $(PREFIX)/share/man/man1
DOCDIR ?= $(PREFIX)/share/doc/bp2ninja

install: build
	install -Dm755 $(BINARY) $(DESTDIR)$(BINDIR)/bp2ninja
	ln -sf bp2ninja $(DESTDIR)$(BINDIR)/bp2siso
	install -Dm644 man/bp2ninja.1 $(DESTDIR)$(MANDIR)/bp2ninja.1
	install -d $(DESTDIR)$(DOCDIR)
	install -Dm644 README.md $(DESTDIR)$(DOCDIR)/README.md
	cp -aL docs/* $(DESTDIR)$(DOCDIR)/

# --- Cross-build matrix -------------------------------------------------------
#
# Static builds (CGO_ENABLED=0) link no libc at all: one binary runs on glibc,
# musl and Android (bionic) hosts of the matching instruction set. Go plugins
# (.so blueprint modules) are unavailable in static builds; build natively with
# CGO_ENABLED=1 (default `make build`) when plugins are needed.
RELEASE_DIR := $(BIN_DIR)/release
VERSION ?= $(shell $(GO) list -m 2>/dev/null || echo dev)
RELEASE_VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo $(VERSION))

# Native-architecture static binary for headless servers (amd64 / arm64 hosts,
# Alpine/musl containers). Runs the graph with system clang; add
# -sysroot <android sysroot> when targeting Android with plain clang.
build-linux-%:
	@mkdir -p $(RELEASE_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=$* $(GO) build -trimpath -o $(RELEASE_DIR)/bp2ninja-linux-$* ./cmd/bp2ninja
	@echo "[OK] Built $(RELEASE_DIR)/bp2ninja-linux-$* (static)"

# Android self-replication: static GOOS=android binary — push to any arm64
# device (adb push ... /data/local/tmp) and run on-device; pairs with Termux's
# clang which already targets the device's bionic.
build-android-arm64:
	@mkdir -p $(RELEASE_DIR)
	CGO_ENABLED=0 GOOS=android GOARCH=arm64 $(GO) build -trimpath -o $(RELEASE_DIR)/bp2ninja-android-arm64 ./cmd/bp2ninja
	@echo "[OK] Built $(RELEASE_DIR)/bp2ninja-android-arm64 (static, on-device)"

# Bionic-linked (dynamic) build via the NDK toolchain. Requires an NDK;
# discovered automatically, or point at one via ANDROID_NDK / NDK_CLANG.
NDK_LLVM_BIN := $(shell ls -d $(firstword $(wildcard $(ANDROID_NDK)/toolchains/llvm/prebuilt/*/bin)) 2>/dev/null)
NDK_CLANG ?= $(NDK_LLVM_BIN)/aarch64-linux-android34-clang
build-android-arm64-bionic:
	@test -x "$(NDK_CLANG)" || { echo "ERROR: NDK clang not found (set NDK_CLANG or ANDROID_NDK)"; exit 1; }
	@mkdir -p $(RELEASE_DIR)
	CGO_ENABLED=1 GOOS=android GOARCH=arm64 CC="$(NDK_CLANG)" $(GO) build -trimpath -o $(RELEASE_DIR)/bp2ninja-android-arm64-bionic ./cmd/bp2ninja
	@echo "[OK] Built $(RELEASE_DIR)/bp2ninja-android-arm64-bionic (bionic-linked)"

# True musl-linked (dynamic) builds via musl-gcc. Static builds above already
# run on musl hosts; this target is for environments requiring a real musl
# dynamic link. Install musl-gcc first (pacman -S musl / apt install musl-tools).
MUSL_CC ?= musl-gcc
build-musl-%:
	@command -v $(MUSL_CC) >/dev/null || { echo "ERROR: $(MUSL_CC) not found (package: musl / musl-tools)"; exit 1; }
	@mkdir -p $(RELEASE_DIR)
	CGO_ENABLED=1 GOOS=linux GOARCH=$* CC="$(MUSL_CC)" $(GO) build -trimpath -o $(RELEASE_DIR)/bp2ninja-musl-$* ./cmd/bp2ninja
	@echo "[OK] Built $(RELEASE_DIR)/bp2ninja-musl-$* (musl-linked)"

release: build-linux-amd64 build-linux-arm64 build-android-arm64
	cd $(RELEASE_DIR) && for f in bp2ninja-*; do \
		ln -sf $$f $$(echo $$f | sed 's/^bp2ninja-/bp2siso-/'); \
		tar -czf bp2ninja-$(RELEASE_VERSION)-$${f#bp2ninja-}.tar.gz $$f; \
	done
	cd $(RELEASE_DIR) && sha256sum bp2ninja-$(RELEASE_VERSION)-*.tar.gz > sha256sums.txt
	@echo "[OK] Release artifacts in $(RELEASE_DIR)/ (bp2siso symlinks + checksums included)"
