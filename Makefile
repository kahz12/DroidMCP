.PHONY: build test clean install

build:
	@echo "Building binaries..."
	@go build -o bin/droidmcp-filesystem ./cmd/filesystem

build-arm64:
	@chmod +x scripts/build-arm64.sh
	@./scripts/build-arm64.sh

test:
	@go test ./...

clean:
	@rm -rf bin/

install:
	@cp bin/droidmcp-* /data/data/com.termux/files/usr/bin/
