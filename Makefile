APP := ariadne
BUILD_DIR ?= build
INSTALL_DIR ?= $(HOME)/app
GOOS := $(shell go env GOOS)
GOARCH := $(shell go env GOARCH)
TARGET := $(GOOS)-$(GOARCH)

ifeq ($(GOOS),windows)
GHOSTTY_ARCHIVE := ghostty-vt-static.lib
else
GHOSTTY_ARCHIVE := libghostty-vt.a
endif

GHOSTTY_LIB := $(BUILD_DIR)/libghostty-vt/$(TARGET)/lib/$(GHOSTTY_ARCHIVE)
BINARY := $(BUILD_DIR)/$(APP)
GO_SOURCES := $(shell find cmd internal -type f -name '*.go') go.mod go.sum

.PHONY: all build install libghostty

all: build

build: $(BINARY)

$(BINARY): $(GHOSTTY_LIB) $(GO_SOURCES)
	go build -o $@ ./cmd/ariadne

libghostty: $(GHOSTTY_LIB)

$(GHOSTTY_LIB):
	go run ./tools/build-libghostty-vt

install: build
	mkdir -p "$(INSTALL_DIR)"
	install -m 0755 "$(BINARY)" "$(INSTALL_DIR)/$(APP)"
