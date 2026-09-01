GO ?= go
BINARY ?= dist/ai-quota-frame

.PHONY: test race vet build check demo

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

build:
	mkdir -p $(dir $(BINARY))
	CGO_ENABLED=0 $(GO) build -buildvcs=false -trimpath -ldflags='-s -w' -o $(BINARY) ./cmd/ai-quota-frame

check: test race vet build

demo:
	DEMO_MODE=true FRAME_ACCESS_TOKEN=demo-frame-token $(GO) run ./cmd/ai-quota-frame
