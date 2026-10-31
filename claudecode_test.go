package parser

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestParseTimestamp(t *testing.T) {
	tests := []struct {
		name    string
		input   interface{}
		wantVal int64
		wantNow bool
	}{
		{"nil", nil, 0, true},
		{"empty string", "", 0, true},
		{"float64", float64(1700000000000), 1700000000000, false},
		{"int64", int64(1700000000000), 1700000000000, false},
		{"int", int(1700000000), 1700000000, false},
		{"rfc3339 nano", "2026-09-19T10:00:00.123456Z", 0, false},
		{"rfc3339 standard", "2026-09-19T10:00:00Z", 0, false},
		{"invalid format", "not-a-date", 0, true},
		{"unsupported type", true, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTimestamp(tt.input)
			if tt.wantNow {
				if time.Since(time.UnixMilli(got)) > 5*time.Second {
					t.Errorf("expected recent timestamp, got %d", got)
				}
			} else if tt.wantVal > 0 {
				if got != tt.wantVal {
					t.Errorf("got %d, want %d", got, tt.wantVal)
				}
			} else {
				if got <= 0 {
					t.Errorf("expected positive timestamp, got %d", got)
				}
			}
		})
	}
}

func TestExtractContentString(t *testing.T) {
	tests := []struct {
		name  string
		input interface{}
		want  string
	}{
		{"nil", nil, ""},
		{"string", "direct string", "direct string"},
		{
			"array of text objects",
			[]interface{}{
				map[string]interface{}{"text": "part 1 "},
				map[string]interface{}{"text": "part 2"},
			},
			"part 1 part 2",
		},
		{
			"arbitrary map fallback to json",
			map[string]interface{}{"key": "value"},
			`{"key":"value"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractContentString(tt.input)
			if got != tt.want {
				t.Errorf("extractContentString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseClaudeContentBlocks(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		blocks := parseClaudeContentBlocks(nil)
		if blocks != nil {
			t.Errorf("expected nil blocks, got %v", blocks)
		}
	})

	t.Run("single string", func(t *testing.T) {
		blocks := parseClaudeContentBlocks("test text")
		if len(blocks) != 1 || blocks[0].Text != "test text" || blocks[0].Type != "text" {
			t.Errorf("unexpected blocks: %+v", blocks)
		}
	})

	t.Run("single map without type", func(t *testing.T) {
		blocks := parseClaudeContentBlocks(map[string]interface{}{"text": "map text"})
		if len(blocks) != 1 || blocks[0].Type != "text" || blocks[0].Text != "map text" {
			t.Errorf("unexpected blocks: %+v", blocks)
		}
	})

	t.Run("slice of maps", func(t *testing.T) {
		blocks := parseClaudeContentBlocks([]interface{}{
			map[string]interface{}{"text": "block 1"},
			map[string]interface{}{"type": "thinking", "thinking": "deep thoughts"},
		})
		if len(blocks) != 2 || blocks[0].Text != "block 1" || blocks[1].Thinking != "deep thoughts" {
			t.Errorf("unexpected blocks: %+v", blocks)
		}
	})
}

func TestClaudeCodeParser_ParseLine_Cases(t *testing.T) {
	parseLine := func(line string) ([]ParsedEvent, error) {
		p := NewClaudeCodeParser()
		p.hasStarted = true
		return p.ParseLine([]byte(line))
	}

	t.Run("empty line", func(t *testing.T) {
		evs, err := parseLine("")
		if err != nil || len(evs) != 0 {
			t.Errorf("expected nil, nil for empty line, got %v, %v", evs, err)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		_, err := parseLine("{invalid")
		if err == nil {
			t.Errorf("expected error for invalid json")
		}
	})

	t.Run("ai-title with aiTitle field", func(t *testing.T) {
		line := `{"type":"ai-title","aiTitle":"Optimized Title","timestamp":"2026-09-19T10:00:00.000Z","cwd":"/repo","gitBranch":"main"}`
		evs, err := parseLine(line)
		if err != nil {
			t.Fatal(err)
		}
		if len(evs) != 1 {
			t.Fatalf("expected 1 event, got %d", len(evs))
		}
		if evs[0].EventType != "title_change" || *evs[0].Data.Title != "Optimized Title" {
			t.Errorf("unexpected event: %+v", evs[0])
		}
	})

	t.Run("title with content fallback", func(t *testing.T) {
		line := `{"type":"title","content":[{"text":"Content Title"}],"timestamp":"2026-09-19T10:00:00.000Z"}`
		evs, err := parseLine(line)
		if err != nil {
			t.Fatal(err)
		}
		if len(evs) != 1 || *evs[0].Data.Title != "Content Title" {
			t.Errorf("unexpected event: %+v", evs)
		}
	})

	t.Run("last-prompt with lastPrompt and content", func(t *testing.T) {
		line1 := `{"type":"last-prompt","lastPrompt":"Fix bugs","timestamp":"2026-09-19T10:00:00.000Z"}`
		evs1, err := parseLine(line1)
		if err != nil {
			t.Fatalf("parseLine failed: %v", err)
		}
		if len(evs1) != 0 {
			t.Errorf("expected 0 events for last-prompt line1, got %d: %+v", len(evs1), evs1)
		}

		line2 := `{"type":"last-prompt","content":"Prompt via content","timestamp":"2026-09-19T10:00:00.000Z"}`
		evs2, err := parseLine(line2)
		if err != nil {
			t.Fatalf("parseLine failed: %v", err)
		}
		if len(evs2) != 0 {
			t.Errorf("expected 0 events for last-prompt line2, got %d: %+v", len(evs2), evs2)
		}
	})

	t.Run("last-prompt produces no activity event without timestamp", func(t *testing.T) {
		p := NewClaudeCodeParser()
		line := []byte(`{"type":"last-prompt","sessionId":"ses-lp-test","lastPrompt":"/code-review"}`)
		evs, err := p.ParseLine(line)
		if err != nil {
			t.Fatalf("ParseLine failed: %v", err)
		}
		for _, ev := range evs {
			if ev.EventType == "turn_start" {
				t.Fatalf("unexpected turn_start event emitted for last-prompt: %+v", ev)
			}
		}
	})

	t.Run("user prompt text", func(t *testing.T) {
		line := `{"type":"user","uuid":"u-1","timestamp":"2026-09-19T10:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"Please fix the bug in server.go"}]}}`
		evs, err := parseLine(line)
		if err != nil {
			t.Fatal(err)
		}
		if len(evs) != 1 {
			t.Fatalf("expected 1 event, got %d", len(evs))
		}
		if evs[0].EventType != "turn_start" || *evs[0].Data.Content != "Please fix the bug in server.go" {
			t.Errorf("unexpected event: %+v", evs[0])
		}
	})

	t.Run("user prompt raw string content", func(t *testing.T) {
		line := `{"type":"user","uuid":"u-raw","timestamp":"2026-09-19T10:00:00.000Z","message":{"role":"user","content":"How do I fix this build error?"}}`
		evs, err := parseLine(line)
		if err != nil {
			t.Fatal(err)
		}
		if len(evs) != 1 {
			t.Fatalf("expected 1 event, got %d", len(evs))
		}
		if evs[0].EventType != "turn_start" || *evs[0].Data.Content != "How do I fix this build error?" {
			t.Errorf("unexpected event: %+v", evs[0])
		}
	})

	t.Run("user tool_result with is_error false and ID fallback", func(t *testing.T) {
		isErr := false
		line := `{"type":"user","uuid":"u-2","timestamp":"2026-09-19T10:00:05.000Z","message":{"role":"user","content":[{"type":"tool_result","id":"call_999","text":"success output","is_error":false}]}}`
		evs, err := parseLine(line)
		if err != nil {
			t.Fatal(err)
		}
		if len(evs) != 1 {
			t.Fatalf("expected 1 event, got %d", len(evs))
		}
		if evs[0].EventType != "tool_result" {
			t.Errorf("eventType = %q, want 'tool_result'", evs[0].EventType)
		}
		if *evs[0].Data.ToolCallID != "call_999" {
			t.Errorf("ToolCallID = %q, want 'call_999'", *evs[0].Data.ToolCallID)
		}
		if *evs[0].Data.ToolOutput != "success output" {
			t.Errorf("ToolOutput = %q, want 'success output'", *evs[0].Data.ToolOutput)
		}
		if *evs[0].Data.IsError != isErr {
			t.Errorf("IsError = %v, want false", *evs[0].Data.IsError)
		}
	})

	t.Run("assistant thinking, standard tool_use, reasoning tokens roll-up", func(t *testing.T) {
		line := `{
			"type": "assistant",
			"uuid": "a-2",
			"timestamp": "2026-09-19T10:00:01.000Z",
			"message": {
				"role": "assistant",
				"model": "claude-3-7-sonnet-20250219",
				"stop_reason": "tool_use",
				"content": [
					{"type": "thinking", "thinking": "Let me run a test"},
					{"type": "tool_use", "id": "call_bash", "name": "Bash", "input": {"command": "go test ./..."}}
				],
				"usage": {
					"input_tokens": 100,
					"output_tokens": 50,
					"cache_read_input_tokens": 500,
					"cache_creation_input_tokens": 50,
					"reasoning_tokens": 20
				}
			}
		}`
		evs, err := parseLine(line)
		if err != nil {
			t.Fatal(err)
		}
		if len(evs) != 3 {
			t.Fatalf("expected 3 events (thinking, tool_call, turn_end), got %d", len(evs))
		}

		// 1. Thinking
		if evs[0].EventType != "thinking" || *evs[0].Data.Content != "Let me run a test" {
			t.Errorf("thinking event mismatch: %+v", evs[0])
		}

		// 2. Tool Call
		if evs[1].EventType != "tool_call" || *evs[1].Data.ToolName != "Bash" || *evs[1].Data.ToolCallID != "call_bash" {
			t.Errorf("tool_call event mismatch: %+v", evs[1])
		}

		// 3. Turn End with Usage roll-up
		if evs[2].EventType != "turn_end" {
			t.Errorf("turn_end event mismatch: %+v", evs[2])
		}
		if *evs[2].Data.StopReason != "tool_use" {
			t.Errorf("stop_reason mismatch: %v", evs[2].Data.StopReason)
		}
		u := evs[2].Data.Usage
		if u == nil {
			t.Fatalf("usage is nil")
		}
		if u.Input != 100 || u.Output != 50 || u.CacheRead != 500 || u.CacheWrite != 50 || u.Reasoning != 20 {
			t.Errorf("token usage breakdown mismatch: %+v", u)
		}
		if u.Total != 700 {
			t.Errorf("u.Total = %d, want 700 (reasoning tokens must not be double counted)", u.Total)
		}
	})

	t.Run("assistant text block conversational response", func(t *testing.T) {
		line := `{
			"type": "assistant",
			"uuid": "a-text",
			"timestamp": "2026-09-19T10:00:02.000Z",
			"cwd": "/workspace",
			"gitBranch": "fix/issue-5",
			"message": {
				"role": "assistant",
				"model": "claude-3-7-sonnet-20250219",
				"stop_reason": "end_turn",
				"content": [
					{"type": "text", "text": "I have completed the task and updated all unit tests."}
				],
				"usage": {
					"input_tokens": 150,
					"output_tokens": 30,
					"cache_read_input_tokens": 0,
					"cache_creation_input_tokens": 0
				}
			}
		}`
		evs, err := parseLine(line)
		if err != nil {
			t.Fatal(err)
		}
		if len(evs) != 1 {
			t.Fatalf("expected 1 event (turn_end), got %d: %+v", len(evs), evs)
		}
		if evs[0].EventType != "turn_end" {
			t.Errorf("eventType = %q, want 'turn_end'", evs[0].EventType)
		}
		if evs[0].Data.Content == nil || *evs[0].Data.Content != "I have completed the task and updated all unit tests." {
			t.Errorf("expected turn_end content to contain assistant text, got: %v", evs[0].Data.Content)
		}
		if evs[0].Data.Usage == nil || evs[0].Data.Usage.Total != 180 {
			t.Errorf("expected usage total 180, got: %+v", evs[0].Data.Usage)
		}
	})

	t.Run("assistant thinking and multiple text blocks combined", func(t *testing.T) {
		line := `{
			"type": "assistant",
			"uuid": "a-multi",
			"timestamp": "2026-09-19T10:00:03.000Z",
			"message": {
				"role": "assistant",
				"model": "claude-3-7-sonnet-20250219",
				"stop_reason": "end_turn",
				"content": [
					{"type": "thinking", "thinking": "Let's summarize the changes."},
					{"type": "text", "text": "First part of answer."},
					{"type": "text", "text": "Second part of answer."}
				],
				"usage": {
					"input_tokens": 200,
					"output_tokens": 80,
					"cache_read_input_tokens": 100,
					"cache_creation_input_tokens": 50,
					"thinking_tokens": 40
				}
			}
		}`
		evs, err := parseLine(line)
		if err != nil {
			t.Fatal(err)
		}
		if len(evs) != 2 {
			t.Fatalf("expected 2 events (thinking, turn_end), got %d: %+v", len(evs), evs)
		}
		if evs[0].EventType != "thinking" || *evs[0].Data.Content != "Let's summarize the changes." {
			t.Errorf("thinking event mismatch: %+v", evs[0])
		}
		if evs[1].EventType != "turn_end" {
			t.Errorf("turn_end event mismatch: %+v", evs[1])
		}
		expectedContent := "First part of answer.\nSecond part of answer."
		if evs[1].Data.Content == nil || *evs[1].Data.Content != expectedContent {
			t.Errorf("expected combined text %q, got %v", expectedContent, evs[1].Data.Content)
		}
		if evs[1].Data.Usage.Reasoning != 40 {
			t.Errorf("expected reasoning tokens 40 from thinking_tokens, got %d", evs[1].Data.Usage.Reasoning)
		}
		// 200 + 80 + 100 + 50 = 430
		if evs[1].Data.Usage.Total != 430 {
			t.Errorf("expected total 430, got %d", evs[1].Data.Usage.Total)
		}
	})

	t.Run("assistant top-level error string", func(t *testing.T) {
		line := `{
			"type": "assistant",
			"uuid": "a-err1",
			"timestamp": "2026-09-19T10:00:04.000Z",
			"error": "rate_limit_exceeded"
		}`
		evs, err := parseLine(line)
		if err != nil {
			t.Fatal(err)
		}
		if len(evs) != 1 {
			t.Fatalf("expected 1 error event, got %d: %+v", len(evs), evs)
		}
		if evs[0].EventType != "error" {
			t.Errorf("eventType = %q, want 'error'", evs[0].EventType)
		}
		if evs[0].Data.Content == nil || *evs[0].Data.Content != "rate_limit_exceeded" {
			t.Errorf("error content = %v, want 'rate_limit_exceeded'", evs[0].Data.Content)
		}
		if evs[0].Data.IsError == nil || !*evs[0].Data.IsError {
			t.Errorf("expected isError = true")
		}
		if evs[0].Data.IsRateLimit != nil && *evs[0].Data.IsRateLimit {
			t.Errorf("expected isRateLimit = false/nil for top-level error string without status 429")
		}
	})

	t.Run("assistant apiErrorStatus and isApiErrorMessage", func(t *testing.T) {
		line := `{
			"type": "assistant",
			"uuid": "a-err2",
			"timestamp": "2026-09-19T10:00:05.000Z",
			"isApiErrorMessage": true,
			"apiErrorStatus": 529,
			"message": {
				"role": "assistant",
				"model": "claude-3-7-sonnet-20250219",
				"content": [
					{"type": "text", "text": "Anthropic API overloaded. Please retry later."}
				]
			}
		}`
		evs, err := parseLine(line)
		if err != nil {
			t.Fatal(err)
		}
		// Expect turn_end and error event
		hasErrorEvent := false
		for _, ev := range evs {
			if ev.EventType == "error" {
				hasErrorEvent = true
				if ev.Data.Content == nil || *ev.Data.Content != "Anthropic API overloaded. Please retry later." {
					t.Errorf("error content = %v, want 'Anthropic API overloaded. Please retry later.'", ev.Data.Content)
				}
				if ev.Data.Model == nil || *ev.Data.Model != "claude-3-7-sonnet-20250219" {
					t.Errorf("error model = %v, want claude-3-7-sonnet-20250219", ev.Data.Model)
				}
				if ev.Data.IsRateLimit != nil && *ev.Data.IsRateLimit {
					t.Errorf("status 529 (server overloaded) must not be classified as rate limit")
				}
			}
		}
		if !hasErrorEvent {
			t.Errorf("expected error event to be emitted for api error message, got: %+v", evs)
		}
	})

	t.Run("assistant api_error_status 429 sets IsRateLimit", func(t *testing.T) {
		line := `{
			"type": "assistant",
			"uuid": "a-err-429",
			"timestamp": "2026-09-19T10:00:05.000Z",
			"api_error_status": 429,
			"error": "rate_limit_exceeded: retry after 60s"
		}`
		evs, err := parseLine(line)
		if err != nil {
			t.Fatal(err)
		}
		hasErrorEvent := false
		for _, ev := range evs {
			if ev.EventType == "error" {
				hasErrorEvent = true
				if ev.Data.IsRateLimit == nil || !*ev.Data.IsRateLimit {
					t.Errorf("expected IsRateLimit = true for api_error_status 429")
				}
				if ev.Data.ExitCode != nil {
					t.Errorf("expected ExitCode = nil (HTTP status codes must not be assigned to ExitCode), got %v", ev.Data.ExitCode)
				}
			}
		}
		if !hasErrorEvent {
			t.Errorf("expected error event emitted for api_error_status 429")
		}
	})

	t.Run("assistant api_error_status alone emits error with formatted status", func(t *testing.T) {
		line := `{
			"type": "assistant",
			"uuid": "a-err-429-snake",
			"timestamp": "2026-09-19T10:00:05.000Z",
			"api_error_status": 429
		}`
		evs, err := parseLine(line)
		if err != nil {
			t.Fatal(err)
		}
		var errEv *ParsedEvent
		for _, ev := range evs {
			if ev.EventType == "error" {
				errEv = &ev
				break
			}
		}
		if errEv == nil {
			t.Fatalf("expected error event emitted for api_error_status 429 alone")
		}
		if errEv.Data.Content == nil || *errEv.Data.Content != "API error: status 429" {
			t.Errorf("expected Content = 'API error: status 429', got %v", errEv.Data.Content)
		}
		if errEv.Data.IsRateLimit == nil || !*errEv.Data.IsRateLimit {
			t.Errorf("expected IsRateLimit = true for status 429")
		}
		if errEv.Data.ExitCode != nil {
			t.Errorf("expected ExitCode = nil (HTTP status codes must not be assigned to ExitCode), got %v", errEv.Data.ExitCode)
		}
	})

	t.Run("assistant status alone emits error with formatted status", func(t *testing.T) {
		line := `{
			"type": "assistant",
			"uuid": "a-err-500",
			"timestamp": "2026-09-19T10:00:05.000Z",
			"status": 500
		}`
		evs, err := parseLine(line)
		if err != nil {
			t.Fatal(err)
		}
		var errEv *ParsedEvent
		for _, ev := range evs {
			if ev.EventType == "error" {
				errEv = &ev
				break
			}
		}
		if errEv == nil {
			t.Fatalf("expected error event emitted for status 500")
		}
		if errEv.Data.Content == nil || *errEv.Data.Content != "API error: status 500" {
			t.Errorf("expected Content = 'API error: status 500', got %v", errEv.Data.Content)
		}
		if errEv.Data.IsRateLimit != nil && *errEv.Data.IsRateLimit {
			t.Errorf("expected IsRateLimit = false for status 500")
		}
		if errEv.Data.ExitCode != nil {
			t.Errorf("expected ExitCode = nil (HTTP status codes must not be assigned to ExitCode), got %v", errEv.Data.ExitCode)
		}
	})

	t.Run("assistant status 200 or 201 does not emit error", func(t *testing.T) {
		for _, status := range []int{200, 201} {
			line := fmt.Sprintf(`{
				"type": "assistant",
				"uuid": "a-ok-%d",
				"timestamp": "2026-09-19T10:00:05.000Z",
				"status": %d,
				"message": { "role": "assistant", "content": "All good" }
			}`, status, status)
			evs, err := parseLine(line)
			if err != nil {
				t.Fatal(err)
			}
			for _, ev := range evs {
				if ev.EventType == "error" {
					t.Errorf("status %d should not be treated as an error event", status)
				}
			}

			lineMsg := fmt.Sprintf(`{
				"type": "assistant",
				"uuid": "a-msg-ok-%d",
				"timestamp": "2026-09-19T10:00:05.000Z",
				"message": { "role": "assistant", "status": %d, "content": "All good" }
			}`, status, status)
			evsMsg, err := parseLine(lineMsg)
			if err != nil {
				t.Fatal(err)
			}
			for _, ev := range evsMsg {
				if ev.EventType == "error" {
					t.Errorf("message.status %d should not be treated as an error event", status)
				}
			}
		}
	})

	t.Run("assistant message api_error_status snake and status emit error", func(t *testing.T) {
		line1 := `{
			"type": "assistant",
			"uuid": "a-msg-429",
			"timestamp": "2026-09-19T10:00:05.000Z",
			"message": { "api_error_status": 429 }
		}`
		evs1, err := parseLine(line1)
		if err != nil {
			t.Fatal(err)
		}
		var errEv1 *ParsedEvent
		for _, ev := range evs1 {
			if ev.EventType == "error" {
				errEv1 = &ev
				break
			}
		}
		if errEv1 == nil {
			t.Fatalf("expected error event for message.api_error_status")
		}
		if errEv1.Data.Content == nil || *errEv1.Data.Content != "API error: status 429" {
			t.Errorf("expected 'API error: status 429', got %v", errEv1.Data.Content)
		}

		line2 := `{
			"type": "assistant",
			"uuid": "a-msg-503",
			"timestamp": "2026-09-19T10:00:05.000Z",
			"message": { "status": 503 }
		}`
		evs2, err := parseLine(line2)
		if err != nil {
			t.Fatal(err)
		}
		var errEv2 *ParsedEvent
		for _, ev := range evs2 {
			if ev.EventType == "error" {
				errEv2 = &ev
				break
			}
		}
		if errEv2 == nil {
			t.Fatalf("expected error event for message.status")
		}
		if errEv2.Data.Content == nil || *errEv2.Data.Content != "API error: status 503" {
			t.Errorf("expected 'API error: status 503', got %v", errEv2.Data.Content)
		}
	})

	t.Run("assistant isApiErrorMessage with api_error_status formats status code", func(t *testing.T) {
		line := `{
			"type": "assistant",
			"uuid": "a-is-api-err",
			"timestamp": "2026-09-19T10:00:05.000Z",
			"isApiErrorMessage": true,
			"api_error_status": 429
		}`
		evs, err := parseLine(line)
		if err != nil {
			t.Fatal(err)
		}
		var errEv *ParsedEvent
		for _, ev := range evs {
			if ev.EventType == "error" {
				errEv = &ev
				break
			}
		}
		if errEv == nil {
			t.Fatalf("expected error event")
		}
		if errEv.Data.Content == nil || *errEv.Data.Content != "API error: status 429" {
			t.Errorf("expected 'API error: status 429', got %v", errEv.Data.Content)
		}
	})

	t.Run("assistant message.error without content", func(t *testing.T) {
		line := `{
			"type": "assistant",
			"uuid": "a-err3",
			"timestamp": "2026-09-19T10:00:06.000Z",
			"message": {
				"role": "assistant",
				"error": "connection_reset_by_peer"
			}
		}`
		evs, err := parseLine(line)
		if err != nil {
			t.Fatal(err)
		}
		hasErrorEvent := false
		for _, ev := range evs {
			if ev.EventType == "error" {
				hasErrorEvent = true
				if ev.Data.Content == nil || *ev.Data.Content != "connection_reset_by_peer" {
					t.Errorf("error content = %v, want 'connection_reset_by_peer'", ev.Data.Content)
				}
			}
		}
		if !hasErrorEvent {
			t.Errorf("expected error event for message.error, got: %+v", evs)
		}
	})

	t.Run("assistant AskUserQuestion with prompt parameter", func(t *testing.T) {
		line := `{
			"type": "assistant",
			"message": {
				"role": "assistant",
				"content": [
					{"type": "tool_use", "id": "q1", "name": "AskUserQuestion", "input": {"prompt": "Confirm delete?"}}
				]
			}
		}`
		evs, err := parseLine(line)
		if err != nil {
			t.Fatal(err)
		}
		if len(evs) != 2 {
			t.Fatalf("expected 2 events (question_prompt, turn_end), got %d", len(evs))
		}
		if evs[0].EventType != "question_prompt" || *evs[0].Data.Question != "Confirm delete?" {
			t.Errorf("question_prompt event mismatch: %+v", evs[0])
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		_, err := parseLine("{invalid")
		if err == nil {
			t.Error("expected error for invalid json")
		}
	})
}

func TestClaudeCodeParser_EmitsRunStartedOnFirstLine(t *testing.T) {
	p := NewClaudeCodeParser()

	// Line 1: user prompt
	line1 := `{"type":"user","timestamp":"2026-09-21T10:00:00.000Z","cwd":"/repo/myproj","gitBranch":"feat/test","message":{"role":"user","content":"Hello world"}}`
	evs1, err := p.ParseLine([]byte(line1))
	if err != nil {
		t.Fatalf("ParseLine(line1) failed: %v", err)
	}

	if len(evs1) < 2 {
		t.Fatalf("expected at least 2 events on first line (run_started + turn_start), got %d: %+v", len(evs1), evs1)
	}

	// First event must be run_started
	if evs1[0].EventType != "run_started" {
		t.Errorf("evs1[0].EventType = %q, want 'run_started'", evs1[0].EventType)
	}
	if evs1[0].CWD != "/repo/myproj" {
		t.Errorf("evs1[0].CWD = %q, want '/repo/myproj'", evs1[0].CWD)
	}
	if evs1[0].GitBranch != "feat/test" {
		t.Errorf("evs1[0].GitBranch = %q, want 'feat/test'", evs1[0].GitBranch)
	}
	if evs1[0].Timestamp != 1789984800000 {
		t.Errorf("evs1[0].Timestamp = %d, want 1789984800000", evs1[0].Timestamp)
	}

	// Second event is the turn_start
	if evs1[1].EventType != "turn_start" {
		t.Errorf("evs1[1].EventType = %q, want 'turn_start'", evs1[1].EventType)
	}

	// Line 2: assistant response - MUST NOT emit another run_started
	line2 := `{"type":"assistant","timestamp":"2026-09-21T10:00:02.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Hi there"}]}}`
	evs2, err := p.ParseLine([]byte(line2))
	if err != nil {
		t.Fatalf("ParseLine(line2) failed: %v", err)
	}
	for _, ev := range evs2 {
		if ev.EventType == "run_started" {
			t.Errorf("unexpected duplicate run_started on line 2: %+v", ev)
		}
	}
}

func TestClaudeCodeParser_EmitsRunStartedForSession(t *testing.T) {
	p := NewClaudeCodeParser()

	sessA := "session-aaa-111"
	sessB := "session-bbb-222"

	// 1. First line of session A -> MUST emit run_started
	lineA1 := `{"sessionId":"` + sessA + `","type":"user","timestamp":"2026-09-21T10:00:00.000Z","cwd":"/repo/a","gitBranch":"main","message":{"role":"user","content":"Task A"}}`
	evsA1, err := p.ParseLine([]byte(lineA1))
	if err != nil {
		t.Fatalf("p.ParseLine(lineA1) failed: %v", err)
	}
	if len(evsA1) < 2 {
		t.Fatalf("expected at least 2 events on first line of session A, got %d", len(evsA1))
	}
	if evsA1[0].EventType != "run_started" {
		t.Errorf("expected first event of session A to be run_started, got %q", evsA1[0].EventType)
	}

	// 2. Second line of session A -> MUST NOT emit run_started
	lineA2 := `{"sessionId":"` + sessA + `","type":"assistant","timestamp":"2026-09-21T10:00:02.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Working on A"}]}}`
	evsA2, err := p.ParseLine([]byte(lineA2))
	if err != nil {
		t.Fatalf("p.ParseLine(lineA2) failed: %v", err)
	}
	for _, ev := range evsA2 {
		if ev.EventType == "run_started" {
			t.Errorf("unexpected duplicate run_started on line A2: %+v", ev)
		}
	}

	// 3. First line of session B -> MUST emit run_started for session B
	lineB1 := `{"sessionId":"` + sessB + `","type":"ai-title","aiTitle":"Title B","timestamp":"2026-09-21T11:00:00.000Z","cwd":"/repo/b"}`
	evsB1, err := p.ParseLine([]byte(lineB1))
	if err != nil {
		t.Fatalf("p.ParseLine(lineB1) failed: %v", err)
	}
	if len(evsB1) < 2 {
		t.Fatalf("expected at least 2 events on first line of session B, got %d", len(evsB1))
	}
	if evsB1[0].EventType != "run_started" {
		t.Errorf("expected first event of session B to be run_started, got %q", evsB1[0].EventType)
	}
	if evsB1[1].EventType != "title_change" {
		t.Errorf("expected second event of session B to be title_change, got %q", evsB1[1].EventType)
	}

	// 4. After p.Reset(), session A emits run_started on next first line
	p.Reset()
	evsA3, err := p.ParseLine([]byte(lineA1))
	if err != nil {
		t.Fatalf("p.ParseLine(lineA1 after reset) failed: %v", err)
	}
	if len(evsA3) < 2 || evsA3[0].EventType != "run_started" {
		t.Errorf("expected run_started after reset, got %+v", evsA3)
	}
}

func TestClaudeCodeParser_EdgeCasesAndReset(t *testing.T) {
	p := NewClaudeCodeParser()

	// Empty line
	evs, err := p.ParseLine([]byte(""))
	if err != nil || evs != nil {
		t.Errorf("expected nil, nil for empty line, got %v, %v", evs, err)
	}

	// Invalid JSON
	_, err = p.ParseLine([]byte("{bad-json"))
	if err == nil {
		t.Errorf("expected error for bad json in ParseLine")
	}

	// Line with Title and Model
	line := `{"title":"Manual Title","sessionId":"ses-custom","type":"user","message":{"role":"user","content":"do it","model":"claude-3-7-sonnet"}}`
	evs, err = p.ParseLine([]byte(line))
	if err != nil {
		t.Fatalf("ParseLine failed: %v", err)
	}
	if len(evs) < 1 || evs[0].EventType != "run_started" {
		t.Fatalf("expected run_started as first event, got %+v", evs)
	}
	if evs[0].Data.Title == nil || *evs[0].Data.Title != "Manual Title" {
		t.Errorf("expected Title 'Manual Title', got %v", evs[0].Data.Title)
	}
	if evs[0].Data.Model == nil || *evs[0].Data.Model != "claude-3-7-sonnet" {
		t.Errorf("expected Model 'claude-3-7-sonnet', got %v", evs[0].Data.Model)
	}

	// Reset parser
	p.Reset()
	// After reset, first line should emit run_started again
	evsAfterReset, err := p.ParseLine([]byte(line))
	if err != nil {
		t.Fatalf("ParseLine after Reset failed: %v", err)
	}
	if len(evsAfterReset) < 1 || evsAfterReset[0].EventType != "run_started" {
		t.Errorf("expected run_started after Reset, got %+v", evsAfterReset)
	}
}

func TestClaudeCodeParser_SchemaAliases(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		wantType  string
		wantTitle string
	}{
		{
			name:      "ai-title with canonical aiTitle",
			line:      `{"type":"ai-title","sessionId":"ses-1","aiTitle":"Canonical AI Title"}`,
			wantType:  "title_change",
			wantTitle: "Canonical AI Title",
		},
		{
			name:      "ai-title with snake_case ai_title",
			line:      `{"type":"ai-title","sessionId":"ses-1","ai_title":"Snake Case AI Title"}`,
			wantType:  "title_change",
			wantTitle: "Snake Case AI Title",
		},
		{
			name:      "ai-title with title alias",
			line:      `{"type":"ai-title","sessionId":"ses-1","title":"Plain Title Alias"}`,
			wantType:  "title_change",
			wantTitle: "Plain Title Alias",
		},
		{
			name:      "agent-name with canonical agentName",
			line:      `{"type":"agent-name","sessionId":"ses-1","agentName":"architect"}`,
			wantType:  "title_change",
			wantTitle: "architect",
		},
		{
			name:      "agent-name with alias name",
			line:      `{"type":"agent-name","sessionId":"ses-1","name":"planner"}`,
			wantType:  "title_change",
			wantTitle: "planner",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewClaudeCodeParser()
			evs, err := p.ParseLine([]byte(tt.line))
			if err != nil {
				t.Fatalf("ParseLine error: %v", err)
			}
			// Line 1 should emit run_started with Title set
			if len(evs) < 1 || evs[0].EventType != "run_started" {
				t.Fatalf("expected first event 'run_started', got %+v", evs)
			}
			if evs[0].Data.Title == nil || *evs[0].Data.Title != tt.wantTitle {
				t.Errorf("run_started Title = %v, want %q", evs[0].Data.Title, tt.wantTitle)
			}

			// Followed by title_change
			var found *ParsedEvent
			for i := range evs {
				if evs[i].EventType == tt.wantType {
					found = &evs[i]
					break
				}
			}
			if found == nil {
				t.Fatalf("expected event %q, got %+v", tt.wantType, evs)
			}
			if found.Data.Title == nil || *found.Data.Title != tt.wantTitle {
				t.Errorf("Title = %v, want %q", found.Data.Title, tt.wantTitle)
			}
		})
	}
}

