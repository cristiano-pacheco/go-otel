# Check to see if we can use ash, in Alpine images, or default to BASH.
SHELL_PATH = /bin/ash
SHELL = $(if $(wildcard $(SHELL_PATH)),/bin/ash,/bin/bash)
GO_BIN = $(shell go env GOBIN || true)
GO_BIN := $(if $(GO_BIN),$(GO_BIN),$(shell go env GOPATH)/bin)

.PHONY: all
all: install-libs lint

# ==============================================================================
# Install dependencies

.PHONY: install-libs
install-libs:
	./scripts/install-tools.sh

# ==============================================================================
# Running tests within the local computer

.PHONY: test
test:
	go test -race ./...

.PHONY: static
static: lint vuln-check nilaway

.PHONY: lint
lint:
	./scripts/install-tools.sh golangci-lint
	"$(GO_BIN)/golangci-lint" run ./... --allow-parallel-runners

.PHONY: vuln-check
vuln-check:
	./scripts/install-tools.sh govulncheck
	"$(GO_BIN)/govulncheck" -show verbose ./...

.PHONY: nilaway
nilaway:
	./scripts/install-tools.sh nilaway
	"$(GO_BIN)/nilaway" --include-pkgs="github.com/cristiano-pacheco/go-otel" ./...