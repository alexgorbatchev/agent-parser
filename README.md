`agent-parser` turns Claude Code, Pi, Codex, Antigravity (`agy`), and OpenCode transcripts into typed Rust events for session viewers, observers, and usage analysis.

# What It Does

- **Transcript parsing**: Converts individual JSONL records into normalized lifecycle, text, reasoning, tool, question, summary, usage, and error events.
- **Streaming state**: Correlates Claude Code usage blocks and carries Codex model and rate-limit state between records.
- **File changes**: Normalizes single-file writes, string edits, multiple edits, and unified diff hunks.
- **OpenCode snapshots**: Reads sessions, messages, parts, projects, and pending permissions from SQLite, and reconstructs chronological event timelines.
- **Antigravity metadata**: Reads conversation summaries, parent links, and workspace paths from native SQLite stores and legacy JSON sidecars.

# How It Works

1. Feed each JSONL record to the parser for its harness, retaining one stateful parser for a Claude Code, Codex, or Antigravity stream.
2. Consume the returned events, preserving their timestamps, recorded tool inputs, token counts, and optional error details.
3. For OpenCode, open a database and request a session snapshot or a reconstructed event timeline.

# How it Really Works

- JSONL parsing is synchronous and operates on the bytes supplied by the caller. It reads no files, environment variables, credentials, network services, or model catalogs.
- Claude Code deduplicates usage by session and request/message identity. Its bounded FIFO remembers up to 200 identities and evicts the oldest 100 at capacity. Text and tool events retain their source semantics. `reset()` clears correlation state.
- Codex retains the recorded model, context window, and active rate-limit reset between calls. The stateless `parse_codex_line` helper starts with empty state for each call. Context fullness is not synthesized.
- Antigravity reads native `USER_INPUT`, `PLANNER_RESPONSE`, `GENERIC`, and `SYSTEM_MESSAGE` records from full or compact transcripts. Tool results match pending calls in recorded order; a new user turn clears pending calls. Token totals include input, output, and cache reads; reasoning remains separate. Token-total overflow returns an error without changing parser state.
- Parsers require mutable access. Use separate instances for independent streams, and synchronize externally when sharing an instance between threads.
- Event timestamps are milliseconds since the Unix epoch. Missing envelope timestamps fall back to the current time; Claude Code first inherits its last recorded timestamp. Codex lifecycle timestamps recorded as integer seconds are converted to milliseconds.
- OpenCode filesystem SQLite databases open read-only with a five-second busy timeout. Snapshot reads use one transaction; callers can interrupt running queries through `interrupt_handle()`. SQLite calls block. `:memory:` creates an isolated in-memory database; `new(connection)` keeps the supplied connection's access policy.
- Antigravity metadata reads open existing SQLite files read-only with a 100 ms busy timeout. Summary reads use one transaction, support an absent parent-index table, and retain database errors. Workspace reads prefer a nonempty legacy JSON `cwd`, then decode the native trajectory protobuf, limited to 1 MiB. File URIs become platform paths with percent escapes decoded. These helpers read only the paths supplied by the caller; they do not discover sessions or launch the harness.
- OpenCode session token totals sum input, output, cache reads, and cache writes. Reasoning remains separate. Totals outside the signed 64-bit range return a typed SQLite conversion error.
- Known fields with incorrect JSON types return typed errors. Unknown record types produce no events. Unknown tool parameters remain in `tool_input`.
- Single-file normalization returns `None` for multi-file unified patches, preserving the original patch in the event's tool input. It does not attribute unrelated hunks to one path.

# Installation

From a sibling Rust project in this checkout:

```sh
cargo add agent-parser --path ../agent-parser
```

# Quick Start

```rust
use agent_parser::ClaudeCodeParser;

fn main() -> Result<(), agent_parser::Error> {
    let mut parser = ClaudeCodeParser::new();
    let line = br#"{"type":"user","sessionId":"session","timestamp":1000,"message":{"role":"user","content":"Explain this code"}}"#;
    for event in parser.parse_line(line)? {
        println!("{}: {}", event.timestamp, event.event_type);
    }
    Ok(())
}
```