func TestClaudeCodeParser_BatchAndNotebookEditingTools(t *testing.T) {
	t.Run("MultiEdit tool_use", func(t *testing.T) {
		p := NewClaudeCodeParser()
		assistantLine := []byte(`{
			"type": "assistant",
			"sessionId": "ses-edit-1",
			"message": {
				"role": "assistant",
				"content": [
					{
						"type": "tool_use",
						"id": "call_multiedit_1",
						"name": "MultiEdit",
						"input": {
							"file_path": "src/parser.go",
							"edits": [{"old_string": "foo", "new_string": "bar"}]
						}
					}
				]
			}
		}`)
		evs, err := p.ParseLine(assistantLine)
		if err != nil {
			t.Fatalf("ParseLine error: %v", err)
		}
		// Expect run_started and tool_call
		var toolCall *ParsedEvent
		for i := range evs {
			if evs[i].EventType == "tool_call" {
				toolCall = &evs[i]
				break
			}
		}
		if toolCall == nil {
			t.Fatalf("expected tool_call event, got %+v", evs)
		}
		if toolCall.Data.ToolName == nil || *toolCall.Data.ToolName != "MultiEdit" {
			t.Errorf("ToolName = %v, want 'MultiEdit'", toolCall.Data.ToolName)
		}
		if toolCall.Data.ToolCallID == nil || *toolCall.Data.ToolCallID != "call_multiedit_1" {
			t.Errorf("ToolCallID = %v, want 'call_multiedit_1'", toolCall.Data.ToolCallID)
		}
	})

	t.Run("NotebookEdit tool_use", func(t *testing.T) {
		p := NewClaudeCodeParser()
		line := []byte(`{
			"type": "assistant",
			"sessionId": "ses-nb-1",
			"message": {
				"role": "assistant",
				"content": [
					{
						"type": "tool_use",
						"id": "call_nbedit_1",
						"name": "NotebookEdit",
						"input": {
							"notebook_path": "analysis.ipynb",
							"cell_index": 2,
							"new_source": "import pandas as pd"
						}
					}
				]
			}
		}`)
		evs, err := p.ParseLine(line)
		if err != nil {
			t.Fatalf("ParseLine error: %v", err)
		}
		var toolCall *ParsedEvent
		for i := range evs {
			if evs[i].EventType == "tool_call" {
				toolCall = &evs[i]
				break
			}
		}
		if toolCall == nil {
			t.Fatalf("expected tool_call event, got %+v", evs)
		}
		if toolCall.Data.ToolName == nil || *toolCall.Data.ToolName != "NotebookEdit" {
			t.Errorf("ToolName = %v, want 'NotebookEdit'", toolCall.Data.ToolName)
		}
	})
}

