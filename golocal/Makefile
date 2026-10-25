# Build the June local server as a static binary for the on-oven target (ARMv7,
# Tegra K1 / Jetson TK1) and for Docker/NAS (amd64).
#
#   make oven    -> golocal/bin/june-local-linux-armv7  (static, runs on Android 5 bionic)
#   make docker  -> golocal/bin/june-local-linux-amd64  (static)
#
# Override GO if your toolchain lives elsewhere: make GO=/usr/local/go/bin/go

GO ?= go
GOENV = CGO_ENABLED=0
VERSION ?= 0.9.0
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS = -s -w -X main.appVersion=$(VERSION) -X main.appCommit=$(COMMIT) -X main.appBuild=$(BUILD_DATE)

.PHONY: oven camerad docker tools client-windows client-linux test clean

oven:
	mkdir -p bin
	GOOS=linux GOARCH=arm GOARM=7 $(GOENV) $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/june-local-linux-armv7 .

camerad:
	mkdir -p bin
	GOOS=linux GOARCH=arm GOARM=7 $(GOENV) $(GO) build -trimpath -ldflags "-s -w" -o bin/june-camerad-linux-armv7 ./camerad

docker:
	mkdir -p bin
	GOOS=linux GOARCH=amd64 $(GOENV) $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/june-local-linux-amd64 .

tools:
	mkdir -p bin
	$(GO) build -trimpath -ldflags "-s -w" -o bin/june-update-publisher ./cmd/june-update-publisher
	$(GO) build -trimpath -ldflags "-s -w" -o bin/june-wireless-update ./cmd/june-wireless-update

client-windows:
	mkdir -p bin
	GOOS=windows GOARCH=amd64 $(GOENV) $(GO) build -trimpath -ldflags "-s -w" -o bin/june-wireless-update-windows-amd64.exe ./cmd/june-wireless-update

client-linux:
	mkdir -p bin
	GOOS=linux GOARCH=amd64 $(GOENV) $(GO) build -trimpath -ldflags "-s -w" -o bin/june-wireless-update-linux-amd64 ./cmd/june-wireless-update

test:
	$(GO) test ./...

clean:
	rm -rf bin
