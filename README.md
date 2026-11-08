`agent-parser` is a pure Go library that parses raw, heterogeneous telemetry streams, session transcripts, and SQLite databases produced by AI coding agents into a unified sequence of structured events. It provides streaming parsers and offline readers for Claude Code, Pi, OpenAI Codex, and OpenCode without requiring CGo or external runtime services.

# What It Does

- **Multi-harness log parsing**: Streams and decodes log entries from Claude Code (JSONL), Pi (DAG trees), OpenAI Codex (date-partitioned rollout JSONL), and OpenCode (SQLite WAL databases).
- **Tool call and diff normalization**: Converts disparate edit and write operations across harnesses (`Edit`, `MultiEdit`, `Write`, `apply_patch`) into a canonical `FileModification` struct with unified line-by-line diff hunks.
- **Images**: Images attached to prompts and tool results arrive in `EventData.Images` as base64 data with its media type, or as the URL or file ID the harness referenced, for Claude Code, Pi, and OpenAI Codex. A prompt with only an image still starts a turn.
- **Token usage tracking**: Extracts granular token metrics (input, output, cache read/write, reasoning) and harness-reported costs.
- **Session title extraction**: Automatically identifies session titles from user prompts, explicit title records (`ai-title`, `custom-title`), and first human messages while stripping harness-injected synthetic context blocks.
- **Rate limit and error detection**: Captures HTTP status codes, rate limit resets, retry delays, and tool execution failures. A tool result's `IsError` is nil when the transcript records no outcome, such as a Codex call that is still running, was aborted, or wrote no status header.
- **Native recaps and questions**: Emits Claude Code `system/away_summary` text as `summary` events. Codex `request_user_input`, `request_user_input_async`, and completed `AgentMessage` questions produce `question_prompt` events; ordinary completed agent messages produce `agent_message` events.
- **Skill invocations**: `EventData.Skills` lists the agent skills an event invokes, with the skill name, whether the model or the user invoked it, and its path when recorded. Model loads appear on the tool call that loads the skill (Claude Code `Skill`, OpenCode `skill`, Pi `read` of a `SKILL.md` from line 1). User invocations appear on the user turn that runs one (Claude Code slash commands, Pi `/skill:name`). Codex records neither on an existing event: its `$name` mentions and shell reads of skills' `SKILL.md` files emit separate `skill_invoked` events.
- **Session discovery**: `DiscoverSessions` lists the sessions each harness stores on disk, read-only, using the harness's own locations and environment overrides: Claude Code projects with subagents, Codex sessions and `archived_sessions`, Pi sessions with forks, and OpenCode session rows. `OpenTranscript` reads a transcript and transparently decompresses Codex's zstd `.jsonl.zst` rollouts.
- **Zero CGo overhead**: Reads OpenCode SQLite databases through the pure Go `modernc.org/sqlite` driver and zstd transcripts through `github.com/klauspost/compress/zstd`, so consumers can cross-compile with `CGO_ENABLED=0`.

# How It Works

Consumers feed raw log lines or database handles into the appropriate parser instance. As records are processed, `agent-parser` returns a slice of `ParsedEvent` structs containing standardized event types (`run_started`, `turn_start`, `agent_message`, `question_prompt`, `thinking`, `tool_call`, `tool_result`, `file_modification`, `skill_invoked`, `model_change`, `title_change`, `summary`, `message_absorbed`, `usage`, `turn_end`, `error`). Every harness parser uses this same vocabulary. `usage` reports token usage, context window, and rate-limit metadata for one model response without ending the turn; harnesses that report usage on the response that ends a turn attach it to `turn_end` instead. A tool call that edits one file carries its `FileModification`; a Codex patch, which can change several files, is followed by one `file_modification` event per file it changed, correlated by `ToolCallID`.

When tool calls perform file edits or patches, `agent-parser` parses the parameters into unified diffs (`DiffHunk` and `DiffLine`), providing downstream consumers with consistent change sets regardless of the originating tool or harness format.

# How it Really Works

- **Faithful parsing without synthetic metrics**: `agent-parser` extracts only the fields recorded by the harness. It does not perform external model catalog lookups or calculate synthetic metrics like context window fullness; enrichment is left to downstream consumers.
- **Streaming multi-block deduplication**: Claude Code streams assistant responses across separate lines sharing the same `requestId` and `message.id`. `ClaudeCodeParser` deduplicates usage reporting across blocks so total token counts are never multiplied.
- **Synthetic prompt filtering**: OpenAI Codex records context it injects for the model (instructions, environment state, skills, notifications) as user-role messages, and Claude Code injects synthetic command sequences. For paginated Codex rollouts, `CodexParser` takes prompts from Codex's completed `UserMessage` items, which leave that context out; otherwise it applies Codex's contextual-fragment rule to user messages. Title extraction also strips leading `<environment_context>` blocks.
- **In-memory state management**: Parsers (`ClaudeCodeParser`, `CodexParser`) maintain safe in-memory state protected by sync primitives to correlate streaming requests, resolve turn context across lines, and track active models.
- **Read-only SQLite access**: `OpenOpencodeDB` connects to OpenCode's WAL database in read-only mode, querying sessions, messages, and parts without database locks or write side-effects.

