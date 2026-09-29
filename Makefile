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
	$(GO) build -trimpath -o bin/gateway-launcher ./cmd/gateway-launcher
	$(GO) build -trimpath -o bin/gateway-update ./cmd/gateway-update
	$(GO) build -trimpath -o bin/gateway-release ./cmd/gateway-release

arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags '-X main.version=$(VERSION)' -o bin/gateway-linux-arm64 ./cmd/gateway
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -o bin/gateway-launcher-linux-arm64 ./cmd/gateway-launcher
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -o bin/gateway-update-linux-arm64 ./cmd/gateway-update
