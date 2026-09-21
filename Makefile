APP := ariadne
BUILD_DIR ?= build
INSTALL_DIR ?= $(HOME)/app
GOOS := $(shell go env GOOS)
GOARCH := $(shell go env GOARCH)
TARGET := $(GOOS)-$(GOARCH)
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= unknown
VERSION_PACKAGE := github.com/aruzen/ariadne/internal/version
LDFLAGS := -X $(VERSION_PACKAGE).Version=$(VERSION) -X $(VERSION_PACKAGE).Commit=$(COMMIT) -X $(VERSION_PACKAGE).Date=$(BUILD_DATE)

ifeq ($(GOOS),windows)
GHOSTTY_ARCHIVE := ghostty-vt-static.lib
else
GHOSTTY_ARCHIVE := libghostty-vt.a
endif

GHOSTTY_LIB := $(BUILD_DIR)/libghostty-vt/$(TARGET)/lib/$(GHOSTTY_ARCHIVE)
BINARY := $(BUILD_DIR)/$(APP)
GO_SOURCES := $(shell find cmd internal api -type f -name '*.go') go.mod go.sum api/plugin/ariadne_plugin.h internal/plugin/native/bridge.c internal/plugin/native/bridge.h

.PHONY: all build install libghostty

all: build

build: $(BINARY)

$(BINARY): $(GHOSTTY_LIB) $(GO_SOURCES)
	go build -trimpath -ldflags "$(LDFLAGS)" -o $@ ./cmd/ariadne

libghostty: $(GHOSTTY_LIB)

$(GHOSTTY_LIB):
	go run ./tools/build-libghostty-vt

install: build
	mkdir -p "$(INSTALL_DIR)"
	install -m 0755 "$(BINARY)" "$(INSTALL_DIR)/$(APP)"