func TestClaudeCodeParser_NormalizedFileModification(t *testing.T) {
	t.Run("Edit tool_use produces normalized FileModification", func(t *testing.T) {
		p := NewClaudeCodeParser()
		line := []byte(`{
			"type": "assistant",
			"sessionId": "ses-edit-norm",
			"message": {
				"role": "assistant",
				"content": [
					{
						"type": "tool_use",
						"id": "call_edit_1",
						"name": "Edit",
						"input": {
							"file_path": "main.go",
							"old_string": "fmt.Println(\"hello\")",
							"new_string": "fmt.Println(\"world\")"
						}
					}
				]
			}
		}`)
		evs, err := p.ParseLine(line)
		if err != nil {
			t.Fatalf("ParseLine failed: %v", err)
		}
		var toolCall *ParsedEvent
		for i := range evs {
			if evs[i].EventType == "tool_call" {
				toolCall = &evs[i]
				break
			}
		}
		if toolCall == nil {
			t.Fatal("expected tool_call event")
		}
		fm := toolCall.Data.FileModification
		if fm == nil {
			t.Fatal("expected non-nil FileModification for Edit tool")
		}
		if fm.Action != "EDIT" {
			t.Errorf("Action = %q, want 'EDIT'", fm.Action)
		}
		if fm.FilePath != "main.go" {
			t.Errorf("FilePath = %q, want 'main.go'", fm.FilePath)
		}
		if len(fm.Hunks) != 1 {
			t.Fatalf("expected 1 hunk, got %d", len(fm.Hunks))
		}
		hunk := fm.Hunks[0]
		if len(hunk.Lines) != 2 {
			t.Fatalf("expected 2 lines in hunk, got %d", len(hunk.Lines))
		}
		if hunk.Lines[0].Type != "del" || hunk.Lines[0].Content != "fmt.Println(\"hello\")" {
			t.Errorf("unexpected del line: %+v", hunk.Lines[0])
		}
		if hunk.Lines[1].Type != "add" || hunk.Lines[1].Content != "fmt.Println(\"world\")" {
			t.Errorf("unexpected add line: %+v", hunk.Lines[1])
		}
	})

	t.Run("MultiEdit tool_use produces normalized FileModification with multiple hunks", func(t *testing.T) {
		p := NewClaudeCodeParser()
		line := []byte(`{
			"type": "assistant",
			"sessionId": "ses-multiedit-norm",
			"message": {
				"role": "assistant",
				"content": [
					{
						"type": "tool_use",
						"id": "call_multiedit_2",
						"name": "MultiEdit",
						"input": {
							"file_path": "pkg/calc.go",
							"edits": [
								{"old_string": "a + b", "new_string": "a * b"},
								{"old_string": "x - y", "new_string": "x / y"}
							]
						}
					}
				]
			}
		}`)
		evs, err := p.ParseLine(line)
		if err != nil {
			t.Fatalf("ParseLine failed: %v", err)
		}
		var toolCall *ParsedEvent
		for i := range evs {
			if evs[i].EventType == "tool_call" {
				toolCall = &evs[i]
				break
			}
		}
		if toolCall == nil {
			t.Fatal("expected tool_call event")
		}
		fm := toolCall.Data.FileModification
		if fm == nil {
			t.Fatal("expected non-nil FileModification for MultiEdit tool")
		}
		if fm.Action != "EDIT" {
			t.Errorf("Action = %q, want 'EDIT'", fm.Action)
		}
		if fm.FilePath != "pkg/calc.go" {
			t.Errorf("FilePath = %q, want 'pkg/calc.go'", fm.FilePath)
		}
		if len(fm.Hunks) != 2 {
			t.Fatalf("expected 2 hunks, got %d", len(fm.Hunks))
		}
	})

	t.Run("Write tool_use produces normalized FileModification", func(t *testing.T) {
		p := NewClaudeCodeParser()
		line := []byte(`{
			"type": "assistant",
			"sessionId": "ses-write-norm",
			"message": {
				"role": "assistant",
				"content": [
					{
						"type": "tool_use",
						"id": "call_write_1",
						"name": "Write",
						"input": {
							"file_path": "README.md",
							"content": "# Hello World\n"
						}
					}
				]
			}
		}`)
		evs, err := p.ParseLine(line)
		if err != nil {
			t.Fatalf("ParseLine failed: %v", err)
		}
		var toolCall *ParsedEvent
		for i := range evs {
			if evs[i].EventType == "tool_call" {
				toolCall = &evs[i]
				break
			}
		}
		if toolCall == nil {
			t.Fatal("expected tool_call event")
		}
		fm := toolCall.Data.FileModification
		if fm == nil {
			t.Fatal("expected non-nil FileModification for Write tool")
		}
		if fm.Action != "WRITE" {
			t.Errorf("Action = %q, want 'WRITE'", fm.Action)
		}
		if fm.FilePath != "README.md" {
			t.Errorf("FilePath = %q, want 'README.md'", fm.FilePath)
		}
		if fm.Content == nil || *fm.Content != "# Hello World\n" {
			t.Errorf("Content = %v, want '# Hello World\\n'", fm.Content)
		}
	})
}

