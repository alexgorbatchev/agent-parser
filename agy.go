package parser

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type rawAgyToolCall struct {
	ID   string                 `json:"id,omitempty"`
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args,omitempty"`
}

type rawAgyTranscriptLine struct {
	StepIndex       int              `json:"step_index"`
	Type            string           `json:"type"`
	Source          string           `json:"source,omitempty"`
	Status          string           `json:"status,omitempty"`
	CreatedAt       interface{}      `json:"created_at,omitempty"`
	Content         string           `json:"content,omitempty"`
	Thinking        string           `json:"thinking,omitempty"`
	Error           string           `json:"error,omitempty"`
	Model           string           `json:"model,omitempty"`
	InputTokens     int64            `json:"input_tokens,omitempty"`
	OutputTokens    int64            `json:"output_tokens,omitempty"`
	CacheReadTokens int64            `json:"cache_read_tokens,omitempty"`
	ThinkingTokens  int64            `json:"thinking_tokens,omitempty"`
	ToolCalls       []rawAgyToolCall `json:"tool_calls,omitempty"`
	TruncatedFields []string         `json:"truncated_fields,omitempty"`
}

type agyPendingTool struct {
	id   string
	name string
}

// AgyParser is a stateful JSONL transcript parser for Google Antigravity (`agy`)
// transcripts (`transcript_full.jsonl` and `transcript.jsonl`). It correlates
// `GENERIC` tool result records with the preceding `PLANNER_RESPONSE` tool calls
// and tracks turn boundaries.
type AgyParser struct {
	mu           sync.Mutex
	pendingTools []agyPendingTool
	inTurn       bool
	lastModel    string
}

// NewAgyParser creates a stateful Antigravity transcript parser.
func NewAgyParser() *AgyParser {
	return &AgyParser{}
}

// Reset clears turn and pending-tool correlation state.
func (p *AgyParser) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pendingTools = nil
	p.inTurn = false
	p.lastModel = ""
}

