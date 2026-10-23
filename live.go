package parser

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// LiveResult is the latest native run result, independent of process exit status.
// StructuredOutput retains JSON null and other JSON values without guessing from text.
type LiveResult struct {
	Text             string          `json:"text"`
	HasText          bool            `json:"hasText"`
	StructuredOutput json.RawMessage `json:"structuredOutput,omitempty"`
	Model            string          `json:"model,omitempty"`
	SessionID        string          `json:"sessionId,omitempty"`
	Usage            *TokenUsage     `json:"usage,omitempty"`
	IsError          bool            `json:"isError"`
	ErrorMessage     string          `json:"errorMessage,omitempty"`
	NativeSubtype    string          `json:"nativeSubtype,omitempty"`
}

type liveUsage struct {
	Input      int64 `json:"input_tokens"`
	Output     int64 `json:"output_tokens"`
	CacheRead  int64 `json:"cache_read_input_tokens"`
	Cached     int64 `json:"cached_input_tokens"`
	CacheWrite int64 `json:"cache_creation_input_tokens"`
}

func (u liveUsage) normalized() *TokenUsage {
	return &TokenUsage{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead + u.Cached,
		CacheWrite: u.CacheWrite, Total: u.Input + u.Output + u.CacheRead + u.CacheWrite}
}

type liveRecord struct {
	Type             string          `json:"type"`
	Subtype          string          `json:"subtype"`
	SessionID        string          `json:"session_id"`
	ThreadID         string          `json:"thread_id"`
	Model            string          `json:"model"`
	IsError          bool            `json:"is_error"`
	Result           *string         `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	Errors           []string        `json:"errors"`
	Usage            *liveUsage      `json:"usage"`
	Message          json.RawMessage `json:"message"`
	Error            json.RawMessage `json:"error"`
	Item             json.RawMessage `json:"item"`
	Event            json.RawMessage `json:"event"`
}

// LiveParser owns one fresh harness stream. Transcript parsers remain separate
// because persisted rollout envelopes and live process protocols are different.
type LiveParser struct {
	mu      sync.Mutex
	harness string
	result  LiveResult
	ended   bool
}

func NewLiveParser(harness string) (*LiveParser, error) {
	if harness != "claude" && harness != "codex" {
		return nil, fmt.Errorf("unsupported live harness %q", harness)
	}
	return &LiveParser{harness: harness}, nil
}

// Result returns the latest terminal result. Multiple native result records may
// occur in a run; callers must drain the process before choosing the final one.
func (p *LiveParser) Result() (LiveResult, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := cloneLiveResult(p.result)
	return r, p.ended
}

func cloneLiveResult(r LiveResult) LiveResult {
	r.StructuredOutput = bytes.Clone(r.StructuredOutput)
	if r.Usage != nil {
		u := *r.Usage
		r.Usage = &u
	}
	return r
}

func (p *LiveParser) ParseLine(line []byte) ([]ParsedEvent, error) {
	if len(bytes.TrimSpace(line)) == 0 {
		return nil, nil
	}
	var r liveRecord
	if err := json.Unmarshal(line, &r); err != nil {
		return nil, fmt.Errorf("decoding live harness record: %w", err)
	}
	if r.Type == "" {
		return nil, fmt.Errorf("live harness record is missing its type")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.harness == "codex" {
		return p.codex(r)
	}
	return p.claude(r)
}

func liveEvent(kind string, data EventData) []ParsedEvent {
	return []ParsedEvent{{EventType: kind, Timestamp: parseTimestamp(nil), Data: data}}
}

func (p *LiveParser) finish() []ParsedEvent {
	p.ended = true
	r := cloneLiveResult(p.result)
	return liveEvent("run_result", EventData{Result: &r, Usage: r.Usage, IsError: boolPtr(r.IsError)})
}

func (p *LiveParser) codex(r liveRecord) ([]ParsedEvent, error) {
	switch r.Type {
	case "thread.started":
		p.result.SessionID = r.ThreadID
		return liveEvent("run_started", EventData{}), nil
	case "turn.started":
		p.ended = false
		return liveEvent("turn_start", EventData{}), nil
	case "turn.completed":
		if r.Usage != nil {
			p.result.Usage = r.Usage.normalized()
		}
		return p.finish(), nil
	case "turn.failed":
		p.result.IsError = true
		p.result.ErrorMessage = nativeError(r.Error)
		return p.finish(), nil
	case "error":
		message := nativeError(r.Error)
		if len(r.Message) > 0 {
			message = nativeError(r.Message)
		}
		return liveEvent("error", EventData{Content: stringPtr(message), IsError: boolPtr(true)}), nil
	case "item.started", "item.updated", "item.completed":
		return p.codexItem(r)
	}
	return nil, nil
}

func (p *LiveParser) codexItem(r liveRecord) ([]ParsedEvent, error) {
	var item struct {
		ID       string          `json:"id"`
		Type     string          `json:"type"`
		Text     string          `json:"text"`
		Delta    *string         `json:"delta"`
		ExitCode *int            `json:"exit_code"`
		Output   json.RawMessage `json:"aggregated_output"`
	}
	if err := json.Unmarshal(r.Item, &item); err != nil {
		return nil, fmt.Errorf("decoding live Codex item: %w", err)
	}
	if item.Type == "agent_message" || item.Type == "reasoning" {
		kind := "agent_message"
		if item.Type == "reasoning" {
			kind = "thinking"
		}
		if item.Delta != nil {
			return liveEvent(kind+"_delta", EventData{Delta: item.Delta}), nil
		}
		if r.Type != "item.completed" {
			return nil, nil
		}
		if item.Type == "agent_message" {
			p.result.Text, p.result.HasText = item.Text, true
		}
		return liveEvent(kind, EventData{Content: &item.Text}), nil
	}
	data := EventData{ToolCallID: &item.ID, ToolName: &item.Type, Native: bytes.Clone(r.Item), ExitCode: item.ExitCode}
	if r.Type == "item.started" {
		return liveEvent("tool_call", data), nil
	}
	if r.Type == "item.completed" {
		if len(item.Output) > 0 {
			data.ToolOutput = stringPtr(nativeError(item.Output))
		}
		if item.ExitCode != nil {
			data.IsError = boolPtr(*item.ExitCode != 0)
		}
		return liveEvent("tool_result", data), nil
	}
	return liveEvent("tool_update", data), nil
}

func nativeError(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var value struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &value) == nil {
		return value.Message
	}
	return ""
}

func (p *LiveParser) claude(r liveRecord) ([]ParsedEvent, error) {
	if r.SessionID != "" {
		p.result.SessionID = r.SessionID
	}
	if r.Model != "" {
		p.result.Model = r.Model
	}
	switch r.Type {
	case "system":
		if r.Subtype == "init" {
			return liveEvent("run_started", EventData{Model: textPointer(r.Model)}), nil
		}
	case "stream_event":
		return p.claudeDelta(r.Event)
	case "assistant", "user":
		return p.claudeMessage(r)
	case "result":
		p.result.IsError = r.IsError || strings.HasPrefix(r.Subtype, "error")
		p.result.NativeSubtype = r.Subtype
		p.result.HasText = r.Result != nil
		p.result.Text = ""
		if r.Result != nil {
			p.result.Text = *r.Result
		}
		p.result.StructuredOutput = bytes.Clone(r.StructuredOutput)
		p.result.ErrorMessage = strings.Join(r.Errors, "\n")
		if r.Usage != nil {
			p.result.Usage = r.Usage.normalized()
		}
		return p.finish(), nil
	}
	return nil, nil
}

func textPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