func TestExtractClaudeCodeTitleFromPrompt(t *testing.T) {
	tests := []struct {
		name      string
		prompt    string
		wantTitle string
		wantOK    bool
	}{
		{
			name:      "simple prompt",
			prompt:    "Please fix the bug in server.go",
			wantTitle: "Please fix the bug in server.go",
			wantOK:    true,
		},
		{
			name:      "slash command prompt",
			prompt:    "/code-review",
			wantTitle: "/code-review",
			wantOK:    true,
		},
		{
			name:      "multiline prompt takes first line",
			prompt:    "Refactor API server router\nHere is additional context\nand details",
			wantTitle: "Refactor API server router",
			wantOK:    true,
		},
		{
			name:      "synthetic local command caveat stripped",
			prompt:    "<local-command-caveat>Caveat: generated by local commands</local-command-caveat>\n/code-review",
			wantTitle: "/code-review",
			wantOK:    true,
		},
		{
			name:      "synthetic local command sequence stripped completely",
			prompt:    "<command-name>/model</command-name>\n<command-message>model</command-message>\n<command-args></command-args>",
			wantTitle: "",
			wantOK:    false,
		},
		{
			name:      "synthetic local command stdout stripped",
			prompt:    "<local-command-stdout>Set model to Opus 5.5</local-command-stdout>",
			wantTitle: "",
			wantOK:    false,
		},
		{
			name:      "task notification stripped completely",
			prompt:    "<task-notification>\n<task-id>123</task-id>\n<status>completed</status>\n</task-notification>",
			wantTitle: "",
			wantOK:    false,
		},
		{
			name:      "system reminder stripped",
			prompt:    "<system-reminder>Some system rules</system-reminder>\nFix the broken test",
			wantTitle: "Fix the broken test",
			wantOK:    true,
		},
		{
			name:      "fork boilerplate stripped",
			prompt:    "<fork-boilerplate>Fork started</fork-boilerplate>\nInvestigate memory leak",
			wantTitle: "Investigate memory leak",
			wantOK:    true,
		},
		{
			name:      "empty prompt",
			prompt:    "   \n  \t  ",
			wantTitle: "",
			wantOK:    false,
		},
		{
			name:      "long prompt truncated to 100 runes",
			prompt:    "This is a very long prompt that exceeds one hundred characters by having a lot of words describing the task in great detail and length",
			wantTitle: "This is a very long prompt that exceeds one hundred characters by having a lot of words describing t",
			wantOK:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTitle, gotOK := ExtractClaudeCodeTitleFromPrompt(tt.prompt)
			if gotOK != tt.wantOK {
				t.Fatalf("ExtractClaudeCodeTitleFromPrompt() gotOK = %v, want %v", gotOK, tt.wantOK)
			}
			if gotTitle != tt.wantTitle {
				t.Errorf("ExtractClaudeCodeTitleFromPrompt() gotTitle = %q, want %q", gotTitle, tt.wantTitle)
			}
		})
	}
}

