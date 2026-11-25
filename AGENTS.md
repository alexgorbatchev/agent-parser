# agent-parser

- Run `just check` before committing. It runs formatting, Clippy, tests, the 90% line and function coverage gates, and the harness schema typecheck.
- When changing parser behavior, update the behavioral tests and the affected native format definitions in [the harness schemas](docs/harness-logs/).
- Keep parser output faithful to the recorded input. Preserve native usage counts, question call IDs, media, Claude Code consumption evidence, and live result records. Callers determine subprocess exit success and model catalog enrichment.
- Open harness databases read-only. Preserve bounded stream correlation, including deduplication of multi-block Claude Code usage records.
- Publish required releases, tags, and packages after the applicable checks pass. Require explicit authorization for production deployments.
