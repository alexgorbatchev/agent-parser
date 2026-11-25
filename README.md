`agent-parser` turns Claude Code, Pi, Codex, Antigravity (`agy`), and OpenCode transcripts into typed Rust events for session viewers, observers, and usage analysis.

The [canonical harness schemas](docs/harness-logs) describe the native formats.

# What It Does

- **Transcript parsing**: Converts individual JSONL records into normalized lifecycle, text, reasoning, tool, question, summary, usage, and error events.
- **Streaming state**: Correlates Claude Code usage blocks and carries Codex model and rate-limit state between records.
- **Headless output**: Normalizes live Claude Code, Codex, and Antigravity streams and retains the latest runner result.
- **Session discovery**: Finds persisted transcripts and OpenCode sessions, including archives, forks, and subagents.
- **File changes**: Normalizes write, edit, and delete operations, including recorded Codex file moves and unified diff hunks.
- **OpenCode snapshots**: Reads sessions, messages, parts, projects, and pending permissions from SQLite, and reconstructs chronological event timelines.
- **Antigravity metadata**: Reads conversation summaries, parent links, and workspace paths from native SQLite stores and legacy JSON sidecars.

# How It Works

1. Feed each JSONL record to the parser for its harness, retaining one stateful parser for a Claude Code, Codex, or Antigravity stream.
2. Consume the returned events, preserving their timestamps, recorded tool inputs, token counts, and optional error details.
3. For OpenCode, open a database and request a session snapshot or a reconstructed event timeline.

# How it Really Works

- JSONL parsing is synchronous and operates on the bytes supplied by the caller. It reads no files, environment variables, credentials, network services, or model catalogs.
- Claude Code deduplicates usage by session and request/message identity. Its bounded FIFO remembers up to 200 identities and evicts the oldest 100 at capacity. Text and tool events retain their source semantics. `reset()` clears correlation state.
- Codex retains the recorded model, context window, active rate-limit reset, and turn state between calls. It pairs the first prompt with the turn start and emits subsequent prompts as `message_absorbed`. Stateful parsing skips inherited records when a subagent supplies its first ordinal.
- Codex emits per-response token counts as `usage` and closes turns with `task_complete` or `turn_aborted`. The stateful parser suppresses repeated cumulative counts, tracks quota refreshes, and pairs paginated history items with their recorded prompts and tool results.
- `TokenUsage.reasoning` is the part of output spent on reasoning. Computed totals count it once. Claude Code reads this count from `output_tokens_details` when present.
- Image-bearing messages and tool results can include `EventData.images` with inline data, a URL, or a harness file ID.
- Antigravity reads native `USER_INPUT`, `PLANNER_RESPONSE`, `GENERIC`, and `SYSTEM_MESSAGE` records from full or compact transcripts. Tool results match pending calls in recorded order; a new user turn clears pending calls. Token totals include input, output, and cache reads; reasoning remains separate. Token-total overflow returns an error without changing parser state.
- Parsers require mutable access. Use separate instances for independent streams, and synchronize externally when sharing an instance between threads.
- Skill events identify user and model invocations. Detection uses recorded tool inputs and parses literal shell arguments without executing transcript commands.
- Session discovery reads harness-specific directories and OpenCode databases. It returns partial results alongside filesystem and decoding errors. Compressed transcripts stream through the same buffered interface as plain files.
- Event timestamps are milliseconds since the Unix epoch. Missing envelope timestamps fall back to the current time; Claude Code first inherits its last recorded timestamp. Codex lifecycle timestamps recorded as integer seconds are converted to milliseconds.
- OpenCode filesystem SQLite databases open read-only with a five-second busy timeout. Snapshot reads use one transaction; callers can interrupt running queries through `interrupt_handle()`. SQLite calls block. `:memory:` creates an isolated in-memory database; `new(connection)` keeps the supplied connection's access policy.
- Antigravity metadata reads open existing SQLite files read-only with a 100 ms busy timeout. Summary reads use one transaction, support an absent parent-index table, and retain database errors. Workspace reads prefer a nonempty legacy JSON `cwd`, then decode the native trajectory protobuf, limited to 1 MiB. File URIs become platform paths with percent escapes decoded. These helpers read only the paths supplied by the caller; they do not discover sessions or launch the harness.
- OpenCode token totals include reasoning once. Session totals use the stored OpenCode version. Timelines also use each message's recorded total to detect the accounting format. Session totals outside the signed 64-bit range return a typed SQLite conversion error.
- Known fields with incorrect JSON types return typed errors. Unknown record types produce no events. Unknown tool parameters remain in `tool_input`.
- Single-file normalization returns `None` for multi-file unified patches, preserving the original patch in the event's tool input. It does not attribute unrelated hunks to one path.