func TestClaudeCode_TitleFromUserPromptAndLastPrompt(t *testing.T) {
	t.Run("user prompt sets title on turn_start", func(t *testing.T) {
		p := NewClaudeCodeParser()
		line := []byte(`{"type":"user","sessionId":"ses-title-1","message":{"role":"user","content":"Implement dark mode"}}`)
		evs, err := p.ParseLine(line)
		if err != nil {
			t.Fatalf("ParseLine error: %v", err)
		}
		var turnStart *ParsedEvent
		for i := range evs {
			if evs[i].EventType == "turn_start" {
				turnStart = &evs[i]
				break
			}
		}
		if turnStart == nil {
			t.Fatal("expected turn_start event")
		}
		if turnStart.Data.Title == nil || *turnStart.Data.Title != "Implement dark mode" {
			t.Errorf("turn_start Title = %v, want 'Implement dark mode'", turnStart.Data.Title)
		}
	})

	t.Run("last-prompt produces no turn_start event", func(t *testing.T) {
		p := NewClaudeCodeParser()
		line := []byte(`{"type":"last-prompt","sessionId":"ses-title-2","lastPrompt":"/code-review"}`)
		evs, err := p.ParseLine(line)
		if err != nil {
			t.Fatalf("ParseLine error: %v", err)
		}
		for _, ev := range evs {
			if ev.EventType == "turn_start" {
				t.Fatalf("unexpected turn_start event emitted for last-prompt: %+v", ev)
			}
		}
	})

	t.Run("custom-title emits title_change", func(t *testing.T) {
		p := NewClaudeCodeParser()
		line := []byte(`{"type":"custom-title","sessionId":"ses-title-3","customTitle":"User Renamed Session"}`)
		evs, err := p.ParseLine(line)
		if err != nil {
			t.Fatalf("ParseLine error: %v", err)
		}
		var titleChange *ParsedEvent
		for i := range evs {
			if evs[i].EventType == "title_change" {
				titleChange = &evs[i]
				break
			}
		}
		if titleChange == nil {
			t.Fatal("expected title_change event")
		}
		if titleChange.Data.Title == nil || *titleChange.Data.Title != "User Renamed Session" {
			t.Errorf("title_change Title = %v, want 'User Renamed Session'", titleChange.Data.Title)
		}
	})
}

func TestClaudeCodeParser_TitlePrecedence(t *testing.T) {
	p := NewClaudeCodeParser()
	sessID := "ses-prec-1"

	// 1. First prompt establishes initial title
	line1 := []byte(`{"type":"user","sessionId":"` + sessID + `","message":{"role":"user","content":"Initial task prompt"}}`)
	evs1, err := p.ParseLine(line1)
	if err != nil {
		t.Fatalf("line 1 failed: %v", err)
	}
	var turn1 *ParsedEvent
	for i := range evs1 {
		if evs1[i].EventType == "turn_start" {
			turn1 = &evs1[i]
			break
		}
	}
	if turn1 == nil || turn1.Data.Title == nil || *turn1.Data.Title != "Initial task prompt" {
		t.Fatalf("expected turn1 title 'Initial task prompt', got %+v", turn1)
	}

	// 2. Second prompt does NOT overwrite established title
	line2 := []byte(`{"type":"user","sessionId":"` + sessID + `","message":{"role":"user","content":"yes please"}}`)
	evs2, err := p.ParseLine(line2)
	if err != nil {
		t.Fatalf("line 2 failed: %v", err)
	}
	for i := range evs2 {
		if evs2[i].EventType == "turn_start" && evs2[i].Data.Title != nil {
			t.Errorf("subsequent prompt must not set title, got %q", *evs2[i].Data.Title)
		}
	}

	// 3. AI title emits title_change
	line3 := []byte(`{"type":"ai-title","sessionId":"` + sessID + `","aiTitle":"AI Synthesized Title"}`)
	evs3, err := p.ParseLine(line3)
	if err != nil {
		t.Fatalf("line 3 failed: %v", err)
	}
	var titleChange *ParsedEvent
	for i := range evs3 {
		if evs3[i].EventType == "title_change" {
			titleChange = &evs3[i]
			break
		}
	}
	if titleChange == nil || titleChange.Data.Title == nil || *titleChange.Data.Title != "AI Synthesized Title" {
		t.Fatalf("expected AI title_change 'AI Synthesized Title', got %+v", titleChange)
	}

	// 4. Prompt after AI title does NOT overwrite AI title
	line4 := []byte(`{"type":"user","sessionId":"` + sessID + `","message":{"role":"user","content":"run tests now"}}`)
	evs4, err := p.ParseLine(line4)
	if err != nil {
		t.Fatalf("line 4 failed: %v", err)
	}
	for i := range evs4 {
		if evs4[i].EventType == "turn_start" && evs4[i].Data.Title != nil {
			t.Errorf("prompt after ai-title must not set title, got %q", *evs4[i].Data.Title)
		}
	}

	// 5. Custom title overrides AI title
	line5 := []byte(`{"type":"custom-title","sessionId":"` + sessID + `","customTitle":"Custom Override"}`)
	evs5, err := p.ParseLine(line5)
	if err != nil {
		t.Fatalf("line 5 failed: %v", err)
	}
	var customTitleChange *ParsedEvent
	for i := range evs5 {
		if evs5[i].EventType == "title_change" {
			customTitleChange = &evs5[i]
			break
		}
	}
	if customTitleChange == nil || customTitleChange.Data.Title == nil || *customTitleChange.Data.Title != "Custom Override" {
		t.Fatalf("expected custom title_change 'Custom Override', got %+v", customTitleChange)
	}
}

func TestClaudeCodeParser_SessionWithoutAiTitle_MatchesCodeReview(t *testing.T) {
	p := NewClaudeCodeParser()
	sessID := "f9c8fea8-a808-4887-9018-121c5d354d59"

	lines := [][]byte{
		[]byte(`{"type":"queue-operation","operation":"dequeue","timestamp":"2026-09-22T19:57:48.825Z","sessionId":"` + sessID + `"}`),
		[]byte(`{"type":"user","sessionId":"` + sessID + `","message":{"role":"user","content":"<local-command-caveat>Caveat...</local-command-caveat>"}}`),
		[]byte(`{"type":"user","sessionId":"` + sessID + `","message":{"role":"user","content":"<command-name>/model</command-name>\n<command-message>model</command-message>\n<command-args></command-args>"}}`),
		[]byte(`{"type":"user","sessionId":"` + sessID + `","message":{"role":"user","content":"<local-command-stdout>Set model to Opus 5.5</local-command-stdout>"}}`),
		[]byte(`{"type":"user","sessionId":"` + sessID + `","message":{"role":"user","content":"/code-review"}}`),
		[]byte(`{"type":"user","sessionId":"` + sessID + `","message":{"role":"user","content":"<task-notification><task-id>a1</task-id><status>completed</status></task-notification>"}}`),
		[]byte(`{"type":"last-prompt","lastPrompt":"/code-review","sessionId":"` + sessID + `"}`),
	}

	var allEvents []ParsedEvent
	for _, l := range lines {
		evs, err := p.ParseLine(l)
		if err != nil {
			t.Fatalf("ParseLine failed: %v", err)
		}
		allEvents = append(allEvents, evs...)
	}

	// Verify that a turn_start was emitted with title "/code-review"
	var foundTurnWithTitle *ParsedEvent
	for i := range allEvents {
		if allEvents[i].EventType == "turn_start" && allEvents[i].Data.Title != nil {
			foundTurnWithTitle = &allEvents[i]
			break
		}
	}
	if foundTurnWithTitle == nil {
		t.Fatal("expected at least one turn_start with Title '/code-review'")
	}
	if *foundTurnWithTitle.Data.Title != "/code-review" {
		t.Errorf("Title = %q, want '/code-review'", *foundTurnWithTitle.Data.Title)
	}
}

