.PHONY: build test clean install

build:
	@echo "Building binaries..."
	@go build -o bin/droidmcp-filesystem ./cmd/filesystem
	@go build -o bin/droidmcp-github ./cmd/github
	@go build -o bin/droidmcp-scraper ./cmd/scraper
	@go build -o bin/droidmcp-termux ./cmd/termux
	@go build -o bin/droidmcp-network ./cmd/network
	@go build -o bin/droidmcp-clipboard ./cmd/clipboard

build-arm64:
	@chmod +x scripts/build-arm64.sh
	@./scripts/build-arm64.sh

test:
	@go test ./...

clean:
	@rm -rf bin/

install:
	@cp bin/droidmcp-* /data/data/com.termux/files/usr/bin/
