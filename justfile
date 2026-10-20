# Minimum statement coverage the package must keep, enforced by `just coverage`
min_coverage := "90"

# Default recipe: list available tasks
default:
    @just --list

# Run all unit tests with race detector
test:
    go test -v -race ./...

# Run the tests and fail when statement coverage drops below the floor
coverage:
    #!/usr/bin/env bash
    set -euo pipefail
    go test -race -coverprofile=coverage.out ./...
    total="$(go tool cover -func=coverage.out | awk '/^total:/ { print substr($3, 1, length($3) - 1) }')"
    if awk "BEGIN { exit ({{ min_coverage }} <= ${total}) ? 0 : 1 }"; then
        echo "statement coverage ${total}% meets the {{ min_coverage }}% floor"
    else
        echo "statement coverage ${total}% is below the {{ min_coverage }}% floor" >&2
        exit 1
    fi

# Typecheck canonical harness log specifications
typecheck:
    bun x tsc --noEmit docs/harness-logs/*.ts

# Format Go source code
fmt:
    go fmt ./...

# Check module hygiene, vet, and run linter
lint:
    go mod tidy -diff
    go vet ./...
    golangci-lint run

# Run every static check, tests, coverage floor, and schema typecheck
check:
    just lint
    just coverage
    just typecheck

# Prepare dependencies and generated assets for local development
setup:
    go mod download
