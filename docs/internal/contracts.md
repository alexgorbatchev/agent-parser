---
created_on: 2026-10-14 13:12
last_modified: 2026-10-14 13:12
status: current
---

# Parser contracts and verification

This reference is for maintainers of the Rust library and consumers integrating streaming observers. The source behavior is grounded in the [Go agent-parser library](https://github.com/alexgorbatchev/agent-parser), including every public parser and OpenCode database method.

## Native Rust integration

Use typed `ParsedEvent`/`EventData` models directly. Stateful parsers use exclusive mutable access instead of an internal Go mutex. Use `std::sync::Mutex` outside the parser if a consumer shares one stream across threads. There are no legacy API aliases.

OpenCode reads use [rusqlite](https://docs.rs/rusqlite/latest/rusqlite/). The bundled SQLite build is a native library, replacing Go's WebAssembly driver. Filesystem handles are read-only; constructor-supplied handles retain caller policy. All related session snapshot queries execute in one read transaction. Cancellation uses SQLite's [interrupt primitive](https://www.sqlite.org/c3ref/interrupt.html); an interrupt made while no statement runs has no effect on a later statement.

Codex reset durations use [go-parse-duration](https://docs.rs/go-parse-duration/latest/go_parse_duration/), whose parser supports Go duration units. RFC3339/RFC2822 timestamps use [chrono](https://docs.rs/chrono/latest/chrono/). Tool arguments and heterogeneous transcript content retain native `serde_json::Value` representations.

## Verified corrections to source defects

| Source defect | Rust behavior | Regression coverage |
| --- | --- | --- |
| Pi and Codex title truncation slices a UTF-8 string at a byte index | Take up to 100 complete Unicode characters | `titles_clean_synthetic_wrappers_and_preserve_unicode`, `pi_usage_cost_questions_and_unicode_titles` |
| OpenCode reconstruction appends completion/error events after later tool output while describing chronological output | Stable timestamp sort across the complete timeline | `reconstruction_orders_events_and_preserves_structured_errors`, `tool_states_errors_and_streamed_text_are_preserved` |
| Session, messages, and parts are queried independently during database reconstruction | Read related rows in one transaction and expose the typed snapshot to live observers | `database_reads_real_rows_without_writing` |
| Unified patches spanning multiple files assign every hunk to the final path | Return no singular normalized modification; retain the original tool input | `unified_patch_rejects_cross_file_hunk_misattribution` |
| A trailing patch newline creates a phantom context line | Split terminated lines without fabricating a final empty context line | `unified_patch_rejects_cross_file_hunk_misattribution` |
| Permission JSON decoding errors are discarded | Return the JSON error | `sqlite_reports_corruption_and_interrupts_running_queries` |

JSONL records must be objects; root `null` is rejected instead of being accepted as a zero-value Go record. Recognized fields retain source type checks; unknown fields remain forward-compatible. OpenCode query ordering uses row IDs to break timestamp ties deterministically.

## Boundaries

The library parses supplied records; it does not tail files, reconstruct Pi DAG branches, resolve model catalogs, calculate prices, or perform telemetry. Its singular file-modification event cannot represent a multi-file patch; the raw tool input is retained for consumers that need a richer change model. Native Codex custom patches outside the source library's supported unified-diff grammar remain raw tool inputs.

Codex tool result error classification retains the source's text heuristics because those records do not expose a uniform structured exit code. OpenCode session metadata retains best-effort decoding; malformed optional metadata is omitted. These limitations are distinct from structured API error classification and SQLite query failures.

## Development checks

Run `just check` for package-scoped formatting, Clippy warnings, all target and documentation tests, and measured line coverage of at least 90%. Run `just build` to compile all targets. Keep diagnostic logs under `.tmp/`, outside version control.

The regression suite uses real SQLite files, verifies writes are rejected, and interrupts a running recursive query. It does not access a user's live session databases.

`tests/fixtures.jsonl` contains 138 input/output reference cases extracted from JSON record literals in the Go library's Claude Code, Pi, and Codex tests. Root timestamps are set to 1000 milliseconds to remove wall-clock dependence; nested source timestamps remain intact. Expected events are actual Go parser output, compared as JSON values without normalization by `tests/reference.rs`. Stateful multi-record behavior, malformed known fields, corrections above, and SQLite readers have separate behavioral tests. The fixtures retain source MIT attribution in `LICENSE`.