func TestClaudeCodeParser_StreamedMultiBlockDeduplication(t *testing.T) {
	t.Run("ClaudeCodeParser deduplicates streamed blocks with same requestId", func(t *testing.T) {
		p := NewClaudeCodeParser()
		sessID := "stream-session-1"

		line1 := `{"type":"assistant","requestId":"req_011CfKA7YVz3YULfeA8Dx8yU","sessionId":"` + sessID + `","apiBlockIndex":0,"message":{"id":"msg_011CfKA7YVz3YULfeA8Dx8yU","role":"assistant","model":"claude-3-7-sonnet","stop_reason":"tool_use","content":[{"type":"thinking","thinking":"Analyzing the repository structure"}],"usage":{"input_tokens":2,"output_tokens":181,"cache_read_input_tokens":27339,"cache_creation_input_tokens":21361}}}`
		line2 := `{"type":"assistant","requestId":"req_011CfKA7YVz3YULfeA8Dx8yU","sessionId":"` + sessID + `","apiBlockIndex":1,"message":{"id":"msg_011CfKA7YVz3YULfeA8Dx8yU","role":"assistant","model":"claude-3-7-sonnet","stop_reason":"tool_use","content":[{"type":"tool_use","name":"claude-code-codegraph","id":"call_1","input":{"action":"search"}}],"usage":{"input_tokens":2,"output_tokens":181,"cache_read_input_tokens":27339,"cache_creation_input_tokens":21361}}}`
		line3 := `{"type":"assistant","requestId":"req_011CfKA7YVz3YULfeA8Dx8yU","sessionId":"` + sessID + `","apiBlockIndex":2,"message":{"id":"msg_011CfKA7YVz3YULfeA8Dx8yU","role":"assistant","model":"claude-3-7-sonnet","stop_reason":"tool_use","content":[{"type":"tool_use","name":"golang","id":"call_2","input":{"action":"build"}}],"usage":{"input_tokens":2,"output_tokens":181,"cache_read_input_tokens":27339,"cache_creation_input_tokens":21361}}}`

		evs1, err := p.ParseLine([]byte(line1))
		if err != nil {
			t.Fatalf("ParseLine line1 failed: %v", err)
		}
		evs2, err := p.ParseLine([]byte(line2))
		if err != nil {
			t.Fatalf("ParseLine line2 failed: %v", err)
		}
		evs3, err := p.ParseLine([]byte(line3))
		if err != nil {
			t.Fatalf("ParseLine line3 failed: %v", err)
		}

		allEvents := append(append(evs1, evs2...), evs3...)
		var turnEndEvents []ParsedEvent

		for _, ev := range allEvents {
			if ev.EventType == "turn_end" {
				turnEndEvents = append(turnEndEvents, ev)
			}
		}

		var nonRunEvents []ParsedEvent
		for _, ev := range evs1 {
			if ev.EventType != "run_started" {
				nonRunEvents = append(nonRunEvents, ev)
			}
		}
		if len(nonRunEvents) != 1 || nonRunEvents[0].EventType != "thinking" {
			t.Fatalf("expected line1 to emit thinking event (excluding run_started), got %v", nonRunEvents)
		}
		if len(evs2) != 2 || evs2[0].EventType != "tool_call" || evs2[1].EventType != "turn_end" {
			t.Fatalf("expected line2 to emit [tool_call, turn_end], got %v", evs2)
		}
		if len(evs3) != 1 || evs3[0].EventType != "tool_call" {
			t.Fatalf("expected line3 to emit [tool_call] without duplicate turn_end, got %v", evs3)
		}
		if len(turnEndEvents) != 1 {
			t.Fatalf("expected exactly 1 turn_end event for requestId, got %d", len(turnEndEvents))
		}
		if turnEndEvents[0].Data.Usage == nil {
			t.Fatal("turn_end usage is nil")
		}
		if turnEndEvents[0].Data.Usage.Output != 181 {
			t.Errorf("output tokens = %d, want 181", turnEndEvents[0].Data.Usage.Output)
		}
		if turnEndEvents[0].Data.Usage.Total != 48883 {
			t.Errorf("total tokens = %d, want 48883", turnEndEvents[0].Data.Usage.Total)
		}
	})

	t.Run("two independent ClaudeCodeParser instances fed the same requestId each emit usage once", func(t *testing.T) {
		p1 := NewClaudeCodeParser()
		p2 := NewClaudeCodeParser()

		line := `{"type":"assistant","requestId":"req_shared_iso","sessionId":"stream-session-iso","apiBlockIndex":0,"message":{"id":"msg_shared_iso","role":"assistant","model":"claude-3-7-sonnet","stop_reason":"end_turn","content":[{"type":"text","text":"Done"}],"usage":{"input_tokens":5,"output_tokens":100,"cache_read_input_tokens":1000,"cache_creation_input_tokens":0}}}`

		evs1, err := p1.ParseLine([]byte(line))
		if err != nil {
			t.Fatalf("p1.ParseLine failed: %v", err)
		}
		var p1TurnEnd *ParsedEvent
		for i := range evs1 {
			if evs1[i].EventType == "turn_end" {
				p1TurnEnd = &evs1[i]
				break
			}
		}
		if p1TurnEnd == nil || p1TurnEnd.Data.Usage == nil || p1TurnEnd.Data.Usage.Output != 100 {
			t.Fatalf("expected p1 to emit turn_end with usage output=100, got: %+v", p1TurnEnd)
		}

		evs2, err := p2.ParseLine([]byte(line))
		if err != nil {
			t.Fatalf("p2.ParseLine failed: %v", err)
		}
		var p2TurnEnd *ParsedEvent
		for i := range evs2 {
			if evs2[i].EventType == "turn_end" {
				p2TurnEnd = &evs2[i]
				break
			}
		}
		if p2TurnEnd == nil || p2TurnEnd.Data.Usage == nil || p2TurnEnd.Data.Usage.Output != 100 {
			t.Fatalf("expected p2 to also emit turn_end with usage output=100 due to isolated state, got: %+v", p2TurnEnd)
		}

		// A second line with the same requestId sent to p1 must have usage deduplicated (nil usage)
		lineDup := `{"type":"assistant","requestId":"req_shared_iso","sessionId":"stream-session-iso","apiBlockIndex":1,"message":{"id":"msg_shared_iso","role":"assistant","model":"claude-3-7-sonnet","stop_reason":"end_turn","content":[{"type":"text","text":"More"}],"usage":{"input_tokens":5,"output_tokens":100,"cache_read_input_tokens":1000,"cache_creation_input_tokens":0}}}`
		evs1Dup, err := p1.ParseLine([]byte(lineDup))
		if err != nil {
			t.Fatalf("p1.ParseLine lineDup failed: %v", err)
		}
		for _, ev := range evs1Dup {
			if ev.EventType == "turn_end" && ev.Data.Usage != nil {
				t.Fatalf("expected p1 to deduplicate usage on second line with same requestId, but got usage: %+v", ev.Data.Usage)
			}
		}
	})

	t.Run("ClaudeCodeParser deduplicates using message.id when requestId is omitted", func(t *testing.T) {
		p := NewClaudeCodeParser()
		sessID := "stream-session-msgid"

		line1 := `{"type":"assistant","sessionId":"` + sessID + `","apiBlockIndex":0,"message":{"id":"msg_only_1","role":"assistant","model":"claude-3-7-sonnet","stop_reason":"tool_use","content":[{"type":"thinking","thinking":"Processing"}],"usage":{"input_tokens":5,"output_tokens":100}}}`
		line2 := `{"type":"assistant","sessionId":"` + sessID + `","apiBlockIndex":1,"message":{"id":"msg_only_1","role":"assistant","model":"claude-3-7-sonnet","stop_reason":"tool_use","content":[{"type":"tool_use","name":"Bash","id":"call_msg","input":{"command":"pwd"}}],"usage":{"input_tokens":5,"output_tokens":100}}}`

		evs1, err := p.ParseLine([]byte(line1))
		if err != nil {
			t.Fatalf("ParseLine line1 failed: %v", err)
		}
		evs2, err := p.ParseLine([]byte(line2))
		if err != nil {
			t.Fatalf("ParseLine line2 failed: %v", err)
		}

		allEvents := append(evs1, evs2...)
		var turnEndCount int
		for _, ev := range allEvents {
			if ev.EventType == "turn_end" {
				turnEndCount++
			}
		}

		if turnEndCount != 1 {
			t.Fatalf("expected exactly 1 turn_end event for message.id, got %d", turnEndCount)
		}
	})

	t.Run("Multiple distinct request turns each emit their own turn_end", func(t *testing.T) {
		p := NewClaudeCodeParser()
		sessID := "stream-session-multiturn"

		// Turn 1, block 0 & 1
		t1b0 := `{"type":"assistant","requestId":"req_turn_1","sessionId":"` + sessID + `","apiBlockIndex":0,"message":{"id":"msg_1","role":"assistant","model":"claude-3-7-sonnet","stop_reason":"tool_use","content":[{"type":"thinking","thinking":"T1"}],"usage":{"input_tokens":10,"output_tokens":20}}}`
		t1b1 := `{"type":"assistant","requestId":"req_turn_1","sessionId":"` + sessID + `","apiBlockIndex":1,"message":{"id":"msg_1","role":"assistant","model":"claude-3-7-sonnet","stop_reason":"tool_use","content":[{"type":"tool_use","name":"Bash","id":"c1","input":{"command":"echo 1"}}],"usage":{"input_tokens":10,"output_tokens":20}}}`

		// Turn 2, block 0 & 1
		t2b0 := `{"type":"assistant","requestId":"req_turn_2","sessionId":"` + sessID + `","apiBlockIndex":0,"message":{"id":"msg_2","role":"assistant","model":"claude-3-7-sonnet","stop_reason":"tool_use","content":[{"type":"thinking","thinking":"T2"}],"usage":{"input_tokens":15,"output_tokens":30}}}`
		t2b1 := `{"type":"assistant","requestId":"req_turn_2","sessionId":"` + sessID + `","apiBlockIndex":1,"message":{"id":"msg_2","role":"assistant","model":"claude-3-7-sonnet","stop_reason":"tool_use","content":[{"type":"tool_use","name":"Bash","id":"c2","input":{"command":"echo 2"}}],"usage":{"input_tokens":15,"output_tokens":30}}}`

		for _, line := range []string{t1b0, t1b1, t2b0, t2b1} {
			if _, err := p.ParseLine([]byte(line)); err != nil {
				t.Fatalf("ParseLine failed: %v", err)
			}
		}

		// Now reset and check session reset
		p.Reset()

		evsAfterReset, err := p.ParseLine([]byte(t1b1))
		if err != nil {
			t.Fatalf("ParseLine after reset failed: %v", err)
		}
		var turnEndFound bool
		for _, ev := range evsAfterReset {
			if ev.EventType == "turn_end" {
				turnEndFound = true
			}
		}
		if !turnEndFound {
			t.Fatalf("expected turn_end to be re-emitted after parser Reset")
		}
	})

	t.Run("Streamed response with thinking and text preserves response content", func(t *testing.T) {
		p := NewClaudeCodeParser()
		sessID := "stream-session-text"

		line1 := `{"type":"assistant","requestId":"req_text_1","sessionId":"` + sessID + `","apiBlockIndex":0,"message":{"id":"msg_text_1","role":"assistant","model":"claude-3-7-sonnet","stop_reason":"","content":[{"type":"thinking","thinking":"Thinking about solution"}],"usage":{"input_tokens":10,"output_tokens":50}}}`
		line2 := `{"type":"assistant","requestId":"req_text_1","sessionId":"` + sessID + `","apiBlockIndex":1,"message":{"id":"msg_text_1","role":"assistant","model":"claude-3-7-sonnet","stop_reason":"end_turn","content":[{"type":"text","text":"I have completed the task."}],"usage":{"input_tokens":10,"output_tokens":50}}}`

		evs1, err := p.ParseLine([]byte(line1))
		if err != nil {
			t.Fatalf("ParseLine line1 failed: %v", err)
		}
		evs2, err := p.ParseLine([]byte(line2))
		if err != nil {
			t.Fatalf("ParseLine line2 failed: %v", err)
		}

		var nonRunEvents []ParsedEvent
		for _, ev := range evs1 {
			if ev.EventType != "run_started" {
				nonRunEvents = append(nonRunEvents, ev)
			}
		}
		if len(nonRunEvents) != 1 || nonRunEvents[0].EventType != "thinking" {
			t.Fatalf("expected line1 to emit thinking event (excluding run_started), got %v", nonRunEvents)
		}

		var turnEnds []ParsedEvent
		for _, ev := range evs2 {
			if ev.EventType == "turn_end" {
				turnEnds = append(turnEnds, ev)
			}
		}
		if len(turnEnds) != 1 {
			t.Fatalf("expected line2 to emit 1 turn_end event, got %d", len(turnEnds))
		}
		if turnEnds[0].Data.Content == nil || *turnEnds[0].Data.Content != "I have completed the task." {
			t.Errorf("content = %v, want 'I have completed the task.'", turnEnds[0].Data.Content)
		}
		if turnEnds[0].Data.Usage == nil || turnEnds[0].Data.Usage.Output != 50 {
			t.Errorf("usage output = %v, want 50", turnEnds[0].Data.Usage)
		}
	})
}

