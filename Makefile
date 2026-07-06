VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: web build run test vet fmt check clean cross

web:
	npm --prefix web install
	npm --prefix web run build

build: web
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o rookery ./cmd/rookery

run: build
	./rookery

test: web
	go test ./...

vet: web
	go vet ./...

fmt:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

check: web fmt vet test

# Release binaries for the two architectures the PRD targets.
cross: web
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o dist/rookery-linux-amd64 ./cmd/rookery
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o dist/rookery-linux-arm64 ./cmd/rookery

clean:
	rm -rf rookery dist web/dist web/node_modules
