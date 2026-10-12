---
created_on: 2026-09-20 03:20
last_modified: 2026-09-22 18:00
status: current
---

# Harness Log Specifications & Type Definitions

This directory contains the canonical TypeScript specifications, schemas, data structure descriptions, and parsing contracts for AI coding agent session logs and telemetry.

> **CRITICAL ARCHITECTURAL CONTRACT**:
> The TypeScript definitions in this directory (`claude-code.ts`, `pi.ts`, `codex.ts`, `opencode.ts`) serve as the **canonical type reference** and single source of truth for all harness log formats and cross-version permutations.
> All Go types, unmarshaling structs, and parser implementations across `packages/agent-parser/`, `packages/agent-common/types/`, `packages/agent-observer/`, and `packages/agent-ingester/` **MUST be kept strictly in sync** with these canonical specifications whenever schemas, aliases, or record variants are added or modified.

## Supported Harnesses

| Harness                                       | Storage Format                      | Default On-Disk Location                                                         | Type Definition File                 |
| --------------------------------------------- | ----------------------------------- | -------------------------------------------------------------------------------- | ------------------------------------ |
| **Claude Code** (`@anthropic-ai/claude-code`) | JSONL stream + JSON descriptors     | `~/.claude/projects/<slug>/<sessionId>.jsonl`<br>`~/.claude/sessions/<pid>.json` | [`claude-code.ts`](./claude-code.ts) |
| **Pi** (`@earendil-works/pi-coding-agent`)    | JSONL DAG tree (append + republish) | `~/.pi/agent/sessions/--<encoded-cwd>--/<ts>_<id>.jsonl`                         | [`pi.ts`](./pi.ts)                   |
| **OpenAI Codex CLI**                          | Date-partitioned JSONL rollouts     | `~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl`                         | [`codex.ts`](./codex.ts)             |
| **OpenCode** (`opencode`)                     | Global SQLite WAL Database          | `${XDG_DATA_HOME:-~/.local/share}/opencode/opencode.db`                          | [`opencode.ts`](./opencode.ts)       |

---

## Runnable Validation Commands

```bash
# Typecheck all harness log specification files
bun x tsc --noEmit docs/harness-logs/*.ts

# Typecheck a single harness spec
bun x tsc --noEmit docs/harness-logs/claude-code.ts
bun x tsc --noEmit docs/harness-logs/pi.ts
bun x tsc --noEmit docs/harness-logs/codex.ts
bun x tsc --noEmit docs/harness-logs/opencode.ts
```

---

## Key Gotchas & Implementation Traps

### 1. Storage Paradigm Differences

- **Claude Code**: Flat append-only stream of chronological JSON events per session.
- **Pi**: Directed Acyclic Graph (DAG) tree where entries link via `{id, parentId}`. Ingestion must walk the active branch rather than treating the file as a flat array. Compactions trigger atomic rewrites that alter the file inode.
- **Codex CLI**: Date-partitioned directory tree (`YYYY/MM/DD/`). The first line is always `session_meta`, followed by alternating `turn_context`, `event_msg`, and raw `response_item` entries.
- **OpenCode**: **Single global SQLite database** in WAL mode. There are NO per-session files to scan; readers must connect using read-only mode (`?mode=ro`) to prevent locking the agent.

### 2. Subagent Linkage Models

- **Claude Code**: Nested files under `<projects-store>/<slug>/<sessionId>/subagents/agent-<agentId>.jsonl` with `isSidechain: true`.
- **Codex CLI**: Independent rollout files in the date tree linked bidirectional via `session_meta.parent_thread_id` and `source.subagent.thread_spawn`.
- **Pi**: Separate session JSONL files with header `parentSession` or extension session metadata.
- **OpenCode**: Rows in the same `session` table with `parent_id` pointing to the parent session row.

### 3. Argument String-Encoding (Codex & OpenCode)

- Codex `function_call.arguments` is JSON encoded as a **string**, requiring a second parse pass. Both `function_call_output.output` and `custom_tool_call_output.output` accept strings or ordered content arrays; preserve array bodies as JSON in normalized `toolOutput` strings, including media and unknown blocks.
- Codex task lifecycle `started_at` and `completed_at` numbers are Unix seconds. Convert them to milliseconds for normalized events; retain RFC3339 string support and use the envelope timestamp when the field is absent, null, or empty. Do not apply this seconds conversion to other harness timestamps.
- OpenCode `session.model`, `message.data`, and `part.data` columns are JSON stored as TEXT in SQLite rows.

### 4. Synthetic First Turns

- Codex injects a synthetic `<environment_context>` block as the first `user_message`. Extracting session titles requires filtering out `<environment_context>` and taking the first human message.
- Claude Code emits an explicit `ai-title` record with rolling title updates.

### 5. Schema Aliases & Cross-Version Permutations

- **Claude Code**:
  - `ai-title` records accept `aiTitle`, `title`, and `ai_title` property aliases across versions.
  - `agent-name` records accept `agentName` and `name` property aliases.
  - Editing tools include single-file `Edit`, `Write`, multi-file batch `MultiEdit` (`edits: Array<{old_string, new_string, replace_all?}>`), and Jupyter notebook operations `NotebookEdit` (`notebook_path`, `new_source`, `cell_id?`, `cell_index?`, `edit_mode?`) and `NotebookRead`.
- **Codex CLI**:
  - `session_meta` records accept `id` and legacy `session_id` property aliases.
  - Tool calls in `response_item` support both standard `function_call` / `function_call_output` and external / MCP `custom_tool_call` / `custom_tool_call_output`.
  - Tool call parameters accept both `arguments` (JSON string) and `input` (raw or parsed parameter payload).