Output:

```text
1000: run_started
1000: turn_start
```

# API

### Streaming records

`ClaudeCodeParser::new()`, `CodexParser::new()`, and `AgyParser::new()` construct stateful parsers. Each exposes `parse_line(&mut self, line: &[u8]) -> Result<Vec<ParsedEvent>, Error>` and `reset(&mut self)`.

`parse_codex_line(line: &[u8])` and `parse_agy_line(line: &[u8])` parse without retained state. `parse_pi_line(line: &[u8], fallback_cwd: &str)` parses a Pi record, using the supplied directory when the record omits it. Each returns `Result<Vec<ParsedEvent>, Error>`.

### Prompt and path helpers

`clean_claude_code_prompt` and `clean_codex_prompt` remove recognized leading synthetic context blocks. `extract_claude_code_title_from_prompt`, `extract_codex_title_from_prompt`, and `extract_pi_title_from_prompt` return an optional first-line title of up to 100 Unicode characters.

`parse_codex_rollout_path(path: &str) -> Result<CodexRolloutFilenameInfo, Error>` extracts the timestamp, session identity, and optional date partition from flat or partitioned rollout paths, including Windows separators.

`normalize_file_modification(tool_name: &str, input: Option<&serde_json::Map<String, serde_json::Value>>) -> Option<FileModification>` returns single-file `EDIT` or `WRITE` data. Diff line types are `add`, `del`, and `ctx`.

### OpenCode

`OpencodeDb::open(path)` opens a database. `get_sessions`, `get_session_by_id`, `get_child_sessions`, `get_messages`, `get_parts`, `get_parts_by_message`, `get_projects`, and `get_pending_permissions` return typed rows or `Error`.

`session_snapshot(session_id)` returns `OpencodeSessionSnapshot { session, messages, parts }` from one transaction. `reconstruct_session_events(session_id)` returns its timeline. The free `reconstruct_session_events(&session, &messages, &parts)` accepts an existing snapshot and returns `Vec<ParsedEvent>`.

`connection()` borrows the native database connection; `interrupt_handle()` returns a handle usable from another thread. `close(self)` consumes the database and reports close failures.

`parse_opencode_model(raw)` returns a model ID and optional structured configuration. `parse_opencode_message_data(raw)` and `parse_opencode_part_data(raw)` decode stored JSON payloads.

### Antigravity metadata

`read_agy_summaries(path)` returns `HashMap<String, AgyConversationSummary>` keyed by native conversation ID. Entries retain the title, status, parent ID, workspace paths, optional last-modified Unix milliseconds, and native idle/killed flags.

`read_agy_conversation_workspaces(conversations_directory, conversation_id)` returns `Vec<PathBuf>` from the legacy sidecar or trajectory database. The ID must be a single path component. Missing databases are errors and are never created.

The [native Antigravity reference types](https://github.com/alexgorbatchev/agent-parser-rust/blob/main/docs/harness-logs/antigravity.ts) record transcript, database, protobuf, detached-subagent, and headless-output formats with their upstream provenance. `AgyParser` consumes persisted transcript records, not headless stream messages.

### Events and errors

`ParsedEvent`, `EventData`, `TokenUsage`, `FileModification`, `DiffHunk`, `DiffLine`, and the `Opencode*` payload and row types support serialization and deserialization. JSON field names retain the transcript consumer contract, including camelCase event fields and optional omitted values.

`Error::Json` retains malformed JSON details, `Error::Invalid` reports invalid record structure, timestamps, identifiers, or overflowing token totals, and `Error::Sqlite` retains native database failures. `Error::Io` retains sidecar read failures; `Error::Protobuf` retains trajectory decoding failures. Missing sessions return a SQLite query error.

# License

[MIT](https://github.com/alexgorbatchev/agent-parser-rust/blob/main/LICENSE), copyright 2026 Alex Gorbatchev.
