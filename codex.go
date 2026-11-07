// Package parser provides streaming log parsers for AI agent harnesses.
//
// Attribution:
// Derived from and inspired by OpenAI Codex CLI specifications (docs/harness-logs/codex.ts),
// AgentHUD (https://github.com/neochoon/agenthud),
// Codex Transcripts (https://github.com/prateek/codex-transcripts),
// Claude Code Session Viewer (https://github.com/eric050828/claude-code-session-viewer), and
// Sessions (https://github.com/nicknisi/sessions).
package parser

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

var (
	codexPartitionedPathRegex = regexp.MustCompile(`(?:^|/)sessions/(\d{4})/(\d{2})/(\d{2})/rollout-(\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}-\d{2})?)-([0-9a-zA-Z_-]+)\.jsonl$`)
	codexFlatPathRegex        = regexp.MustCompile(`(?:^|/)rollout-(\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}-\d{2})?)-([0-9a-zA-Z_-]+)\.jsonl$`)

	codexPartitionedFallbackRegex = regexp.MustCompile(`(?:^|/)sessions/(\d{4})/(\d{2})/(\d{2})/rollout-(.+?)-([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\.jsonl$`)
	codexFlatFallbackRegex        = regexp.MustCompile(`(?:^|/)rollout-(.+?)-([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\.jsonl$`)
)

// CodexRolloutFilenameInfo contains parsed components from a Codex rollout file path.
type CodexRolloutFilenameInfo struct {
	FilePath        string `json:"filePath"`
	Year            string `json:"year,omitempty"`
	Month           string `json:"month,omitempty"`
	Day             string `json:"day,omitempty"`
	TimestampString string `json:"timestampString"`
	SessionID       string `json:"sessionId"`
}

// ParseCodexRolloutPath parses a Codex rollout file path into constituent components.
func ParseCodexRolloutPath(filePath string) (*CodexRolloutFilenameInfo, error) {
	if filePath == "" {
		return nil, fmt.Errorf("empty file path")
	}

	normalized := strings.ReplaceAll(filePath, "\\", "/")
	if match := codexPartitionedPathRegex.FindStringSubmatch(normalized); len(match) == 6 {
		return &CodexRolloutFilenameInfo{
			FilePath:        filePath,
			Year:            match[1],
			Month:           match[2],
			Day:             match[3],
			TimestampString: match[4],
			SessionID:       match[5],
		}, nil
	}

	if match := codexPartitionedFallbackRegex.FindStringSubmatch(normalized); len(match) == 6 {
		return &CodexRolloutFilenameInfo{
			FilePath:        filePath,
			Year:            match[1],
			Month:           match[2],
			Day:             match[3],
			TimestampString: match[4],
			SessionID:       match[5],
		}, nil
	}

	if match := codexFlatPathRegex.FindStringSubmatch(normalized); len(match) == 3 {
		return &CodexRolloutFilenameInfo{
			FilePath:        filePath,
			TimestampString: match[1],
			SessionID:       match[2],
		}, nil
	}

	if match := codexFlatFallbackRegex.FindStringSubmatch(normalized); len(match) == 3 {
		return &CodexRolloutFilenameInfo{
			FilePath:        filePath,
			TimestampString: match[1],
			SessionID:       match[2],
		}, nil
	}

	return nil, fmt.Errorf("invalid codex rollout path: %q", filePath)
}

// CleanCodexPrompt filters synthetic <environment_context> and <session_context> blocks from user prompts.
func CleanCodexPrompt(promptText string) string {
	trimmed := strings.TrimSpace(promptText)

	for {
		changed := false
		if strings.HasPrefix(trimmed, "<environment_context>") {
			if idx := strings.Index(trimmed, "</environment_context>"); idx != -1 {
				trimmed = strings.TrimSpace(trimmed[idx+len("</environment_context>"):])
				changed = true
			} else {
				return ""
			}
		}
		if strings.HasPrefix(trimmed, "<session_context>") {
			if idx := strings.Index(trimmed, "</session_context>"); idx != -1 {
				trimmed = strings.TrimSpace(trimmed[idx+len("</session_context>"):])
				changed = true
			} else {
				return ""
			}
		}
		if !changed {
			break
		}
	}

	return strings.TrimSpace(trimmed)
}