func TestClaudeCodeParser_StructFieldAcronymCasing(t *testing.T) {
	// Verify rawJSONLine fields conform to Go acronym casing
	rawJSONLineType := reflect.TypeOf(rawJSONLine{})

	requiredJSONLineFields := []struct {
		name    string
		jsonTag string
	}{
		{"RequestID", "requestId,omitempty"},
		{"APIBlockIndex", "apiBlockIndex,omitempty"},
		{"APIErrorStatus", "apiErrorStatus,omitempty"},
		{"IsAPIErrorMessage", "isApiErrorMessage,omitempty"},
	}

	for _, req := range requiredJSONLineFields {
		field, ok := rawJSONLineType.FieldByName(req.name)
		if !ok {
			t.Errorf("rawJSONLine missing field %s conforming to Go acronym convention", req.name)
			continue
		}
		if tag := field.Tag.Get("json"); tag != req.jsonTag {
			t.Errorf("rawJSONLine field %s has json tag %q, want %q", req.name, tag, req.jsonTag)
		}
	}

	bannedJSONLineFields := []string{
		"RequestId",
		"ApiBlockIndex",
		"ApiErrorStatus",
		"IsApiErrorMessage",
	}
	for _, banned := range bannedJSONLineFields {
		if _, ok := rawJSONLineType.FieldByName(banned); ok {
			t.Errorf("rawJSONLine contains mixed-case acronym field %s, should be renamed", banned)
		}
	}

	// Verify rawMessage fields conform to Go acronym casing
	rawMessageType := reflect.TypeOf(rawMessage{})
	field, ok := rawMessageType.FieldByName("APIErrorStatus")
	if !ok {
		t.Errorf("rawMessage missing field APIErrorStatus conforming to Go acronym convention")
	} else if tag := field.Tag.Get("json"); tag != "apiErrorStatus,omitempty" {
		t.Errorf("rawMessage field APIErrorStatus has json tag %q, want %q", tag, "apiErrorStatus,omitempty")
	}

	if _, ok := rawMessageType.FieldByName("ApiErrorStatus"); ok {
		t.Errorf("rawMessage contains mixed-case acronym field ApiErrorStatus, should be renamed")
	}
}

func TestClaudeCodeParser_FIFOEvictionPreservesActiveStreamingRequest(t *testing.T) {
	// In unpatched code, map eviction is random.
	// When capacity is exceeded and eviction triggers, random iteration can evict
	// a recently started streaming request whose remaining blocks have not finished arriving.
	// Under deterministic FIFO eviction, only the oldest keys are evicted, so a recently
	// started request is guaranteed to remain in dedup state.
	failedCount := 0
	trials := 50

	for trial := 0; trial < trials; trial++ {
		p := NewClaudeCodeParser()
		sessID := fmt.Sprintf("session-fifo-%d", trial)

		// 1. Feed 80 distinct requests (160 keys: reqID + msgID)
		for i := 0; i < 80; i++ {
			line := fmt.Sprintf(`{"type":"assistant","requestId":"req_fill_%d","sessionId":"%s","apiBlockIndex":0,"message":{"id":"msg_fill_%d","role":"assistant","model":"claude-3-7-sonnet","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":10,"output_tokens":10}}}`, i, sessID, i)
			if _, err := p.ParseLine([]byte(line)); err != nil {
				t.Fatalf("ParseLine fill %d failed: %v", i, err)
			}
		}

		// 2. Stream block 0 of the active request (tool call, records keys req_active and msg_active)
		activeReqID := fmt.Sprintf("req_active_%d", trial)
		activeMsgID := fmt.Sprintf("msg_active_%d", trial)
		b0 := fmt.Sprintf(`{"type":"assistant","requestId":"%s","sessionId":"%s","apiBlockIndex":0,"message":{"id":"%s","role":"assistant","model":"claude-3-7-sonnet","stop_reason":"tool_use","content":[{"type":"tool_use","name":"Bash","id":"c1","input":{"command":"pwd"}}],"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":5000}}}`, activeReqID, sessID, activeMsgID)
		evs0, err := p.ParseLine([]byte(b0))
		if err != nil {
			t.Fatalf("ParseLine b0 failed: %v", err)
		}

		// 3. Feed 30 more requests (e.g. from subagents/tool turns).
		// This pushes total keys to 160 + 2 + 60 = 222 > 200, triggering eviction of 100 keys.
		// In unpatched code, random eviction can delete activeReqID / activeMsgID.
		for i := 80; i < 110; i++ {
			line := fmt.Sprintf(`{"type":"assistant","requestId":"req_fill_%d","sessionId":"%s","apiBlockIndex":0,"message":{"id":"msg_fill_%d","role":"assistant","model":"claude-3-7-sonnet","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":10,"output_tokens":10}}}`, i, sessID, i)
			if _, err := p.ParseLine([]byte(line)); err != nil {
				t.Fatalf("ParseLine intermediate %d failed: %v", i, err)
			}
		}

		// 4. Stream remaining block 1 of the active request.
		b1 := fmt.Sprintf(`{"type":"assistant","requestId":"%s","sessionId":"%s","apiBlockIndex":1,"message":{"id":"%s","role":"assistant","model":"claude-3-7-sonnet","stop_reason":"tool_use","content":[{"type":"tool_use","name":"Bash","id":"c2","input":{"command":"ls"}}],"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":5000}}}`, activeReqID, sessID, activeMsgID)
		evs1, err := p.ParseLine([]byte(b1))
		if err != nil {
			t.Fatalf("ParseLine b1 failed: %v", err)
		}

		// Count turn_end usage emitted for the active request
		var activeUsageCount int
		for _, ev := range append(evs0, evs1...) {
			if ev.EventType == "turn_end" && ev.Data.Usage != nil && ev.Data.Usage.Output == 50 {
				activeUsageCount++
			}
		}

		if activeUsageCount != 1 {
			failedCount++
		}
	}

	if failedCount > 0 {
		t.Fatalf("active streaming request double-counted usage in %d out of %d trials due to arbitrary map eviction", failedCount, trials)
	}
}

