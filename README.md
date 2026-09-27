`agent-parser` is a pure Go library that parses raw, heterogeneous telemetry streams, session transcripts, and SQLite databases produced by AI coding agents into a unified sequence of structured events. It provides streaming parsers and offline readers for Claude Code, Pi, OpenAI Codex, and OpenCode without requiring CGo or external runtime services.

# What It Does

- **Multi-harness log parsing**: Streams and decodes log entries from Claude Code (JSONL), Pi (DAG trees), OpenAI Codex (date-partitioned rollout JSONL), and OpenCode (SQLite WAL databases).
- **Tool call and diff normalization**: Converts disparate edit and write operations across harnesses (`Edit`, `MultiEdit`, `Write`, `apply_patch`) into a canonical `FileModification` struct with unified line-by-line diff hunks.
- **Token usage tracking**: Extracts granular token metrics (input, output, cache read/write, reasoning) and harness-reported costs.
- **Session title extraction**: Automatically identifies session titles from user prompts, explicit title records (`ai-title`, `custom-title`), and first human messages while stripping harness-injected synthetic context blocks.
- **Rate limit and error detection**: Captures HTTP status codes, rate limit resets, retry delays, and tool execution failures.
- **Zero CGo overhead**: Reads OpenCode SQLite databases via pure Go WebAssembly drivers (`github.com/ncruces/go-sqlite3`), running anywhere Go compiles.

# How It Works

Consumers feed raw log lines or database handles into the appropriate parser instance. As records are processed, `agent-parser` returns a slice of `ParsedEvent` structs containing standardized event types (`run_started`, `turn_start`, `tool_call`, `tool_result`, `turn_end`, `error`, `thinking`).

When tool calls perform file edits or patches, `agent-parser` parses the parameters into unified diffs (`DiffHunk` and `DiffLine`), providing downstream consumers with consistent change sets regardless of the originating tool or harness format.

# How it Really Works

- **Faithful parsing without synthetic metrics**: `agent-parser` extracts only the fields recorded by the harness. It does not perform external model catalog lookups or calculate synthetic metrics like context window fullness; enrichment is left to downstream consumers.
- **Streaming multi-block deduplication**: Claude Code streams assistant responses across separate lines sharing the same `requestId` and `message.id`. `ClaudeCodeParser` deduplicates usage reporting across blocks so total token counts are never multiplied.
- **Synthetic prompt filtering**: OpenAI Codex prepends `<environment_context>` blocks to transcripts, and Claude Code injects synthetic command sequences. Title extraction routines strip these synthetic headers to retrieve the genuine user intent.
- **In-memory state management**: Parsers (`ClaudeCodeParser`, `CodexParser`) maintain safe in-memory state protected by sync primitives to correlate streaming requests, resolve turn context across lines, and track active models.
- **Read-only SQLite access**: `OpenOpencodeDB` connects to OpenCode's WAL database in read-only mode, querying sessions, messages, and parts without database locks or write side-effects.

# Prerequisites

- Go 1.22 or higher
- Optional: [Bun](https://bun.sh) (only for running type checks against canonical schema specifications in `docs/harness-logs/`)

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
}
```

# API

### Parser Instances and Functions

| Export | Signature | Description |
| :--- | :--- | :--- |
| `NewClaudeCodeParser` | `() *ClaudeCodeParser` | Constructs a Claude Code streaming JSONL parser with multi-block deduplication |
| `ParsePiLine` | `(line []byte, fallbackCWD string) ([]ParsedEvent, error)` | Parses a single Pi session JSONL entry |
| `NewCodexParser` | `() *CodexParser` | Constructs an OpenAI Codex CLI rollout parser with stateful turn tracking |
| `ParseCodexLine` | `(line []byte) ([]ParsedEvent, error)` | Stateless helper parsing an OpenAI Codex line without cross-line context |
| `OpenOpencodeDB` | `(dbPath string) (*OpencodeDB, error)` | Opens an OpenCode SQLite database in read-only mode |
| `NormalizeFileModification` | `(toolName string, toolInput map[string]interface{}) *FileModification` | Normalizes tool parameters into canonical diffs |

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

| Harness | Log Format | Default Location |
| :--- | :--- | :--- |
| **Claude Code** | JSONL stream | `~/.claude/projects/<slug>/<sessionId>.jsonl` |
| **Pi Coding Agent** | JSONL DAG tree | `~/.pi/agent/sessions/--<encoded-cwd>--/<ts>_<id>.jsonl` |
| **OpenAI Codex CLI** | Rollout JSONL | `~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl` |
| **OpenCode** | SQLite WAL database | `${XDG_DATA_HOME:-~/.local/share}/opencode/opencode.db` |

Canonical TypeScript definitions and schema quirks for all four harnesses are documented in [`docs/harness-logs/`](./docs/harness-logs/).

# License

[MIT](LICENSE)
