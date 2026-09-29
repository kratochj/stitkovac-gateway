GO ?= go
VERSION ?= dev

.PHONY: test check build arm64
test:
	$(GO) test -race ./...

check:
	$(GO) vet ./...
	test -z "$$($(GO) fmt ./...)"

build:
	$(GO) build -trimpath -ldflags '-X main.version=$(VERSION)' -o bin/gateway ./cmd/gateway

arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags '-X main.version=$(VERSION)' -o bin/gateway-linux-arm64 ./cmd/gateway