- **Pi Coding Agent**:
  - Content block and message role types accept both camelCase and snake_case variants: `toolCall` | `tool_call` and `toolResult` | `tool_result`.

### 6. Streamed Per-Block Assistant Lines (Claude Code)

Claude Code streams multi-block assistant turns by writing each content block as a separate JSONL record line rather than outputting the full response at once. Each block line includes `apiBlockIndex` (0-based integer index, e.g. block 0 for thinking, block 1 for tool_use or text). Crucially, all streaming block lines belonging to a single API response share identical `message.id`, `requestId`, `message.stop_reason`, and cumulative `message.usage`. Parsers and consumers must count usage only once per request rather than summing usage across streaming block lines (#67, #81).

### 7. Pointer Records Without Timestamps (`last-prompt`)

Claude Code transcript directories contain `last-prompt` pointer records that identify the most recent prompt. These records omit timestamps and exist purely as navigation pointers; parsers and consumers must never treat them as conversational prompt events or emit `turn_start` for them (#65).

---

## Primary Source References & GitHub Links

### Claude Code

- **TypeScript parser library**: [`d1ll0n/parse-cc`](https://github.com/d1ll0n/parse-cc/blob/main/src/types/entries.ts#L1-L290)
- **Rust streaming parser**: [`sevenevesai/claude-session-parser`](https://github.com/sevenevesai/claude-session-parser/blob/main/src/lib.rs#L1-L180)
- **Python transcript exporter**: [`simonw/claude-code-transcripts`](https://github.com/simonw/claude-code-transcripts/blob/main/claude_code_transcripts.py#L1-L240)
- **Go TUI viewer**: [`JeiKeiLim/claude-code-log-viewer-cli`](https://github.com/JeiKeiLim/claude-code-log-viewer-cli/blob/main/internal/providers/claude/claude.go#L1-L160)

### Pi Coding Agent

- **Official SessionManager API**: [`earendil-works/pi` (`session-manager.ts`)](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/src/core/session-manager.ts#L1-L320)
- **Session format spec**: [`earendil-works/pi` (`session-format.md`)](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/session-format.md#L1-L180)
- **Message structures**: [`earendil-works/pi` (`messages.ts`)](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/src/core/messages.ts#L1-L150)
- **AI types & usage**: [`earendil-works/pi` (`types.ts`)](https://github.com/earendil-works/pi/blob/main/packages/ai/src/types.ts#L1-L220)
- **Session indexer**: [`catlain/pi-journal`](https://github.com/catlain/pi-journal/blob/main/src/indexer.ts#L1-L140)

### OpenAI Codex CLI

- **Codex rollout schema spec**: [`neochoon/agenthud` (`codex-session.md`)](https://github.com/neochoon/agenthud/blob/main/docs/schemas/codex-session.md#L1-L220)
- **Codex provider**: [`neochoon/agenthud` (`codex.ts`)](https://github.com/neochoon/agenthud/blob/main/src/data/providers/codex.ts#L1-L280)
- **Transcript viewer**: [`prateek/codex-transcripts`](https://github.com/prateek/codex-transcripts/blob/main/codex_transcripts.py#L1-L260)
- **Codex parser**: [`eric050828/claude-code-session-viewer` (`codex-parser.ts`)](https://github.com/eric050828/claude-code-session-viewer/blob/main/lib/codex-parser.ts#L1-L180)

### OpenCode

- **Storage engine**: [`anomalyco/opencode` (`db.ts`)](https://github.com/anomalyco/opencode/blob/main/packages/opencode/src/storage/db.ts#L1-L150)
- **OpenCode schema spec**: [`neochoon/agenthud` (`opencode-session.md`)](https://github.com/neochoon/agenthud/blob/main/docs/schemas/opencode-session.md#L1-L220)
- **SQLite CLI searcher**: [`rmk40/opencode-db-search`](https://github.com/rmk40/opencode-db-search/blob/main/src/db.ts#L1-L110)
- **OpenCode parser**: [`nicknisi/sessions` (`opencode.ts`)](https://github.com/nicknisi/sessions/blob/main/src/opencode.ts#L1-L180)

---

## Boundaries

### Always

- Treat `docs/harness-logs/*.ts` as the canonical type reference; ensure Go types and unmarshaling structs across `packages/agent-parser/`, `packages/agent-common/types/`, and downstream ingest pipelines are kept strictly in sync with these specifications.
- Use strict TypeScript typing: NO `any`, NO unsafe `as Type` casts (use type guards), and declare explicit return types on all exported functions.
- Open SQLite databases for reading with `readonly: true` / URI `?mode=ro`.
- Automatically record any new user instructions or schema changes into the appropriate `AGENTS.md` file without requiring the user to ask (checking first if conflicts exist).
- In code-based projects, any time code is changed such that results from running that code are changed, a test file must be changed as well, and 90% code coverage is required (the `scripts/` folder is explicitly excluded from this rule).

### Ask First

- Modifying cross-harness normalized schemas in `packages/ui-web/src/types/index.ts` or Go structs.
- Adding dependencies to `packages/*`.

### Never

- Never parse or ship credential tokens (e.g. Claude Code `.key` socket tokens or `auth.json` secrets).
- Never publish releases, tags, packages, or production deployments automatically without explicit user authorization.
- Never write to or mutate an agent harness's live storage directory or database.
