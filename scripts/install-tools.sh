#!/bin/sh
set -eu

install_tool() {
    case "$1" in
        golangci-lint) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest ;;
        govulncheck) go install golang.org/x/vuln/cmd/govulncheck@latest ;;
        mockery) go install github.com/vektra/mockery/v3@latest ;;
        swag) go install github.com/swaggo/swag/cmd/swag@latest ;;
        nilaway) go install go.uber.org/nilaway/cmd/nilaway@latest ;;
        *) echo "Unknown tool: $1" >&2; exit 1 ;;
    esac
}

if [ "$#" -eq 0 ]; then
    set -- golangci-lint govulncheck mockery swag nilaway
fi

for tool in "$@"; do
    install_tool "$tool"
done