# Installation

Run this from the consuming Rust project.

```sh
cargo add agent-parser --git https://github.com/alexgorbatchev/agent-parser.git
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

`LiveParser::new(harness)` accepts `claude-code`, `codex`, or `agy` and returns a parser for headless output. Feed the parser one record at a time with `parse_line` and retrieve its latest normalized runner result with `result()`.

`claude_code_activity_timestamp`, `codex_activity_timestamp`, `pi_activity_timestamp`, and `agy_activity_timestamp` extract native activity timestamps in Unix milliseconds when the record represents activity.

### Discovery and transcript input

`discover_sessions(&DiscoverOptions)` returns `Discovery` with session sources and errors. Supply a home directory and any harness filters. An empty filter includes every supported harness. `discover_sessions_until` also accepts a cancellation callback.

`open_transcript(path)` returns a buffered reader for plain or Zstandard-compressed transcripts. Open SQLite session sources through `OpencodeDb`.

### Prompt and path helpers

`clean_claude_code_prompt` and `clean_codex_prompt` remove recognized leading synthetic context blocks. `extract_claude_code_title_from_prompt`, `extract_codex_title_from_prompt`, and `extract_pi_title_from_prompt` return an optional first-line title of up to 100 Unicode characters.

`parse_codex_rollout_path(path: &str) -> Result<CodexRolloutFilenameInfo, Error>` extracts the timestamp, session identity, and optional date partition from flat or partitioned rollout paths, including Windows separators.

`normalize_file_modification(tool_name: &str, input: Option<&serde_json::Map<String, serde_json::Value>>) -> Option<FileModification>` returns single-file `EDIT` or `WRITE` data. Diff line types are `add`, `del`, and `ctx`.

`parse_go_duration` reads a signed Go-style duration in nanoseconds. It returns `None` for unknown units, invalid syntax, or values outside the signed 64-bit range.

### OpenCode

`OpencodeDb::open(path)` opens a database. `get_sessions`, `get_session_by_id`, `get_child_sessions`, `get_messages`, `get_parts`, `get_parts_by_message`, `get_projects`, and `get_pending_permissions` return typed rows or `Error`.

`session_snapshot(session_id)` returns `OpencodeSessionSnapshot { session, messages, parts }` from one transaction. `reconstruct_session_events(session_id)` returns its timeline. The free `reconstruct_session_events(&session, &messages, &parts)` accepts an existing snapshot and returns `Vec<ParsedEvent>`.

`connection()` borrows the native database connection; `interrupt_handle()` returns a handle usable from another thread. `close(self)` consumes the database and reports close failures.

`parse_opencode_model(raw)` returns a model ID and optional structured configuration. `parse_opencode_message_data(raw)` and `parse_opencode_part_data(raw)` decode stored JSON payloads.

### Antigravity metadata

`read_agy_summaries(path)` returns `HashMap<String, AgyConversationSummary>` keyed by native conversation ID. Entries retain the title, status, parent ID, workspace paths, optional last-modified Unix milliseconds, and native idle/killed flags.

`read_agy_conversation_workspaces(conversations_directory, conversation_id)` returns `Vec<PathBuf>` from the legacy sidecar or trajectory database. The ID must be a single path component. Missing databases are errors and are never created.

The [native Antigravity reference types](docs/harness-logs/antigravity.ts) record transcript, database, protobuf, detached-subagent, and headless-output formats with their upstream provenance. Use `AgyParser` for persisted transcripts and `LiveParser` for headless output.

### Events and errors

`ParsedEvent`, `EventData`, `TokenUsage`, `Image`, `FileModification`, `DiffHunk`, `DiffLine`, and the `Opencode*` payload and row types support serialization and deserialization. JSON field names retain the transcript consumer contract, including camelCase event fields and optional omitted values.

`Error::Json` retains malformed JSON details, `Error::Invalid` reports invalid record structure, timestamps, identifiers, or overflowing token totals, and `Error::Sqlite` retains native database failures. `Error::Io` retains sidecar read failures; `Error::Protobuf` retains trajectory decoding failures. Missing sessions return a SQLite query error.

# License

[MIT](LICENSE), copyright 2026 Alex Gorbatchev.
