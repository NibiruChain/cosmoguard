VERSION ?= $(shell git describe --tags --abbrev=0)
COMMIT ?= $(shell git rev-parse HEAD)
BUILD_TARGETS := build install

all: install

$(BUILD_TARGETS):
	CGO_ENABLED=0 go $@ \
		-mod=readonly \
		-ldflags="-s -w -X github.com/NibiruChain/cosmoguard/pkg/cosmoguard.Version=$(VERSION) -X github.com/NibiruChain/cosmoguard/pkg/cosmoguard.CommitHash=$(COMMIT)" \
		./cmd/cosmoguard

mod:
	go mod tidy

test: mod
	go test ./...

clean:
	rm -rf $(BUILDDIR)/

.PHONY: all $(BUILD_TARGETS) test clean