// ParseLine parses one Antigravity transcript JSONL record into normalized events.
func (p *AgyParser) ParseLine(line []byte) ([]ParsedEvent, error) {
	if len(bytes.TrimSpace(line)) == 0 {
		return nil, nil
	}
	var raw rawAgyTranscriptLine
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, fmt.Errorf("decoding agy transcript line: %w", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.parseRecord(raw), nil
}

// ParseAgyLine parses a single Antigravity transcript JSONL record without
// cross-line state.
func ParseAgyLine(line []byte) ([]ParsedEvent, error) {
	return NewAgyParser().ParseLine(line)
}

func (p *AgyParser) parseRecord(raw rawAgyTranscriptLine) []ParsedEvent {
	ts := parseTimestamp(raw.CreatedAt)
	switch raw.Type {
	case "USER_INPUT":
		p.pendingTools = nil
		p.inTurn = true
		role := "user"
		content := raw.Content
		return []ParsedEvent{{
			EventType: "turn_start",
			Timestamp: ts,
			Data: EventData{
				Role:       &role,
				Content:    &content,
				LastPrompt: &content,
			},
		}}
	case "PLANNER_RESPONSE":
		return p.parsePlannerResponse(raw, ts)
	case "GENERIC":
		return p.parseGenericStep(raw, ts)
	case "SYSTEM_MESSAGE":
		if raw.Error != "" {
			errText := raw.Error
			return []ParsedEvent{{
				EventType: "error",
				Timestamp: ts,
				Data: EventData{
					Content: &errText,
					IsError: boolPtr(true),
				},
			}}
		}
		return nil
	default:
		return nil
	}
}

func (p *AgyParser) parsePlannerResponse(raw rawAgyTranscriptLine, ts int64) []ParsedEvent {
	if raw.Model != "" {
		p.lastModel = raw.Model
	}
	modelPtr := optionalString(raw.Model)
	if modelPtr == nil && p.lastModel != "" {
		m := p.lastModel
		modelPtr = &m
	}
	role := "assistant"
	var events []ParsedEvent

	if raw.Thinking != "" {
		thinking := raw.Thinking
		events = append(events, ParsedEvent{
			EventType: "thinking",
			Timestamp: ts,
			Data: EventData{
				Role:    &role,
				Content: &thinking,
				Model:   modelPtr,
			},
		})
	}

	if raw.Content != "" {
		content := raw.Content
		events = append(events, ParsedEvent{
			EventType: "agent_message",
			Timestamp: ts,
			Data: EventData{
				Role:    &role,
				Content: &content,
				Model:   modelPtr,
			},
		})
	}

	for i, tc := range raw.ToolCalls {
		callID := tc.ID
		if callID == "" {
			callID = "step-" + strconv.Itoa(raw.StepIndex) + "-tool-" + strconv.Itoa(i)
		}
		args := decodeAgyToolArgs(tc.Args)
		name := tc.Name
		data := EventData{
			Role:       &role,
			Model:      modelPtr,
			ToolCallID: &callID,
			ToolName:   &name,
			ToolInput:  args,
		}
		if fm := NormalizeFileModification(name, args); fm != nil {
			data.FileModification = fm
		}
		if skills := agyToolSkills(name, args); len(skills) > 0 {
			data.Skills = skills
		}
		events = append(events, ParsedEvent{
			EventType: "tool_call",
			Timestamp: ts,
			Data:      data,
		})
		p.pendingTools = append(p.pendingTools, agyPendingTool{id: callID, name: name})
	}

	if raw.InputTokens > 0 || raw.OutputTokens > 0 || raw.CacheReadTokens > 0 {
		usage := &TokenUsage{
			Input:     raw.InputTokens,
			Output:    raw.OutputTokens,
			CacheRead: raw.CacheReadTokens,
			Reasoning: raw.ThinkingTokens,
			Total:     raw.InputTokens + raw.OutputTokens + raw.CacheReadTokens,
		}
		events = append(events, ParsedEvent{
			EventType: "usage",
			Timestamp: ts,
			Data: EventData{
				Model: modelPtr,
				Usage: usage,
			},
		})
	}

	if raw.Error != "" {
		errText := raw.Error
		events = append(events, ParsedEvent{
			EventType: "error",
			Timestamp: ts,
			Data: EventData{
				Content: &errText,
				Model:   modelPtr,
				IsError: boolPtr(true),
			},
		})
	}

	if len(raw.ToolCalls) == 0 && p.inTurn && (raw.Content != "" || raw.Status == "DONE" || raw.Status == "ERROR") {
		p.inTurn = false
		events = append(events, ParsedEvent{
			EventType: "turn_end",
			Timestamp: ts,
			Data: EventData{
				Model: modelPtr,
			},
		})
	}

	return events
}

func (p *AgyParser) parseGenericStep(raw rawAgyTranscriptLine, ts int64) []ParsedEvent {
	var callID, toolName string
	if len(p.pendingTools) > 0 {
		next := p.pendingTools[0]
		p.pendingTools = p.pendingTools[1:]
		callID = next.id
		toolName = next.name
	} else {
		callID = "step-" + strconv.Itoa(raw.StepIndex)
	}
	isErr := raw.Status == "ERROR" || raw.Error != ""
	output := raw.Content
	if output == "" && raw.Error != "" {
		output = raw.Error
	}
	data := EventData{
		ToolCallID: &callID,
		ToolName:   optionalString(toolName),
		ToolOutput: &output,
		IsError:    &isErr,
	}
	return []ParsedEvent{{
		EventType: "tool_result",
		Timestamp: ts,
		Data:      data,
	}}
}

// decodeAgyToolArgs unwraps JSON-encoded string values recorded by older Antigravity
// runtimes while preserving plain string arguments verbatim.
func decodeAgyToolArgs(args map[string]interface{}) map[string]interface{} {
	if len(args) == 0 {
		return nil
	}
	out := make(map[string]interface{}, len(args))
	for k, v := range args {
		if s, ok := v.(string); ok {
			trimmed := strings.TrimSpace(s)
			if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[' || trimmed[0] == '"') {
				var decoded interface{}
				if err := json.Unmarshal([]byte(trimmed), &decoded); err == nil {
					out[k] = decoded
					continue
				}
			}
		}
		out[k] = v
	}
	return out
}

func agyToolSkills(toolName string, args map[string]interface{}) []SkillInvocation {
	if len(args) == 0 {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "read_file", "view_file", "client_view_file", "read":
		return skillsFromReadTool(args)
	case "run_command", "bash", "shell":
		for _, key := range []string{"CommandLine", "command_line", "commandLine", "command"} {
			if cmd := stringInput(args, key); cmd != "" {
				return skillsFromShellCommand(cmd)
			}
		}
	}
	return nil
}

// AgyConversationSummary holds read-only session metadata read from Antigravity's
// `conversation_summaries.db` index or per-conversation `conversations/<id>.db`.
type AgyConversationSummary struct {
	ConversationID       string
	Title                string
	Status               string
	ParentConversationID string
	WorkspacePaths       []string
	LastModified         time.Time
	NotFullyIdle         bool
	Killed               bool
}

// ReadAgySummaries reads conversation metadata from an Antigravity
// `conversation_summaries.db` SQLite database in read-only mode.
func ReadAgySummaries(ctx context.Context, dbPath string) (map[string]AgyConversationSummary, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("stating agy summaries db: %w", err)
	}
	uri := "file:" + filepath.ToSlash(dbPath) + "?mode=ro&_pragma=busy_timeout(100)"
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, fmt.Errorf("opening agy summaries db: %w", err)
	}
	defer func() { _ = db.Close() }()

	result := make(map[string]AgyConversationSummary)
	rows, err := db.QueryContext(ctx, `
		SELECT conversation_id, title, workspace_uris, status, parent_conversation_id,
		       last_modified_time, not_fully_idle, killed
		FROM conversation_summaries
	`)
	if err != nil {
		return nil, fmt.Errorf("querying agy conversation_summaries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			s             AgyConversationSummary
			workspaceURIs string
			lastModRaw    string
			notIdle       interface{}
			killed        interface{}
		)
		if err := rows.Scan(
			&s.ConversationID, &s.Title, &workspaceURIs, &s.Status, &s.ParentConversationID,
			&lastModRaw, &notIdle, &killed,
		); err != nil {
			return nil, fmt.Errorf("scanning agy conversation summary: %w", err)
		}
		s.WorkspacePaths = parseAgyWorkspaceURIs(workspaceURIs)
		s.LastModified = parseAgySQLiteTime(lastModRaw)
		s.NotFullyIdle = sqliteBool(notIdle)
		s.Killed = sqliteBool(killed)
		if s.ConversationID != "" {
			result[s.ConversationID] = s
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating agy conversation summaries: %w", err)
	}

	parentRows, err := db.QueryContext(ctx, `SELECT conversation_id, parent_id FROM conversation_parents`)
	if err == nil {
		defer func() { _ = parentRows.Close() }()
		for parentRows.Next() {
			var childID, parentID string
			if scanErr := parentRows.Scan(&childID, &parentID); scanErr == nil && childID != "" && parentID != "" {
				entry := result[childID]
				entry.ConversationID = childID
				if entry.ParentConversationID == "" {
					entry.ParentConversationID = parentID
				}
				result[childID] = entry
			}
		}
	}

	return result, nil
}

