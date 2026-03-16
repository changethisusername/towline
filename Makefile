VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
BUILD_DATE ?= $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')

LDFLAGS = -s -w \
  -X main.Version=$(VERSION) \
  -X main.Commit=$(COMMIT) \
  -X main.BuildDate=$(BUILD_DATE)

PLATFORM ?= $(shell go env GOOS)
ARCH ?= $(shell go env GOARCH)

.PHONY: build build-mcp build-cli test test-coverage fmt vet clean

build: build-mcp build-cli

build-mcp:
	@mkdir -p dist
	GOOS=$(PLATFORM) GOARCH=$(ARCH) CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o dist/towline-mcp ./cmd/towline-mcp

build-cli:
	@mkdir -p dist
	GOOS=$(PLATFORM) GOARCH=$(ARCH) CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o dist/towline ./cmd/towline

test:
	go test -v ./internal/... ./pkg/...

test-coverage:
	go test -v -coverprofile=coverage.out ./internal/... ./pkg/...

fmt:
	gofmt -s -w .

vet:
	go vet ./...

clean:
	rm -rf dist/
