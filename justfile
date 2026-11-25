default:
    @just --list

setup:
    cargo fetch --locked

fmt:
    cargo fmt --package agent-parser

fmt-check:
    cargo fmt --package agent-parser -- --check

lint:
    cargo clippy --all-targets --locked -- -D warnings

test:
    cargo test --all-targets --locked
    cargo test --doc --locked

# This repository contains one library; README is a compiled documentation input.
test-affected base="origin/main":
    #!/usr/bin/env bash
    set -euo pipefail
    base=$(git merge-base {{ quote(base) }} HEAD)
    files=$(git diff --name-only "$base" --; git ls-files --others --exclude-standard)
    affected=false
    while IFS= read -r file; do
        case "$file" in
            ""|docs/*|LICENSE) ;;
            *) affected=true ;;
        esac
    done <<< "$files"
    if "$affected"; then
        echo 'Selected agent-parser: source, tests, compiled README, or package configuration changed.'
        just test
    else
        echo 'No test inputs changed.'
    fi

coverage:
    cargo llvm-cov --all-targets --locked --ignore-filename-regex '(^|/)(tests|scripts)/' --fail-under-lines 90 --fail-under-functions 90

check: fmt-check lint test coverage

build:
    cargo build --all-targets --locked

docs:
    cargo doc --no-deps --locked
