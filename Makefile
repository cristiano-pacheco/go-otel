# Check to see if we can use ash, in Alpine images, or default to BASH.
SHELL_PATH = /bin/ash
SHELL = $(if $(wildcard $(SHELL_PATH)),/bin/ash,/bin/bash)

.PHONY: all
all: install-libs lint

# ==============================================================================
# Install dependencies

.PHONY: install-libs
install-libs:
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install golang.org/x/vuln/cmd/govulncheck@latest
	go install go.uber.org/nilaway/cmd/nilaway@latest

# ==============================================================================
# Running tests within the local computer

.PHONY: static
static: lint vuln-check nilaway

.PHONY: lint
lint:
	golangci-lint run ./... --allow-parallel-runners

.PHONY: vuln-check
vuln-check:
	govulncheck -show verbose ./... 

.PHONY: nilaway
nilaway:
	nilaway --include-pkgs="github.com/cristiano-pacheco/go-otel" ./...