func TestRequestDedup_FIFOOrderAndEviction(t *testing.T) {
	t.Run("nil and empty handling", func(t *testing.T) {
		var nilDedup *requestDedup
		if nilDedup.has("any") {
			t.Error("nil requestDedup should report false for has")
		}
		nilDedup.record("any")             // must not panic
		recordSeenRequest(nilDedup, "any") // must not panic

		d := newRequestDedup()
		if d.has("nonexistent") {
			t.Error("new requestDedup should report false for nonexistent key")
		}
	})

	t.Run("records up to maxSeenRequests without evicting", func(t *testing.T) {
		d := newRequestDedup()
		for i := 0; i < maxSeenRequests; i++ {
			k := fmt.Sprintf("key_%d", i)
			d.record(k)
		}
		if len(d.order) != maxSeenRequests {
			t.Fatalf("expected order length %d, got %d", maxSeenRequests, len(d.order))
		}
		for i := 0; i < maxSeenRequests; i++ {
			k := fmt.Sprintf("key_%d", i)
			if !d.has(k) {
				t.Fatalf("expected key %s to be present", k)
			}
		}
	})

	t.Run("evicts oldest evictBatchSize entries in strict FIFO order", func(t *testing.T) {
		d := newRequestDedup()
		for i := 0; i < maxSeenRequests; i++ {
			d.record(fmt.Sprintf("key_%d", i))
		}

		// Insert one more key to trigger batch eviction
		d.record("key_overflow")

		// Keys 0..evictBatchSize-1 must be evicted
		for i := 0; i < evictBatchSize; i++ {
			k := fmt.Sprintf("key_%d", i)
			if d.has(k) {
				t.Errorf("expected key %s (oldest) to be evicted, but it is still present", k)
			}
		}

		// Keys evictBatchSize..maxSeenRequests-1 must still be present
		for i := evictBatchSize; i < maxSeenRequests; i++ {
			k := fmt.Sprintf("key_%d", i)
			if !d.has(k) {
				t.Errorf("expected key %s (newer) to still be present, but was evicted", k)
			}
		}

		// The newly added key must be present
		if !d.has("key_overflow") {
			t.Errorf("expected key_overflow to be present")
		}

		// Total order length must be (maxSeenRequests - evictBatchSize + 1)
		expectedLen := maxSeenRequests - evictBatchSize + 1
		if len(d.order) != expectedLen {
			t.Errorf("expected order length %d, got %d", expectedLen, len(d.order))
		}
	})

	t.Run("duplicate record calls are idempotent and do not duplicate in order", func(t *testing.T) {
		d := newRequestDedup()
		d.record("key_1")
		d.record("key_2")
		d.record("key_1") // duplicate

		if len(d.order) != 2 {
			t.Fatalf("expected order length 2, got %d", len(d.order))
		}
		if d.order[0] != "key_1" || d.order[1] != "key_2" {
			t.Fatalf("expected order [key_1, key_2], got %v", d.order)
		}
	})
}

func TestClaudeCodeParser_ThinkingOnlyTerminalStopReasons(t *testing.T) {
	cases := []struct {
		name       string
		stopReason string
	}{
		{"max_tokens truncation during thinking", "max_tokens"},
		{"refusal during thinking", "refusal"},
		{"end_turn when only thinking generated", "end_turn"},
		{"stop_sequence during thinking", "stop_sequence"},
		{"toolUse non-Claude stop reason during thinking", "toolUse"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := NewClaudeCodeParser()
			line := fmt.Sprintf(`{"type":"assistant","requestId":"req_think_%s","sessionId":"sess_think","apiBlockIndex":0,"message":{"id":"msg_think_%s","role":"assistant","model":"claude-3-7-sonnet","stop_reason":"%s","content":[{"type":"thinking","thinking":"Thinking about problem"}],"usage":{"input_tokens":15,"output_tokens":120,"cache_read_input_tokens":500}}}`, tc.stopReason, tc.stopReason, tc.stopReason)

			events, err := p.ParseLine([]byte(line))
			if err != nil {
				t.Fatalf("ParseLine failed: %v", err)
			}

			var thinkingCount, turnEndCount int
			var turnEnd *ParsedEvent
			for i := range events {
				switch events[i].EventType {
				case "thinking":
					thinkingCount++
				case "turn_end":
					turnEndCount++
					turnEnd = &events[i]
				}
			}

			if thinkingCount != 1 {
				t.Errorf("expected 1 thinking event, got %d", thinkingCount)
			}
			if turnEndCount != 1 {
				t.Fatalf("expected exactly 1 turn_end event for terminal thinking block with stop_reason %q, got %d", tc.stopReason, turnEndCount)
			}
			if turnEnd == nil || turnEnd.Data.Usage == nil {
				t.Fatalf("expected turn_end with usage, got nil")
			}
			if turnEnd.Data.Usage.Output != 120 {
				t.Errorf("expected output tokens 120, got %d", turnEnd.Data.Usage.Output)
			}
			if turnEnd.Data.Usage.Input != 15 {
				t.Errorf("expected input tokens 15, got %d", turnEnd.Data.Usage.Input)
			}
			if turnEnd.Data.Usage.Total != 635 {
				t.Errorf("expected total tokens 635, got %d", turnEnd.Data.Usage.Total)
			}
			if turnEnd.Data.StopReason == nil || *turnEnd.Data.StopReason != tc.stopReason {
				t.Errorf("expected stop_reason %q, got %v", tc.stopReason, turnEnd.Data.StopReason)
			}
		})
	}

	t.Run("multi-block streaming with intermediate tool_use thinking block", func(t *testing.T) {
		p := NewClaudeCodeParser()
		sessID := "sess_stream_tool"
		line0 := `{"type":"assistant","requestId":"req_stream_tool","sessionId":"` + sessID + `","apiBlockIndex":0,"message":{"id":"msg_stream_tool","role":"assistant","model":"claude-3-7-sonnet","stop_reason":"tool_use","content":[{"type":"thinking","thinking":"Deciding tool call"}],"usage":{"input_tokens":10,"output_tokens":80}}}`
		line1 := `{"type":"assistant","requestId":"req_stream_tool","sessionId":"` + sessID + `","apiBlockIndex":1,"message":{"id":"msg_stream_tool","role":"assistant","model":"claude-3-7-sonnet","stop_reason":"tool_use","content":[{"type":"tool_use","name":"Bash","id":"call_123","input":{"command":"echo test"}}],"usage":{"input_tokens":10,"output_tokens":80}}}`

		evs0, err := p.ParseLine([]byte(line0))
		if err != nil {
			t.Fatalf("ParseLine line0 failed: %v", err)
		}
		var turnEnds0 int
		for _, ev := range evs0 {
			if ev.EventType == "turn_end" {
				turnEnds0++
			}
		}
		if turnEnds0 != 0 {
			t.Fatalf("expected block 0 with stop_reason 'tool_use' to be intermediate (0 turn_end events), got %d", turnEnds0)
		}

		evs1, err := p.ParseLine([]byte(line1))
		if err != nil {
			t.Fatalf("ParseLine line1 failed: %v", err)
		}
		var turnEnds1 int
		var turnEnd1 *ParsedEvent
		for i := range evs1 {
			if evs1[i].EventType == "turn_end" {
				turnEnds1++
				turnEnd1 = &evs1[i]
			}
		}
		if turnEnds1 != 1 {
			t.Fatalf("expected block 1 to emit exactly 1 turn_end event, got %d", turnEnds1)
		}
		if turnEnd1 == nil || turnEnd1.Data.Usage == nil || turnEnd1.Data.Usage.Output != 80 {
			t.Errorf("expected block 1 turn_end to carry token usage (output=80), got %v", turnEnd1)
		}
	})
}

func TestClaudeCodeParser_DeliveryEvidence(t *testing.T) {
	const session = `"sessionId":"s","timestamp":"2026-10-25T14:49:47.482Z"`
	tests := []struct {
		name       string
		line       string
		eventType  string
		wantUUID   string
		wantMerged []string
		wantText   string
		wantNone   bool
	}{
		{
			name:      "normal turn",
			line:      `{"type":"user","uuid":"sent",` + session + `,"message":{"role":"user","content":"hello"}}`,
			eventType: "turn_start",
			wantUUID:  "sent",
			wantText:  "hello",
		},
		{
			name:       "merged queued messages",
			line:       `{"type":"user","uuid":"last","mergedSendUuids":["first","second"],` + session + `,"message":{"role":"user","content":"a\nb\nc"}}`,
			eventType:  "turn_start",
			wantUUID:   "last",
			wantMerged: []string{"first", "second"},
			wantText:   "a\nb\nc",
		},
		{
			name:      "absorbed mid-turn",
			line:      `{"type":"queue-operation","operation":"remove","reason":"absorbed_mid_turn","commandUuid":"absorbed","deliveryId":"d",` + session + `,"content":"git pull"}`,
			eventType: "message_absorbed",
			wantUUID:  "absorbed",
			wantText:  "git pull",
		},
		{
			name:     "delivered without a UUID",
			line:     `{"type":"queue-operation","operation":"remove","reason":"delivered_to_agent",` + session + `,"content":"x"}`,
			wantNone: true,
		},
		{
			name:     "enqueue proves nothing",
			line:     `{"type":"queue-operation","operation":"enqueue",` + session + `,"content":"x"}`,
			wantNone: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events, err := NewClaudeCodeParser().ParseLine([]byte(tt.line))
			if err != nil {
				t.Fatal(err)
			}
			var found *ParsedEvent
			for i := range events {
				if events[i].Data.MessageUUID != nil {
					found = &events[i]
				}
			}
			if tt.wantNone {
				if found != nil {
					t.Fatalf("unexpected evidence: %+v", found)
				}
				return
			}
			if found == nil {
				t.Fatalf("no evidence in %+v", events)
			}
			if found.EventType != tt.eventType || *found.Data.MessageUUID != tt.wantUUID {
				t.Fatalf("event = %s %s", found.EventType, *found.Data.MessageUUID)
			}
			if !reflect.DeepEqual(found.Data.MergedMessageUUIDs, tt.wantMerged) {
				t.Fatalf("merged = %v, want %v", found.Data.MergedMessageUUIDs, tt.wantMerged)
			}
			if found.Data.Content == nil || *found.Data.Content != tt.wantText {
				t.Fatalf("content = %v", found.Data.Content)
			}
		})
	}
}

func TestClaudeCodeParser_MissingUUIDStaysUnset(t *testing.T) {
	events, err := NewClaudeCodeParser().ParseLine([]byte(`{"type":"user","message":{"role":"user","content":"hello"}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.EventType == "turn_start" && event.Data.MessageUUID != nil {
			t.Fatalf("message UUID = %q, want unset", *event.Data.MessageUUID)
		}
	}
}
