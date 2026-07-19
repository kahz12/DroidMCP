.PHONY: build build-arm64 test vet fmt fmt-check lint sec checksums clean install

# Reproducible-build flags shared by every target. -trimpath strips local
# paths from the binary so a build on machine A and machine B with the same
# source tree produce byte-identical output; -s -w drop the symbol and DWARF
# tables; -buildid= clears the random link-time build ID so the result is
# stable across rebuilds.
GOFLAGS_REPRO   := -trimpath
# VERSION is stamped into internal/buildinfo.Version so /healthz and logs report
# the real build. Defaults to the git tag/commit; overridable (e.g. VERSION=v1.2.3).
# Builds from the same commit stamp the same value, preserving reproducibility.
VERSION         ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS_REPRO   := -s -w -buildid= -X github.com/kahz12/droidmcp/internal/buildinfo.Version=$(VERSION)
SERVICES        := filesystem github scraper termux network clipboard media sqlite

build:
	@echo "Building binaries (reproducible, version $(VERSION))..."
	@mkdir -p bin
	@for s in $(SERVICES); do \
		go build $(GOFLAGS_REPRO) -ldflags="$(LDFLAGS_REPRO)" -o bin/droidmcp-$$s ./cmd/$$s; \
	done

build-arm64:
	@chmod +x scripts/build-arm64.sh
	@./scripts/build-arm64.sh

test:
	@go test -race -count=1 ./...

vet:
	@go vet ./...

fmt:
	@gofmt -w .

fmt-check:
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then \
		echo "These files are not gofmt-clean; run 'make fmt':"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

lint:
	@command -v golangci-lint >/dev/null 2>&1 || { echo "golangci-lint not installed"; exit 1; }
	@golangci-lint run --timeout=5m ./...

sec:
	@command -v gosec >/dev/null 2>&1 || { echo "gosec not installed (go install github.com/securego/gosec/v2/cmd/gosec@latest)"; exit 1; }
	@gosec ./...

checksums: build
	@cd bin && sha256sum droidmcp-* > SHA256SUMS && echo "wrote bin/SHA256SUMS"

clean:
	@rm -rf bin/

install:
	@cp bin/droidmcp-* /data/data/com.termux/files/usr/bin/