// ExtractCodexTitleFromPrompt extracts a session title from prompt text, ignoring synthetic context blocks.
func ExtractCodexTitleFromPrompt(promptText string) (string, bool) {
	cleanPrompt := CleanCodexPrompt(promptText)
	if cleanPrompt == "" {
		return "", false
	}
	firstLine := strings.Split(cleanPrompt, "\n")[0]
	firstLine = strings.TrimSpace(firstLine)
	if len(firstLine) > 100 {
		firstLine = firstLine[:100]
	}
	return firstLine, true
}

type rawCodexGitMeta struct {
	CommitHash    string `json:"commit_hash"`
	Branch        string `json:"branch"`
	RepositoryURL string `json:"repository_url"`
}

type rawCodexThreadSpawn struct {
	ParentThreadID string  `json:"parent_thread_id"`
	Depth          int     `json:"depth"`
	AgentPath      *string `json:"agent_path"`
	AgentNickname  string  `json:"agent_nickname"`
	AgentRole      string  `json:"agent_role"`
}

type rawCodexSessionMetaPayload struct {
	ID               string      `json:"id"`
	SessionID        string      `json:"session_id,omitempty"`
	Timestamp        string      `json:"timestamp"`
	CWD              string      `json:"cwd"`
	Originator       string      `json:"originator"`
	CLIVersion       string      `json:"cli_version"`
	Source           interface{} `json:"source"`
	Model            string      `json:"model,omitempty"`
	ModelProvider    string      `json:"model_provider"`
	BaseInstructions *struct {
		Text string `json:"text"`
	} `json:"base_instructions"`
	Git               *rawCodexGitMeta `json:"git"`
	ParentThreadID    string           `json:"parent_thread_id"`
	AgentNickname     string           `json:"agent_nickname"`
	AgentRole         string           `json:"agent_role"`
	MultiAgentVersion string           `json:"multi_agent_version"`
	// SubagentHistoryStartOrdinal is the first ordinal of a forked subagent's
	// own history; earlier records are context inherited from the parent.
	SubagentHistoryStartOrdinal *uint64 `json:"subagent_history_start_ordinal,omitempty"`
	// HistoryMode is "legacy" (Codex's default when absent) or "paginated".
	HistoryMode string `json:"history_mode,omitempty"`
}

type rawCodexTurnContextPayload struct {
	TurnID            string                 `json:"turn_id"`
	CWD               string                 `json:"cwd"`
	CurrentDate       string                 `json:"current_date"`
	Timezone          string                 `json:"timezone"`
	Model             string                 `json:"model"`
	Personality       string                 `json:"personality"`
	ApprovalPolicy    string                 `json:"approval_policy"`
	SandboxPolicy     map[string]interface{} `json:"sandbox_policy"`
	CollaborationMode map[string]interface{} `json:"collaboration_mode"`
	Summary           string                 `json:"summary"`
	RealtimeActive    bool                   `json:"realtime_active"`
}

