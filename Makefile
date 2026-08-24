BINARY  := svar-wave
PKG     := ./cmd/svarwave
BIN_DIR := bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

LDFLAGS := -s -w -X main.version=$(VERSION)
GOBUILD := go build -trimpath -ldflags "$(LDFLAGS)"

# --- cross-compilation -------------------------------------------------------
# oto/beep require cgo and ALSA on Linux, so cross builds need:
#   * a cross gcc per architecture
#   * ALSA headers/libs of the TARGET architecture (fetched automatically
#     from deb.debian.org into .sysroots/, see `make sysroots`)
CC_ARM64 ?= $(shell command -v aarch64-linux-gnu-gcc || command -v aarch64-linux-gnu-gcc-14)
CC_ARMV7 ?= $(shell command -v arm-linux-gnueabihf-gcc || command -v arm-linux-gnueabihf-gcc-14)

ALSA_VER    := 1.2.16.1-1
DEB_BASE    := https://deb.debian.org/debian/pool/main/a/alsa-lib
SYSROOT_DIR := .sysroots

.PHONY: all build release amd64 arm64 arm7 sysroots run daemon stop \
        test vet fmt fmt-check clean distclean

all: build

# native build -> bin/svar-wave
build:
	$(GOBUILD) -o $(BIN_DIR)/$(BINARY) $(PKG)

# all cross builds -> bin/svar-wave-linux-<arch>
release: amd64 arm64 arm7
	@ls -lh $(BIN_DIR)/

amd64:
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 $(GOBUILD) -o $(BIN_DIR)/$(BINARY)-linux-amd64 $(PKG)

arm64: $(SYSROOT_DIR)/pc-arm64
	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 CC="$(CC_ARM64)" PKG_CONFIG="$(abspath $(SYSROOT_DIR)/pc-arm64)" \
		$(GOBUILD) -o $(BIN_DIR)/$(BINARY)-linux-arm64 $(PKG)

arm7: $(SYSROOT_DIR)/pc-armv7
	CGO_ENABLED=1 GOOS=linux GOARCH=arm GOARM=7 CC="$(CC_ARMV7)" PKG_CONFIG="$(abspath $(SYSROOT_DIR)/pc-armv7)" \
		$(GOBUILD) -o $(BIN_DIR)/$(BINARY)-linux-arm7 $(PKG)

# fetch target-arch ALSA libs and generate pkg-config shims
sysroots: $(SYSROOT_DIR)/pc-arm64 $(SYSROOT_DIR)/pc-armv7

$(SYSROOT_DIR)/pc-arm64: | $(SYSROOT_DIR)
	@curl -sso $(SYSROOT_DIR)/alsa-lib_arm64.deb $(DEB_BASE)/libasound2t64_$(ALSA_VER)_arm64.deb
	@curl -sso $(SYSROOT_DIR)/alsa-dev_arm64.deb $(DEB_BASE)/libasound2-dev_$(ALSA_VER)_arm64.deb
	@dpkg-deb -x $(SYSROOT_DIR)/alsa-lib_arm64.deb $(SYSROOT_DIR)/arm64
	@dpkg-deb -x $(SYSROOT_DIR)/alsa-dev_arm64.deb $(SYSROOT_DIR)/arm64
	@$(call gen-pc,pc-arm64,arm64,aarch64-linux-gnu)
	@echo "sysroot arm64 ready"

$(SYSROOT_DIR)/pc-armv7: | $(SYSROOT_DIR)
	@curl -sso $(SYSROOT_DIR)/alsa-lib_armhf.deb $(DEB_BASE)/libasound2t64_$(ALSA_VER)_armhf.deb
	@curl -sso $(SYSROOT_DIR)/alsa-dev_armhf.deb $(DEB_BASE)/libasound2-dev_$(ALSA_VER)_armhf.deb
	@dpkg-deb -x $(SYSROOT_DIR)/alsa-lib_armhf.deb $(SYSROOT_DIR)/armhf
	@dpkg-deb -x $(SYSROOT_DIR)/alsa-dev_armhf.deb $(SYSROOT_DIR)/armhf
	@$(call gen-pc,pc-armv7,armhf,arm-linux-gnueabihf)
	@echo "sysroot armv7 ready"

define gen-pc
	mkdir -p $(SYSROOT_DIR); printf '#!/bin/sh\ncase " $$* " in\n *" --cflags "*) echo "-I$(abspath $(SYSROOT_DIR))/$(2)/usr/include" ;;\n *" --libs "*) echo "-L$(abspath $(SYSROOT_DIR))/$(2)/usr/lib/$(3) -lasound" ;;\n *) echo "-I$(abspath $(SYSROOT_DIR))/$(2)/usr/include" ;;\nesac\n' > $(SYSROOT_DIR)/$(1); chmod +x $(SYSROOT_DIR)/$(1)
endef

$(SYSROOT_DIR):
	@mkdir -p $@

run: build
	./$(BIN_DIR)/$(BINARY) -config config.yaml

daemon: build
	./$(BIN_DIR)/$(BINARY) -config config.yaml -daemon

stop:
	./$(BIN_DIR)/$(BINARY) -config config.yaml -stop

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

fmt-check:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "not gofmt'ed:"; echo "$$out"; exit 1; fi

clean:
	rm -rf $(BIN_DIR)

distclean: clean
	rm -rf $(SYSROOT_DIR)
