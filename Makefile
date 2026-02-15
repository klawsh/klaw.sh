.PHONY: build test install clean run help

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -s -w -X main.version=$(VERSION)
BINARY := klaw

# Default target
all: build

## build: Build the klaw binary
build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/klaw

## test: Run tests
test:
	go test -v ./...

## install: Install klaw to GOPATH/bin
install:
	go install -ldflags "$(LDFLAGS)" ./cmd/klaw

## clean: Remove build artifacts
clean:
	rm -rf bin/

## run: Build and run klaw chat
run: build
	./bin/$(BINARY) chat

## fmt: Format code
fmt:
	go fmt ./...

## lint: Run linter
lint:
	golangci-lint run

## deps: Download dependencies
deps:
	go mod download
	go mod tidy

## cross: Build for multiple platforms
cross:
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-darwin-amd64 ./cmd/klaw
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-darwin-arm64 ./cmd/klaw
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-linux-amd64 ./cmd/klaw
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-linux-arm64 ./cmd/klaw
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY)-windows-amd64.exe ./cmd/klaw

## release: Build release binaries with checksums
release: clean cross
	@echo ""
	@echo "Creating checksums..."
	cd bin && shasum -a 256 klaw-* > checksums.txt
	@cat bin/checksums.txt
	@echo ""
	@echo "Release binaries ready in bin/"

## dist: Create distribution archives
dist: release
	@echo "Creating archives..."
	cd bin && tar -czf klaw-darwin-amd64.tar.gz klaw-darwin-amd64
	cd bin && tar -czf klaw-darwin-arm64.tar.gz klaw-darwin-arm64
	cd bin && tar -czf klaw-linux-amd64.tar.gz klaw-linux-amd64
	cd bin && tar -czf klaw-linux-arm64.tar.gz klaw-linux-arm64
	cd bin && zip klaw-windows-amd64.zip klaw-windows-amd64.exe
	@echo ""
	@ls -la bin/*.tar.gz bin/*.zip

## help: Show this help
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^## ' Makefile | sed 's/## /  /'
