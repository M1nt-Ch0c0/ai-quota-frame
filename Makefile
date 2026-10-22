GO ?= go
BINARY ?= dist/ai-quota-frame
TARGET_GOOS := $(shell $(GO) env GOOS)
# CoreBluetooth requires CGO on macOS. Other desktop builds stay static.
BUILD_CGO_ENABLED ?= $(if $(filter darwin,$(TARGET_GOOS)),1,0)

.PHONY: test race vet build check

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

build:
	mkdir -p $(dir $(BINARY))
	CGO_ENABLED=$(BUILD_CGO_ENABLED) $(GO) build -buildvcs=false -trimpath -ldflags='-s -w' -o $(BINARY) ./cmd/ai-quota-frame

check: test race vet build