// ReadAgyConversationWorkspaces reads workspace paths for one conversation from its
// read-only SQLite database (`conversations/<id>.db`, `trajectory_metadata_blob`)
// or legacy `<id>.meta` JSON sidecar, matching pingdotgg/t3code's AntigravitySessionFiles.ts.
func ReadAgyConversationWorkspaces(ctx context.Context, conversationsDir, conversationID string) ([]string, error) {
	metaPath := filepath.Join(conversationsDir, conversationID+".meta")
	if data, err := os.ReadFile(metaPath); err == nil {
		var meta struct {
			CWD string `json:"cwd"`
		}
		if json.Unmarshal(data, &meta) == nil && strings.TrimSpace(meta.CWD) != "" {
			return []string{strings.TrimSpace(meta.CWD)}, nil
		}
	}

	dbPath := filepath.Join(conversationsDir, conversationID+".db")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, err
	}
	uri := "file:" + filepath.ToSlash(dbPath) + "?mode=ro&_pragma=busy_timeout(100)"
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, fmt.Errorf("opening agy conversation db: %w", err)
	}
	defer func() { _ = db.Close() }()

	var blob []byte
	err = db.QueryRowContext(ctx, `SELECT data FROM trajectory_metadata_blob WHERE length(data) <= 1048576 LIMIT 1`).Scan(&blob)
	if err != nil {
		return nil, fmt.Errorf("reading agy trajectory_metadata_blob: %w", err)
	}
	return decodeAgyTrajectoryWorkspacePaths(blob), nil
}

// decodeAgyTrajectoryWorkspacePaths extracts file:// workspace paths from the
// TrajectoryMetadata protobuf blob (field 1 -> nested field 1, and field 7).
func decodeAgyTrajectoryWorkspacePaths(blob []byte) []string {
	var uris []string
	for _, field := range readProtoLengthFields(blob) {
		switch field.number {
		case 1:
			for _, nested := range readProtoLengthFields(field.value) {
				if nested.number == 1 && utf8.Valid(nested.value) {
					if u := strings.TrimSpace(string(nested.value)); u != "" {
						uris = append(uris, u)
					}
				}
			}
		case 7:
			if utf8.Valid(field.value) {
				if u := strings.TrimSpace(string(field.value)); u != "" {
					uris = append(uris, u)
				}
			}
		}
	}
	return fileURIsToPaths(uris)
}

type protoLengthField struct {
	number uint64
	value  []byte
}

func readProtoLengthFields(data []byte) []protoLengthField {
	var fields []protoLengthField
	offset := 0
	for offset < len(data) {
		tag, n := readProtoVarint(data[offset:])
		if n == 0 {
			return fields
		}
		offset += n
		fieldNum := tag >> 3
		wire := tag & 0x7
		switch wire {
		case 0:
			_, vn := readProtoVarint(data[offset:])
			if vn == 0 {
				return fields
			}
			offset += vn
		case 1:
			if offset+8 > len(data) {
				return fields
			}
			offset += 8
		case 5:
			if offset+4 > len(data) {
				return fields
			}
			offset += 4
		case 2:
			length, ln := readProtoVarint(data[offset:])
			if ln == 0 {
				return fields
			}
			offset += ln
			if length > uint64(len(data)-offset) {
				return fields
			}
			end := offset + int(length)
			fields = append(fields, protoLengthField{number: fieldNum, value: data[offset:end]})
			offset = end
		default:
			return fields
		}
	}
	return fields
}