// rawCodexTokenBreakdown is Codex's TokenUsage. Cached and cache-write input
// tokens are parts of InputTokens, and reasoning tokens are part of
// OutputTokens.
type rawCodexTokenBreakdown struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	CacheWriteInputTokens int64 `json:"cache_write_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
	TotalTokens           int64 `json:"total_tokens"`
}

type rawCodexTokenCountInfo struct {
	TotalTokenUsage    rawCodexTokenBreakdown `json:"total_token_usage"`
	LastTokenUsage     rawCodexTokenBreakdown `json:"last_token_usage"`
	ModelContextWindow int64                  `json:"model_context_window"`
}

type rawCodexEventMsgPayload struct {
	Item        json.RawMessage `json:"item,omitempty"`
	Type        string          `json:"type"`
	Message     string          `json:"message"`
	TurnID      string          `json:"turn_id"`
	StartedAt   json.RawMessage `json:"started_at"`
	CompletedAt json.RawMessage `json:"completed_at"`
	DurationMS  int64           `json:"duration_ms"`
	// StartedAtMs bounds the start of an item_completed item.
	StartedAtMs      *int64      `json:"started_at_ms,omitempty"`
	LastAgentMessage string      `json:"last_agent_message"`
	CodexErrorInfo   interface{} `json:"codex_error_info,omitempty"`
	// Reason is why a turn_aborted turn stopped (TurnAbortReason).
	Reason string `json:"reason,omitempty"`
	// Error describes why a task_complete or turn_aborted turn failed.
	Error              *rawCodexErrorEvent     `json:"error,omitempty"`
	Info               *rawCodexTokenCountInfo `json:"info"`
	RateLimits         *rawCodexRateLimits     `json:"rate_limits,omitempty"`
	ModelContextWindow int64                   `json:"model_context_window,omitempty"`
	Summary            []rawCodexSummaryPart   `json:"summary"`
	EncryptedContent   string                  `json:"encrypted_content"`
}

// rawCodexErrorEvent is Codex's ErrorEvent as stored on turn completions.
type rawCodexErrorEvent struct {
	Message        string      `json:"message"`
	CodexErrorInfo interface{} `json:"codex_error_info,omitempty"`
}

// rawCodexSummaryPart is one part of a reasoning summary.
type rawCodexSummaryPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type rawCodexAgentMessage struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Questions []map[string]interface{} `json:"questions"`
}

func parseCodexCompletedItem(raw json.RawMessage, ts int64, startedAt *int64, p *CodexParser) ([]ParsedEvent, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var header struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return nil, fmt.Errorf("unmarshaling completed item: %w", err)
	}
	switch header.Type {
	case "CommandExecution":
		skills, err := parseCodexCommandExecution(raw, ts)
		if err != nil {
			return nil, err
		}
		calls, err := parseCodexToolItem(header.Type, raw, ts, startedAt, p)
		return append(calls, skills...), err
	case "McpToolCall", "WebSearch", "ImageView", "Extension", "DynamicToolCall":
		return parseCodexToolItem(header.Type, raw, ts, startedAt, p)
	case "UserMessage":
		return parseCodexUserMessage(raw, ts, p)
	case "FileChange":
		return parseCodexFileChange(raw, ts)
	}
	if header.Type != "AgentMessage" {
		return nil, nil
	}
	return parseCodexAgentMessage(raw, ts, p)
}

// parseCodexAgentMessage reports a completed AgentMessage item. Paginated
// history writes one for every assistant message, alongside the assistant
// response_item with the same text, and is the source there. An item with
// questions is Codex's projection of a question tool call, whose
// function_call already reports the question with its arguments and call ID.
func parseCodexAgentMessage(raw json.RawMessage, ts int64, p *CodexParser) ([]ParsedEvent, error) {
	var item rawCodexAgentMessage
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, fmt.Errorf("unmarshaling completed agent message: %w", err)
	}
	if len(item.Questions) > 0 || !p.isPaginated() {
		return nil, nil
	}
	var content strings.Builder
	for _, block := range item.Content {
		if block.Type == "Text" {
			content.WriteString(block.Text)
		}
	}
	return codexAgentMessageEvents(content.String(), ts), nil
}

func codexQuestionText(input map[string]interface{}) string {
	for _, key := range []string{"question", "prompt", "title"} {
		if text, ok := input[key].(string); ok && strings.TrimSpace(text) != "" {
			return text
		}
	}
	var parts []string
	if questions, ok := input["questions"].([]interface{}); ok {
		for _, question := range questions {
			if q, ok := question.(map[string]interface{}); ok {
				if text := codexQuestionText(q); text != "" {
					parts = append(parts, text)
				}
			}
		}
	}
	return strings.Join(parts, "\n")
}

type rawCodexResponseItemPayload struct {
	Type             string                `json:"type"`
	Role             string                `json:"role"`
	Name             string                `json:"name"`
	CallID           string                `json:"call_id"`
	Arguments        string                `json:"arguments"`
	Input            interface{}           `json:"input,omitempty"`
	Output           rawCodexToolOutput    `json:"output"`
	Query            string                `json:"query"`
	Content          interface{}           `json:"content"`
	Summary          []rawCodexSummaryPart `json:"summary"`
	EncryptedContent string                `json:"encrypted_content"`
}

type rawCodexLine struct {
	Type      string      `json:"type"`
	Timestamp interface{} `json:"timestamp"`
	// Ordinal is the record's position in a paginated-history rollout.
	Ordinal *uint64         `json:"ordinal,omitempty"`
	Payload json.RawMessage `json:"payload"`
}

// CodexParser maintains session stream state when parsing Codex rollout transcripts.
type CodexParser struct {
	mu                sync.Mutex
	currentModel      string
	contextWindow     int64
	rateLimitResetAt  *int64
	retryAfterSeconds *int64
	// lastTotalUsage is the cumulative usage of the previous token_count
	// record, used to recognize records Codex re-sends without new usage.
	lastTotalUsage *rawCodexTokenBreakdown
	// sessionMetaSeen is set once the rollout's own session_meta, its first
	// record, has been parsed; historyStartOrdinal is that record's
	// subagent_history_start_ordinal.
	sessionMetaSeen     bool
	historyStartOrdinal *uint64
	// turnOpen is set between task_started and the turn's completion or abort.
	// pendingTurnStart holds the turn's turn_start until its first prompt or
	// other event arrives, so the prompt can be reported on it.
	turnOpen         bool
	pendingTurnStart *ParsedEvent
	// callIDs holds the IDs of tool calls recorded as response items, whose
	// completed items repeat them.
	callIDs map[string]struct{}
	// paginated is set when the rollout's own session_meta declares paginated
	// history, whose completed UserMessage items are the prompt source.
	// userImages holds the images of the user response_item on line
	// userImagesLine, which the following UserMessage item describes.
	paginated      bool
	line           int
	userImages     []Image
	userImagesLine int
}

// NewCodexParser creates a new stateful parser stream for Codex transcripts.
func NewCodexParser() *CodexParser {
	return &CodexParser{}
}

// Reset resets the parser stream state.
func (p *CodexParser) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.currentModel = ""
	p.contextWindow = 0
	p.rateLimitResetAt = nil
	p.retryAfterSeconds = nil
	p.lastTotalUsage = nil
	p.sessionMetaSeen = false
	p.historyStartOrdinal = nil
	p.turnOpen = false
	p.pendingTurnStart = nil
	p.callIDs = nil
	p.paginated = false
	p.line = 0
	p.userImages = nil
	p.userImagesLine = 0
}

// ParseLine parses a single line using this CodexParser instance.
func (p *CodexParser) ParseLine(line []byte) ([]ParsedEvent, error) {
	return parseCodexLineWithState(line, p)
}

// ParseCodexLine parses a single line of Codex rollout JSONL log into ParsedEvent structs without stream state.
func ParseCodexLine(line []byte) ([]ParsedEvent, error) {
	return parseCodexLineWithState(line, nil)
}

func parseCodexLineWithState(line []byte, p *CodexParser) ([]ParsedEvent, error) {
	trimmed := strings.TrimSpace(string(line))
	if len(trimmed) == 0 {
		return nil, nil
	}

	var raw rawCodexLine
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return nil, fmt.Errorf("unmarshaling codex line: %w", err)
	}

	if p.isInheritedHistory(raw.Ordinal) {
		return nil, nil
	}

	ts := parseTimestamp(raw.Timestamp)

	p.advanceLine()
	pending := p.pendingTurn()

	var events []ParsedEvent
	var err error
	switch raw.Type {
	case "session_meta":
		events, err = parseCodexSessionMeta(raw.Payload, ts, p)
	case "turn_context":
		events, err = parseCodexTurnContext(raw.Payload, ts, p)
	case "event_msg":
		events, err = parseCodexEventMsg(raw.Payload, ts, p)
	case "response_item":
		events, err = parseCodexResponseItem(raw.Payload, ts, p)
	}
	if err != nil {
		return nil, err
	}
	// The first activity of a turn reports the turn's pending start ahead of
	// it. A model change from turn_context, which Codex writes before the
	// turn's prompt, is not activity.
	if codexIsTurnActivity(events) {
		if start := p.takePendingTurnStart(pending); start != nil {
			events = append([]ParsedEvent{*start}, events...)
		}
	}
	return events, nil
}

func codexIsTurnActivity(events []ParsedEvent) bool {
	for _, ev := range events {
		if ev.EventType != "model_change" {
			return true
		}
	}
	return false
}

func extractInt(v interface{}) *int {
	switch val := v.(type) {
	case float64:
		i := int(val)
		return &i
	case int:
		return &val
	case int64:
		i := int(val)
		return &i
	}
	return nil
}

type rawCodexCommandExecution struct {
	Command  []string `json:"command"`
	ExitCode *int     `json:"exit_code"`
	Source   string   `json:"source"`
}

type rawCodexUserMessage struct {
	Content []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL string `json:"image_url"`
		FileID   string `json:"file_id"`
		Name     string `json:"name"`
		Path     string `json:"path"`
	} `json:"content"`
}

// parseCodexUserMessage reports a completed UserMessage item. Codex builds it
// only for real user input, leaving out the context it injects as user-role
// messages, so in paginated history it is the prompt source. Its local images
// carry only a path; the user response_item recorded just before it holds
// their data.
func parseCodexUserMessage(raw json.RawMessage, ts int64, p *CodexParser) ([]ParsedEvent, error) {
	skills, err := parseCodexUserMessageSkills(raw, ts)
	if err != nil || !p.isPaginated() {
		return skills, err
	}
	var item rawCodexUserMessage
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, fmt.Errorf("unmarshaling completed user message: %w", err)
	}
	var text strings.Builder
	var images []Image
	for _, content := range item.Content {
		switch content.Type {
		case "text":
			text.WriteString(content.Text)
		case "image":
			// ImageReference is either an inline image_url or an uploaded file_id.
			if content.ImageURL != "" {
				images = append(images, imageFromURL(content.ImageURL))
			} else if content.FileID != "" {
				images = append(images, Image{FileID: content.FileID})
			}
		}
	}
	if recorded, ok := p.takeAdjacentUserImages(); ok {
		images = recorded
	}
	return append(p.promptEvents(codexUserPromptEvents(text.String(), images, ts)), skills...), nil
}

// parseCodexUserMessageSkills emits a skill_invoked event for the skills a user mentions
// with $name. The completed UserMessage lists them as skill content items. Forked child
// rollouts replay the parent's response_item messages, including the injected <skill>
// message, but not item_completed records, so counting here counts each mention once.
func parseCodexUserMessageSkills(raw json.RawMessage, ts int64) ([]ParsedEvent, error) {
	var item rawCodexUserMessage
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, fmt.Errorf("unmarshaling completed user message: %w", err)
	}
	var skills []SkillInvocation
	for _, content := range item.Content {
		if content.Type == "skill" && strings.TrimSpace(content.Name) != "" {
			skills = append(skills, SkillInvocation{Name: strings.TrimSpace(content.Name), Source: SkillSourceUser, Path: content.Path})
		}
	}
	if len(skills) == 0 {
		return nil, nil
	}
	return []ParsedEvent{{
		EventType: "skill_invoked",
		Timestamp: ts,
		Data:      EventData{Role: stringPtr("user"), Skills: skills},
	}}, nil
}

// parseCodexCommandExecution emits a skill_invoked event when a completed shell command
// read one or more skills' SKILL.md files. Codex has no skill tool: the model loads a
// skill by reading its file, directly or from code mode, and every executed command is
// recorded as a CommandExecution item.
func parseCodexCommandExecution(raw json.RawMessage, ts int64) ([]ParsedEvent, error) {
	var item rawCodexCommandExecution
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, fmt.Errorf("unmarshaling completed command execution: %w", err)
	}
	// user_shell marks a command the user ran with !, which is not a model loading a skill.
	if item.ExitCode == nil || *item.ExitCode != 0 || len(item.Command) == 0 || item.Source == "user_shell" {
		return nil, nil
	}
	script := strings.Join(item.Command, " ")
	if len(item.Command) >= 3 && (item.Command[1] == "-lc" || item.Command[1] == "-c") {
		script = item.Command[2]
	}
	skills := skillsFromShellCommand(script)
	if len(skills) == 0 {
		return nil, nil
	}
	return []ParsedEvent{{
		EventType: "skill_invoked",
		Timestamp: ts,
		Data:      EventData{Role: stringPtr("assistant"), Skills: skills},
	}}, nil
}
