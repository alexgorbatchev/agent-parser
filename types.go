package parser

import "encoding/json"

// DiffLine represents a single line inside a unified diff hunk.
type DiffLine struct {
	Type          string `json:"type"` // "add" | "del" | "ctx"
	OldLineNumber *int   `json:"oldLineNumber,omitempty"`
	NewLineNumber *int   `json:"newLineNumber,omitempty"`
	Content       string `json:"content"`
}

// DiffHunk represents a unified diff hunk with its header and lines.
type DiffHunk struct {
	Header string     `json:"header,omitempty"`
	Lines  []DiffLine `json:"lines"`
}

// FileModification captures structured file changes parsed from agent tool calls.
type FileModification struct {
	Action   string     `json:"action"` // "EDIT" | "WRITE"
	FilePath string     `json:"filePath"`
	Hunks    []DiffHunk `json:"hunks,omitempty"`
	Content  *string    `json:"content,omitempty"`
}

// TokenUsage tracks token consumption metrics for an agent run or turn.
type TokenUsage struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Reasoning  int64 `json:"reasoning"`
	Total      int64 `json:"total"`
}

// EventData holds the normalized payload for a parsed agent event.
type EventData struct {
	Delta              *string                `json:"delta,omitempty"`
	Result             *LiveResult            `json:"result,omitempty"`
	Native             json.RawMessage        `json:"native,omitempty"`
	Role               *string                `json:"role,omitempty"`
	Content            *string                `json:"content,omitempty"`
	Title              *string                `json:"title,omitempty"`
	LastPrompt         *string                `json:"lastPrompt,omitempty"`
	Model              *string                `json:"model,omitempty"`
	ModelContextWindow *int64                 `json:"modelContextWindow,omitempty"`
	ContextFullness    *float64               `json:"contextFullness,omitempty"`
	ToolName           *string                `json:"toolName,omitempty"`
	ToolCallID         *string                `json:"toolCallId,omitempty"`
	ToolInput          map[string]interface{} `json:"toolInput,omitempty"`
	FileModification   *FileModification      `json:"fileModification,omitempty"`
	ToolOutput         *string                `json:"toolOutput,omitempty"`
	IsError            *bool                  `json:"isError,omitempty"`
	IsRateLimit        *bool                  `json:"isRateLimit,omitempty"`
	RateLimitResetAt   *int64                 `json:"rateLimitResetAt,omitempty"`
	RetryAfterSeconds  *int64                 `json:"retryAfterSeconds,omitempty"`
	ExitCode           *int                   `json:"exitCode,omitempty"`
	Question           *string                `json:"question,omitempty"`
	StopReason         *string                `json:"stopReason,omitempty"`
	Usage              *TokenUsage            `json:"usage,omitempty"`
	RawHarnessCost     *float64               `json:"rawHarnessCost,omitempty"`
}

// ParsedEvent represents a single normalized event emitted by a harness parser.
type ParsedEvent struct {
	EventType string    `json:"eventType"`
	Timestamp int64     `json:"timestamp"`
	CWD       string    `json:"cwd"`
	GitBranch string    `json:"gitBranch"`
	Data      EventData `json:"data"`
}

// Event is an alias for ParsedEvent.
type Event = ParsedEvent
