BIN_DIR := bin
BINARY := $(BIN_DIR)/bp2ninja
GO ?= go

.PHONY: all build clean test gowork clean-gowork

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
	install -Dm644 man/bp2ninja.1 $(DESTDIR)$(MANDIR)/bp2ninja.1
	install -d $(DESTDIR)$(DOCDIR)
	install -Dm644 README.md $(DESTDIR)$(DOCDIR)/README.md
	cp -aL docs/* $(DESTDIR)$(DOCDIR)/
