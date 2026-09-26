BIN      := openldap-exporter
MODULE   := github.com/VibhuviOiO/openldap-exporter
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
REVISION ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS  := -s -w -X $(MODULE)/internal/collector.Version=$(VERSION) -X $(MODULE)/internal/collector.Revision=$(REVISION)
IMAGE    ?= ghcr.io/VibhuviOiO/$(BIN)

export GOTOOLCHAIN ?= local
export CGO_ENABLED  = 0

.PHONY: all build test vet fmt lint check run docker release clean

all: fmt vet test build

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BIN) ./cmd/$(BIN)

test:
	go test -race -cover ./...

vet:
	go vet ./...

fmt:
	@test -z "$$(gofmt -l . | tee /dev/stderr)" || (echo "gofmt: files above need formatting" && exit 1)

lint:
	@command -v staticcheck >/dev/null || go install honnef.co/go/tools/cmd/staticcheck@latest
	staticcheck ./...

# Validate the example config and the alert rules without touching a server.
check: build
	./bin/$(BIN) -config openldap-exporter.example.yml -check
	@command -v promtool >/dev/null && promtool check rules alerts/openldap.rules.yml || echo "promtool not installed - rules not validated"

run: build
	./bin/$(BIN) -config openldap-exporter.yml -log.level debug

docker:
	docker build --build-arg VERSION=$(VERSION) --build-arg REVISION=$(REVISION) -t $(IMAGE):$(VERSION) -t $(IMAGE):latest .

# Static binaries for the usual targets.
release:
	@mkdir -p dist
	@for os in linux darwin; do for arch in amd64 arm64; do \
	  echo "  $$os/$$arch"; \
	  GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' -o dist/$(BIN)-$(VERSION)-$$os-$$arch ./cmd/$(BIN); \
	done; done
	@cd dist && shasum -a 256 * > sha256sums.txt && cat sha256sums.txt

clean:
	rm -rf bin dist