# Prerequisites

- Go 1.26.2 or higher
- [Bun](https://bun.sh) for contributors: `just check` typechecks the canonical schema specifications in `docs/harness-logs/` with a pinned TypeScript version. Code that only imports the library does not need Bun.

# Installation

```bash
go get github.com/alexgorbatchev/agent-parser
```

# Quick Start

```go
package main

import (
	"bufio"
	"fmt"
	"log"
	"os"

	"github.com/alexgorbatchev/agent-parser"
)

func main() {
	file, err := os.Open("session.jsonl")
	if err != nil {
		panic(err)
	}
	defer file.Close()

	parser := parser.NewClaudeCodeParser()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<20), 64<<20) // transcript lines often exceed bufio's 64 KiB default

	for scanner.Scan() {
		events, err := parser.ParseLine(scanner.Bytes())
		if err != nil {
			continue
		}

		for _, ev := range events {
			fmt.Printf("[%s] Type: %s", ev.EventType, ev.EventType)
			if ev.Data.ToolName != nil {
				fmt.Printf(" Tool: %s", *ev.Data.ToolName)
			}
			if ev.Data.FileModification != nil {
				fmt.Printf(" File: %s (%s)", ev.Data.FileModification.FilePath, ev.Data.FileModification.Action)
			}
			fmt.Println()
		}
	}
	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}
}
```

# API

### Parser Instances and Functions

| Export | Signature | Description |
| :--- | :--- | :--- |
| `NewClaudeCodeParser` | `() *ClaudeCodeParser` | Constructs a Claude Code streaming JSONL parser with multi-block deduplication |
| `ParsePiLine` | `(line []byte, fallbackCWD string) ([]ParsedEvent, error)` | Parses a single Pi session JSONL entry |
| `NewCodexParser` | `() *CodexParser` | Constructs an OpenAI Codex CLI rollout parser with stateful turn tracking |
| `ParseCodexLine` | `(line []byte) ([]ParsedEvent, error)` | Stateless helper parsing one OpenAI Codex line without cross-line context. It cannot apply rules that need the whole rollout (turn boundaries, forked-subagent inherited history, usage re-send detection, paginated prompt and message sources, nested tool items); use `CodexParser` to parse a rollout |
| `OpenOpencodeDB` | `(dbPath string) (*OpencodeDB, error)` | Opens an OpenCode SQLite database in read-only mode |
| `NormalizeFileModification` | `(toolName string, toolInput map[string]interface{}) *FileModification` | Normalizes tool parameters into canonical diffs |

### Activity Timestamps

Each function accepts one native JSONL record and returns `(timestamp int64, found bool, err error)`. The timestamp is Unix milliseconds from the record's outer `timestamp` string.

| Export | Activity records |
| :--- | :--- |
| `ClaudeCodeActivityTimestamp` | `user`, `assistant`, `attachment`, `system`, and `progress` |
| `PiActivityTimestamp` | `message`, `custom_message`, `compaction`, and `branch_summary` |
| `CodexActivityTimestamp` | `turn_context` and the `event_msg` / `response_item` payload variants in the [canonical Codex schema](docs/harness-logs/codex.ts) |

Metadata and unknown record types return `found=false`. Missing, null, or empty timestamps also return `found=false`; malformed JSON and invalid timestamps on activity records return an error. Valid timestamps require an RFC3339 timezone and may contain fractional seconds. Numeric timestamps and zone-less strings are rejected. An epoch timestamp returns `0, true, nil`.

Pi's nested message timestamp describes message creation, and Codex task payload timestamps describe lifecycle boundaries; neither overrides the outer record time. These functions read one record without changing parser state, scanning files, or substituting the current time. Callers choose traversal order and handle records without usable activity times.

### Session Discovery

| Export | Signature | Description |
| :--- | :--- | :--- |
| `DiscoverSessions` | `(ctx context.Context, opts DiscoverOptions) ([]SessionSource, error)` | Lists stored sessions for the selected harnesses, read-only |
| `DiscoverOptions` | `struct { Harnesses []Harness; Home string }` | Empty `Harnesses` means every supported harness; empty `Home` means the user's home directory |
| `SessionSource` | `struct` | `Harness`, `SessionID`, `ParentSessionID`, `Path`, `Subagent`, `Fork`, `ForkedAt`, `Archived`, `ModTime` |
| `Harness` | `string` | `HarnessClaudeCode`, `HarnessCodex`, `HarnessPi`, `HarnessOpencode` |
| `SupportedHarnesses` | `() []Harness` | Returns a new slice of every supported harness |
| `OpenTranscript` | `(path string) (io.ReadCloser, error)` | Opens a transcript, decompressing zstd `.jsonl.zst` files |

```go
sources, err := parser.DiscoverSessions(ctx, parser.DiscoverOptions{Harnesses: []parser.Harness{parser.HarnessCodex}})
if err != nil {
	log.Printf("some sessions could not be listed: %v", err) // sources still holds everything found
}
for _, source := range sources {
	reader, err := parser.OpenTranscript(source.Path)
	if err != nil {
		continue
	}
	codex := parser.NewCodexParser()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 1<<20), 64<<20) // transcript lines often exceed bufio's 64 KiB default
	for scanner.Scan() {
		events, _ := codex.ParseLine(scanner.Bytes())
		_ = events
	}
	if err := scanner.Err(); err != nil {
		log.Printf("reading %s: %v", source.Path, err)
	}
	_ = reader.Close()
}
```

A fork (`Fork`) is a Claude Code fork subagent or a Pi fork. Its transcript can replay the parent's history before its own entries, so `ForkedAt` gives the time the fork was created: the parent's spawning tool call for Claude Code, the session header for Pi. Drop events at or before `ForkedAt` to avoid counting the parent's history twice; a copied Claude Code fork replays the spawning call itself with exactly that time. Results are sorted by harness, path, and session ID. Missing locations are skipped silently. Unreadable directories, malformed Pi headers, Claude Code subagents whose `.meta.json` is missing or unreadable, Claude Code forks whose spawn cannot be found, and unreadable OpenCode databases are joined into the returned error while every session, including those forks, is still returned. Symlinked root directories are followed, and `Path` keeps the configured root. Discovery parses only what identifies a session: the first record of Pi subagent runs and forks, Claude Code subagent `.meta.json` files, and, for a Claude Code fork, the parent record holding its spawning tool call. OpenCode sources share one database `Path` per session row: read them with `OpenOpencodeDB` and `ReconstructSessionEvents`, not `OpenTranscript`.

### Core Types

```go
type ParsedEvent struct {
	EventType string    `json:"eventType"`
	Timestamp int64     `json:"timestamp"`
	CWD       string    `json:"cwd"`
	GitBranch string    `json:"gitBranch"`
	Data      EventData `json:"data"`
}

type Event = ParsedEvent

type FileModification struct {
	Action   string     `json:"action"` // "EDIT" | "WRITE"
	FilePath string     `json:"filePath"`
	Hunks    []DiffHunk `json:"hunks,omitempty"`
	Content  *string    `json:"content,omitempty"`
}

type Image struct {
	MediaType string `json:"mediaType,omitempty"` // with Data
	Data      string `json:"data,omitempty"`      // base64
	URL       string `json:"url,omitempty"`
	FileID    string `json:"fileId,omitempty"`
}

type TokenUsage struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Reasoning  int64 `json:"reasoning"`
	Total      int64 `json:"total"`
}
```

# Supported Harnesses

| Harness | Log Format | Locations discovered by `DiscoverSessions` | Overrides |
| :--- | :--- | :--- | :--- |
| **Claude Code** | JSONL stream | `~/.claude/projects/<slug>/<sessionId>.jsonl`; subagents at `<slug>/<sessionId>/subagents/[workflows/<run>/]agent-<id>.jsonl` (forks identified by their `.meta.json`) and legacy `<slug>/agent-<id>.jsonl` | `CLAUDE_CONFIG_DIR` (searched before `~/.claude`) |
| **Pi Coding Agent** | JSONL DAG tree | `~/.pi/agent/sessions/--<encoded-cwd>--/<ts>_<id>.jsonl`; forks at `<ts>_<parent>/forks/<ts>_<id>.jsonl`; subagent runs at `<ts>_<parent>/<run>/run-<n>/session.jsonl` | `PI_SESSIONS_DIR`, then `PI_CODING_AGENT_DIR/sessions` |
| **OpenAI Codex CLI** | Rollout JSONL, plain or zstd `.jsonl.zst` | `~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl[.zst]` and `~/.codex/archived_sessions/rollout-*.jsonl[.zst]` | `CODEX_SESSIONS_DIR`, `CODEX_HOME` |
| **OpenCode** | SQLite WAL database | `${XDG_DATA_HOME:-~/.local/share}/opencode/opencode.db` and channel `opencode-<channel>.db` | `OPENCODE_DB_PATH`, then OpenCode's `OPENCODE_DB` (absolute or relative to the data directory) |

Canonical TypeScript definitions and schema quirks for all four harnesses are documented in [`docs/harness-logs/`](./docs/harness-logs/).

# License

[MIT](LICENSE)