func readProtoVarint(data []byte) (uint64, int) {
	var val uint64
	for i := 0; i < len(data) && i < 10; i++ {
		b := data[i]
		val |= uint64(b&0x7f) << (7 * i)
		if b < 0x80 {
			return val, i + 1
		}
	}
	return 0, 0
}

func parseAgyWorkspaceURIs(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var list []string
		if json.Unmarshal([]byte(trimmed), &list) == nil {
			return fileURIsToPaths(list)
		}
	}
	parts := strings.Split(trimmed, ",")
	return fileURIsToPaths(parts)
}

func fileURIsToPaths(uris []string) []string {
	var paths []string
	seen := make(map[string]bool)
	for _, raw := range uris {
		u := strings.TrimSpace(raw)
		if u == "" {
			continue
		}
		p := u
		if strings.HasPrefix(u, "file://") {
			parsed, err := url.Parse(u)
			if err != nil {
				continue
			}
			p = parsed.Path
		}
		if p != "" && !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	return paths
}

func parseAgySQLiteTime(raw string) time.Time {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func sqliteBool(v interface{}) bool {
	switch val := v.(type) {
	case bool:
		return val
	case int64:
		return val != 0
	case int:
		return val != 0
	case float64:
		return val != 0
	case string:
		s := strings.ToLower(strings.TrimSpace(val))
		return s == "1" || s == "true"
	default:
		return false
	}
}

func discoverAgy(ctx context.Context, home string) ([]SessionSource, error) {
	var roots []string
	if custom := os.Getenv("AGY_SESSIONS_DIR"); custom != "" {
		roots = []string{custom}
	} else {
		if geminiHome := os.Getenv("GEMINI_HOME"); geminiHome != "" {
			roots = append(roots, filepath.Join(geminiHome, "antigravity-cli", "brain"))
		}
		roots = append(roots, filepath.Join(home, ".gemini", "antigravity-cli", "brain"))
	}
	roots = uniqueDirs(roots)

	var sources []SessionSource
	var errs []error
	for _, brainRoot := range roots {
		if err := ctx.Err(); err != nil {
			return sources, err
		}
		found, err := discoverAgyBrainRoot(ctx, brainRoot)
		sources = append(sources, found...)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return sources, errors.Join(errs...)
}

func discoverAgyBrainRoot(ctx context.Context, brainRoot string) ([]SessionSource, error) {
	resolved, err := filepath.EvalSymlinks(brainRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return nil, err
	}

	// Build parent lookup from detached subagents/ directories and sibling conversation_summaries.db.
	parents := make(map[string]string)
	summariesDB := filepath.Join(filepath.Dir(resolved), "conversation_summaries.db")
	if summaries, sumErr := ReadAgySummaries(ctx, summariesDB); sumErr == nil {
		for id, summary := range summaries {
			if summary.ParentConversationID != "" {
				parents[id] = summary.ParentConversationID
			}
		}
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		parentID := entry.Name()
		subDir := filepath.Join(resolved, parentID, ".system_generated", "subagents")
		subEntries, subErr := os.ReadDir(subDir)
		if subErr != nil {
			continue
		}
		for _, sub := range subEntries {
			if sub.IsDir() || !strings.HasSuffix(sub.Name(), ".json") {
				continue
			}
			childID := strings.TrimSuffix(sub.Name(), ".json")
			if childID != "" {
				parents[childID] = parentID
			}
		}
	}

	var sources []SessionSource
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return sources, err
		}
		if !entry.IsDir() {
			continue
		}
		convID := entry.Name()
		logsDir := filepath.Join(brainRoot, convID, ".system_generated", "logs")
		transcriptPath, info := selectAgyTranscriptFile(logsDir)
		if transcriptPath == "" {
			continue
		}
		source := SessionSource{
			Harness:   HarnessAgy,
			SessionID: convID,
			Path:      transcriptPath,
			ModTime:   info.ModTime(),
		}
		if parentID := parents[convID]; parentID != "" {
			source.ParentSessionID = parentID
			source.Subagent = true
		}
		sources = append(sources, source)
	}
	return sources, nil
}

func selectAgyTranscriptFile(logsDir string) (string, fs.FileInfo) {
	for _, name := range []string{"transcript_full.jsonl", "transcript.jsonl"} {
		candidate := filepath.Join(logsDir, name)
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, info
		}
	}
	return "", nil
}
