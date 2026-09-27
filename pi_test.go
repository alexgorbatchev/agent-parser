package parser

import (
	"testing"
)

func TestExtractPiTitleFromPrompt(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantTitle   string
		wantHasTile bool
	}{
		{
			name:        "empty string",
			input:       "",
			wantTitle:   "",
			wantHasTile: false,
		},
		{
			name:        "whitespace only",
			input:       "   \n\t  \n  ",
			wantTitle:   "",
			wantHasTile: false,
		},
		{
			name:        "single line prompt",
			input:       "Fix the bug in auth handler",
			wantTitle:   "Fix the bug in auth handler",
			wantHasTile: true,
		},
		{
			name:        "multiline prompt takes first non-empty line",
			input:       "\n\nImplement streaming telemetry pipeline\nWith 90% test coverage and full error handling.",
			wantTitle:   "Implement streaming telemetry pipeline",
			wantHasTile: true,
		},
		{
			name:        "prompt with leading and trailing spaces on first line",
			input:       "   Add dark mode toggle to navigation drawer   \nDetails follow...",
			wantTitle:   "Add dark mode toggle to navigation drawer",
			wantHasTile: true,
		},
		{
			name:        "long prompt exceeding 100 characters is truncated to 100",
			input:       "This is an extremely long user task prompt that goes on and on beyond one hundred characters in total length to verify that truncation works properly",
			wantTitle:   "This is an extremely long user task prompt that goes on and on beyond one hundred characters in tota",
			wantHasTile: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, hasTitle := ExtractPiTitleFromPrompt(tt.input)
			if hasTitle != tt.wantHasTile {
				t.Errorf("ExtractPiTitleFromPrompt() hasTitle = %v, want %v", hasTitle, tt.wantHasTile)
			}
			if title != tt.wantTitle {
				t.Errorf("ExtractPiTitleFromPrompt() title = %q, want %q", title, tt.wantTitle)
			}
		})
	}
}

func TestParsePiContentBlocks(t *testing.T) {
	tests := []struct {
		name       string
		input      interface{}
		wantBlocks int
	}{
		{"nil", nil, 0},
		{"string", "just text", 1},
		{
			"array of maps",
			[]interface{}{
				map[string]interface{}{"type": "text", "text": "block 1"},
				map[string]interface{}{"type": "thinking", "thinking": "block 2"},
			},
			2,
		},
		{
			"single map",
			map[string]interface{}{"type": "toolCall", "toolName": "read_file"},
			1,
		},
		{"unsupported int", 12345, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parsePiContentBlocks(tt.input)
			if len(got) != tt.wantBlocks {
				t.Fatalf("parsePiContentBlocks() returned %d blocks, want %d", len(got), tt.wantBlocks)
			}
		})
	}
}

func TestParsePiLine(t *testing.T) {
	t.Run("empty line", func(t *testing.T) {
		evs, err := ParsePiLine([]byte(""), "/fallback")
		if err != nil || len(evs) != 0 {
			t.Errorf("expected nil, nil for empty line, got %v, %v", evs, err)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		_, err := ParsePiLine([]byte("{invalid"), "/fallback")
		if err == nil {
			t.Errorf("expected error for invalid json")
		}
	})

	t.Run("session header with fallback CWD", func(t *testing.T) {
		line := []byte(`{"type":"session","version":3,"id":"01a0b64b-test","timestamp":"2026-09-18T20:53:30.007Z"}`)
		evs, err := ParsePiLine(line, "/fallback/dir")
		if err != nil {
			t.Fatalf("ParsePiLine error: %v", err)
		}
		if len(evs) != 1 || evs[0].EventType != "run_started" || evs[0].CWD != "/fallback/dir" {
			t.Errorf("unexpected session event: %+v", evs)
		}
	})

	t.Run("label and session_info", func(t *testing.T) {
		lineLabel := []byte(`{"type":"label","label":"Bookmark Step 1","timestamp":"2026-09-18T20:53:30.000Z","targetId":"entry-1","cwd":"/cwd"}`)
		evsLabel, err := ParsePiLine(lineLabel, "")
		if err != nil || len(evsLabel) != 1 || *evsLabel[0].Data.Title != "Bookmark Step 1" {
			t.Errorf("label event with label field mismatch: %+v", evsLabel)
		}

		line1 := []byte(`{"type":"label","title":"My Session Title","timestamp":"2026-09-18T20:53:30.000Z","cwd":"/cwd"}`)
		evs1, err := ParsePiLine(line1, "")
		if err != nil || len(evs1) != 1 || *evs1[0].Data.Title != "My Session Title" {
			t.Errorf("label event mismatch: %+v", evs1)
		}

		line2 := []byte(`{"type":"session_info","name":"Named Session","timestamp":"2026-09-18T20:53:30.000Z"}`)
		evs2, err := ParsePiLine(line2, "/cwd")
		if err != nil || len(evs2) != 1 || *evs2[0].Data.Title != "Named Session" {
			t.Errorf("session_info event mismatch: %+v", evs2)
		}
	})

	t.Run("model_change", func(t *testing.T) {
		lineModelId := []byte(`{"type":"model_change","provider":"anthropic","modelId":"claude-3-7-sonnet","timestamp":"2026-09-18T20:53:30.000Z"}`)
		evsModelId, err := ParsePiLine(lineModelId, "/cwd")
		if err != nil || len(evsModelId) != 1 || evsModelId[0].EventType != "model_change" || *evsModelId[0].Data.Model != "claude-3-7-sonnet" {
			t.Errorf("model_change with modelId mismatch: %+v", evsModelId)
		}

		lineModel_Id := []byte(`{"type":"model_change","model_id":"gpt-4o","timestamp":"2026-09-18T20:53:30.000Z"}`)
		evsModel_Id, err := ParsePiLine(lineModel_Id, "/cwd")
		if err != nil || len(evsModel_Id) != 1 || evsModel_Id[0].EventType != "model_change" || *evsModel_Id[0].Data.Model != "gpt-4o" {
			t.Errorf("model_change with model_id mismatch: %+v", evsModel_Id)
		}

		line := []byte(`{"type":"model_change","model":"gemini-3.7-flash","timestamp":"2026-09-18T20:53:30.000Z"}`)
		evs, err := ParsePiLine(line, "/cwd")
		if err != nil || len(evs) != 1 || evs[0].EventType != "model_change" || *evs[0].Data.Model != "gemini-3.7-flash" {
			t.Errorf("model_change event mismatch: %+v", evs)
		}
	})

	t.Run("message with nil message field", func(t *testing.T) {
		line := []byte(`{"type":"message","timestamp":"2026-09-18T20:53:30.000Z"}`)
		evs, err := ParsePiLine(line, "/cwd")
		if err != nil || len(evs) != 0 {
			t.Errorf("expected empty events for nil message, got %v", evs)
		}
	})

	t.Run("user message with string content", func(t *testing.T) {
		line := []byte(`{"type":"message","message":{"role":"user","content":"Simple text prompt"}}`)
		evs, err := ParsePiLine(line, "/cwd")
		if err != nil || len(evs) != 1 || *evs[0].Data.LastPrompt != "Simple text prompt" {
			t.Errorf("user string content mismatch: %+v", evs)
		}
		if evs[0].Data.Title == nil || *evs[0].Data.Title != "Simple text prompt" {
			t.Errorf("user title mismatch: got %v, want 'Simple text prompt'", evs[0].Data.Title)
		}
	})

	t.Run("user message with multiline content blocks", func(t *testing.T) {
		line := []byte(`{
			"type": "message",
			"message": {
				"role": "user",
				"content": [
					{"type": "text", "text": "Fix database deadlock\nDetails about lock order"}
				]
			}
		}`)
		evs, err := ParsePiLine(line, "/cwd")
		if err != nil || len(evs) != 1 {
			t.Fatalf("ParsePiLine failed: %v", err)
		}
		if evs[0].Data.Title == nil || *evs[0].Data.Title != "Fix database deadlock" {
			t.Errorf("user title mismatch: got %v, want 'Fix database deadlock'", evs[0].Data.Title)
		}
	})

	t.Run("toolResult with isError false and true", func(t *testing.T) {
		lineFalse := []byte(`{"type":"message","message":{"role":"toolResult","toolCallId":"call-1","toolName":"read_file","content":"ok","isError":false}}`)
		evsFalse, err := ParsePiLine(lineFalse, "/cwd")
		if err != nil || len(evsFalse) != 1 || *evsFalse[0].Data.IsError != false || *evsFalse[0].Data.ToolCallID != "call-1" {
			t.Errorf("toolResult false mismatch: %+v", evsFalse)
		}

		lineTrue := []byte(`{"type":"message","message":{"role":"toolResult","toolCallId":"call-2","toolName":"read_file","content":"err","isError":true}}`)
		evsTrue, err := ParsePiLine(lineTrue, "/cwd")
		if err != nil || len(evsTrue) != 1 || *evsTrue[0].Data.IsError != true {
			t.Errorf("toolResult true mismatch: %+v", evsTrue)
		}
	})

	t.Run("assistant message with cost breakdown object", func(t *testing.T) {
		line := []byte(`{
			"type": "message",
			"message": {
				"role": "assistant",
				"model": "claude-sonnet-4-5",
				"content": [{"type": "text", "text": "Task finished"}],
				"usage": {
					"input": 100,
					"output": 50,
					"cost": {
						"input": 0.001,
						"output": 0.002,
						"total": 0.003
					}
				},
				"stopReason": "stop"
			}
		}`)
		evs, err := ParsePiLine(line, "/cwd")
		if err != nil || len(evs) != 1 {
			t.Fatalf("unexpected events: %v, %v", evs, err)
		}
		if evs[0].Data.Usage == nil || evs[0].Data.Usage.Input != 100 || evs[0].Data.Usage.Output != 50 {
			t.Errorf("expected usage 100/50, got %v", evs[0].Data.Usage)
		}
	})

	t.Run("assistant message with numeric cost", func(t *testing.T) {
		line := []byte(`{
			"type": "message",
			"message": {
				"role": "assistant",
				"model": "claude-sonnet-4-5",
				"usage": {
					"input": 200,
					"output": 100,
					"cost": 0.015
				},
				"stopReason": "stop"
			}
		}`)
		evs, err := ParsePiLine(line, "/cwd")
		if err != nil || len(evs) != 1 {
			t.Fatalf("unexpected events: %v, %v", evs, err)
		}
		if evs[0].Data.Usage == nil || evs[0].Data.Usage.Input != 200 {
			t.Errorf("expected usage input 200, got %v", evs[0].Data.Usage)
		}
	})

	t.Run("assistant thinking via text fallback, toolCall with arguments, usage total computed", func(t *testing.T) {
		line := []byte(`{
			"type": "message",
			"message": {
				"role": "assistant",
				"model": "claude-3-7-sonnet",
				"stopReason": "stop",
				"content": [
					{"type": "thinking", "text": "Thought via text field"},
					{"type": "toolCall", "name": "custom_tool", "id": "t-1", "arguments": {"foo": "bar"}}
				],
				"usage": {
					"input": 50,
					"output": 25,
					"cacheRead": 10,
					"cacheWrite": 5,
					"reasoning": 15,
					"total": 0,
					"cost": 0.005
				}
			}
		}`)
		evs, err := ParsePiLine(line, "/cwd")
		if err != nil {
			t.Fatal(err)
		}
		if len(evs) != 3 {
			t.Fatalf("expected 3 events, got %d", len(evs))
		}

		// Thinking
		if evs[0].EventType != "thinking" || *evs[0].Data.Content != "Thought via text field" {
			t.Errorf("thinking mismatch: %+v", evs[0])
		}

		// Tool call
		if evs[1].EventType != "tool_call" || *evs[1].Data.ToolName != "custom_tool" || *evs[1].Data.ToolCallID != "t-1" {
			t.Errorf("toolCall mismatch: %+v", evs[1])
		}

		// Turn end with computed total
		u := evs[2].Data.Usage
		if u == nil {
			t.Fatalf("usage is nil")
		}
		if u.Total != 105 {
			t.Errorf("u.Total = %d, want 105", u.Total)
		}
		if evs[2].Data.RawHarnessCost == nil || *evs[2].Data.RawHarnessCost != 0.005 {
			t.Errorf("cost mismatch: %v", evs[2].Data.RawHarnessCost)
		}
	})

	t.Run("assistant message with object-shaped cost and distinct thinking vs text blocks", func(t *testing.T) {
		line := []byte(`{
			"type": "message",
			"message": {
				"role": "assistant",
				"model": "claude-3-7-sonnet",
				"stopReason": "stop",
				"content": [
					{"type": "thinking", "thinking": "Internal chain of thought reasoning"},
					{"type": "text", "text": "Conversational assistant response to user"}
				],
				"usage": {
					"input": 100,
					"output": 50,
					"cacheRead": 20,
					"cacheWrite": 10,
					"reasoning": 30,
					"total": 180,
					"cost": {
						"input": 0.001,
						"output": 0.003,
						"cacheRead": 0.0002,
						"cacheWrite": 0.0001,
						"total": 0.0043
					}
				}
			}
		}`)
		evs, err := ParsePiLine(line, "/cwd")
		if err != nil {
			t.Fatalf("ParsePiLine error: %v", err)
		}
		// Expect exactly 2 events: 1 thinking event, 1 turn_end event (no thinking event for the text block!)
		if len(evs) != 2 {
			t.Fatalf("expected 2 events, got %d", len(evs))
		}

		// Event 0: thinking
		if evs[0].EventType != "thinking" {
			t.Errorf("expected EventType thinking, got %s", evs[0].EventType)
		}
		if *evs[0].Data.Content != "Internal chain of thought reasoning" {
			t.Errorf("thinking content mismatch: %q", *evs[0].Data.Content)
		}

		// Event 1: turn_end
		if evs[1].EventType != "turn_end" {
			t.Errorf("expected EventType turn_end, got %s", evs[1].EventType)
		}
		if evs[1].Data.Content == nil || *evs[1].Data.Content != "Conversational assistant response to user" {
			t.Errorf("turn_end content mismatch: %v", evs[1].Data.Content)
		}
		if evs[1].Data.Usage == nil || evs[1].Data.Usage.Total != 180 {
			t.Errorf("usage total mismatch: %+v", evs[1].Data.Usage)
		}
		if evs[1].Data.RawHarnessCost == nil || *evs[1].Data.RawHarnessCost != 0.0043 {
			t.Errorf("cost breakdown total mismatch: %v", evs[1].Data.RawHarnessCost)
		}
	})

	t.Run("assistant message with string content", func(t *testing.T) {
		line := []byte(`{
			"type": "message",
			"message": {
				"role": "assistant",
				"model": "claude-3-7-sonnet",
				"content": "Just a plain string response",
				"stopReason": "stop"
			}
		}`)
		evs, err := ParsePiLine(line, "/cwd")
		if err != nil {
			t.Fatalf("ParsePiLine error: %v", err)
		}
		// Should only produce turn_end, no thinking event
		if len(evs) != 1 {
			t.Fatalf("expected 1 event, got %d", len(evs))
		}
		if evs[0].EventType != "turn_end" {
			t.Errorf("expected EventType turn_end, got %s", evs[0].EventType)
		}
		if evs[0].Data.Content == nil || *evs[0].Data.Content != "Just a plain string response" {
			t.Errorf("content mismatch: %v", evs[0].Data.Content)
		}
	})

	t.Run("message with nil message payload or system role", func(t *testing.T) {
		lineNil := []byte(`{"type":"message"}`)
		evsNil, err := ParsePiLine(lineNil, "/cwd")
		if err != nil || len(evsNil) != 0 {
			t.Errorf("expected empty events for nil message payload, got %v, %v", evsNil, err)
		}

		lineSys := []byte(`{"type":"message","message":{"role":"system","content":"system prompt"}}`)
		evsSys, err := ParsePiLine(lineSys, "/cwd")
		if err != nil || len(evsSys) != 0 {
			t.Errorf("expected empty events for system role, got %v, %v", evsSys, err)
		}
	})

	t.Run("assistant AskUserQuestion tool names and prompt parameter", func(t *testing.T) {
		toolNames := []string{"AskUserQuestion", "ask_user", "ask_question"}
		for _, name := range toolNames {
			line := []byte(`{
				"type": "message",
				"message": {
					"role": "assistant",
					"content": [
						{"type": "toolCall", "toolName": "` + name + `", "id": "q-1", "parameters": {"prompt": "Confirm action?"}}
					]
				}
			}`)
			evs, err := ParsePiLine(line, "/cwd")
			if err != nil {
				t.Fatalf("name %s: error %v", name, err)
			}
			if len(evs) != 2 || evs[0].EventType != "question_prompt" || *evs[0].Data.Question != "Confirm action?" {
				t.Errorf("name %s: question_prompt mismatch: %+v", name, evs)
			}
		}
	})

	t.Run("message with snake_case tool_result role and alias fields", func(t *testing.T) {
		line := []byte(`{
			"type": "message",
			"message": {
				"role": "tool_result",
				"tool_name": "bash",
				"tool_call_id": "call-snake-1",
				"content": "output text",
				"is_error": true
			}
		}`)
		evs, err := ParsePiLine(line, "/cwd")
		if err != nil {
			t.Fatalf("ParsePiLine error: %v", err)
		}
		if len(evs) != 1 {
			t.Fatalf("expected 1 event, got %d", len(evs))
		}
		if evs[0].EventType != "tool_result" {
			t.Errorf("EventType = %q, want 'tool_result'", evs[0].EventType)
		}
		if evs[0].Data.ToolName == nil || *evs[0].Data.ToolName != "bash" {
			t.Errorf("ToolName = %v, want 'bash'", evs[0].Data.ToolName)
		}
		if evs[0].Data.ToolCallID == nil || *evs[0].Data.ToolCallID != "call-snake-1" {
			t.Errorf("ToolCallID = %v, want 'call-snake-1'", evs[0].Data.ToolCallID)
		}
		if evs[0].Data.IsError == nil || *evs[0].Data.IsError != true {
			t.Errorf("IsError = %v, want true", evs[0].Data.IsError)
		}
	})

	t.Run("assistant content block with snake_case tool_call type and alias fields", func(t *testing.T) {
		line := []byte(`{
			"type": "message",
			"message": {
				"role": "assistant",
				"content": [
					{
						"type": "tool_call",
						"tool_name": "read",
						"tool_call_id": "call-snake-2",
						"arguments": {"file_path": "main.go"}
					}
				]
			}
		}`)
		evs, err := ParsePiLine(line, "/cwd")
		if err != nil {
			t.Fatalf("ParsePiLine error: %v", err)
		}
		if len(evs) != 2 {
			t.Fatalf("expected 2 events (tool_call, turn_end), got %d", len(evs))
		}
		if evs[0].EventType != "tool_call" {
			t.Errorf("EventType = %q, want 'tool_call'", evs[0].EventType)
		}
		if evs[0].Data.ToolName == nil || *evs[0].Data.ToolName != "read" {
			t.Errorf("ToolName = %v, want 'read'", evs[0].Data.ToolName)
		}
		if evs[0].Data.ToolCallID == nil || *evs[0].Data.ToolCallID != "call-snake-2" {
			t.Errorf("ToolCallID = %v, want 'call-snake-2'", evs[0].Data.ToolCallID)
		}
		if evs[1].EventType != "turn_end" {
			t.Errorf("EventType = %q, want 'turn_end'", evs[1].EventType)
		}
	})

	t.Run("assistant content block with omitted tool call ID produces nil ToolCallID", func(t *testing.T) {
		line := []byte(`{
			"type": "message",
			"message": {
				"role": "assistant",
				"content": [
					{
						"type": "tool_call",
						"name": "bash",
						"arguments": {"command": "ls"}
					}
				]
			}
		}`)
		evs, err := ParsePiLine(line, "/cwd")
		if err != nil {
			t.Fatalf("ParsePiLine error: %v", err)
		}
		if len(evs) != 2 || evs[0].EventType != "tool_call" {
			t.Fatalf("expected tool_call event, got %+v", evs)
		}
		if evs[0].Data.ToolCallID != nil {
			t.Errorf("expected ToolCallID=nil, got %v", *evs[0].Data.ToolCallID)
		}
	})
}

func TestPiParser_NormalizedFileModification(t *testing.T) {
	t.Run("edit toolCall produces normalized FileModification", func(t *testing.T) {
		line := []byte(`{
			"type": "message",
			"message": {
				"role": "assistant",
				"content": [
					{
						"type": "toolCall",
						"name": "edit",
						"id": "pi-edit-1",
						"arguments": {
							"path": "src/index.ts",
							"edits": [
								{"oldText": "const x = 1;", "newText": "const x = 2;"}
							]
						}
					}
				]
			}
		}`)
		evs, err := ParsePiLine(line, "/workspace")
		if err != nil {
			t.Fatalf("ParsePiLine failed: %v", err)
		}
		if len(evs) == 0 {
			t.Fatal("expected events")
		}
		fm := evs[0].Data.FileModification
		if fm == nil {
			t.Fatal("expected non-nil FileModification for Pi edit tool")
		}
		if fm.Action != "EDIT" {
			t.Errorf("Action = %q, want 'EDIT'", fm.Action)
		}
		if fm.FilePath != "src/index.ts" {
			t.Errorf("FilePath = %q, want 'src/index.ts'", fm.FilePath)
		}
		if len(fm.Hunks) != 1 {
			t.Fatalf("expected 1 hunk, got %d", len(fm.Hunks))
		}
	})

	t.Run("write toolCall produces normalized FileModification", func(t *testing.T) {
		line := []byte(`{
			"type": "message",
			"message": {
				"role": "assistant",
				"content": [
					{
						"type": "toolCall",
						"name": "write",
						"id": "pi-write-1",
						"arguments": {
							"path": "src/types.ts",
							"content": "export type Foo = string;\n"
						}
					}
				]
			}
		}`)
		evs, err := ParsePiLine(line, "/workspace")
		if err != nil {
			t.Fatalf("ParsePiLine failed: %v", err)
		}
		if len(evs) == 0 {
			t.Fatal("expected events")
		}
		fm := evs[0].Data.FileModification
		if fm == nil {
			t.Fatal("expected non-nil FileModification for Pi write tool")
		}
		if fm.Action != "WRITE" {
			t.Errorf("Action = %q, want 'WRITE'", fm.Action)
		}
		if fm.FilePath != "src/types.ts" {
			t.Errorf("FilePath = %q, want 'src/types.ts'", fm.FilePath)
		}
		if fm.Content == nil || *fm.Content != "export type Foo = string;\n" {
			t.Errorf("Content = %v, want 'export type Foo = string;\\n'", fm.Content)
		}
	})
}

func TestParsePiError(t *testing.T) {
	tests := []struct {
		name          string
		line          string
		wantError     bool
		wantContent   string
		wantTurnEnd   bool
	}{
		{
			name: "assistant message with stopReason == 'error' and errorMessage",
			line: `{
				"type": "message",
				"id": "err-1",
				"timestamp": "2026-09-18T20:55:00.000Z",
				"cwd": "/workspace",
				"message": {
					"role": "assistant",
					"model": "claude-sonnet-4-5",
					"stopReason": "error",
					"errorMessage": "Rate limit exceeded: 429",
					"content": []
				}
			}`,
			wantError:   true,
			wantContent: "Rate limit exceeded: 429",
			wantTurnEnd: true,
		},
		{
			name: "assistant message with stopReason == 'error' fallback to text content",
			line: `{
				"type": "message",
				"id": "err-2",
				"message": {
					"role": "assistant",
					"stopReason": "error",
					"content": [{"type": "text", "text": "An internal error occurred"}]
				}
			}`,
			wantError:   true,
			wantContent: "An internal error occurred",
			wantTurnEnd: true,
		},
		{
			name: "assistant message with stopReason == 'error' and no message or content defaults to 'error'",
			line: `{
				"type": "message",
				"id": "err-3",
				"message": {
					"role": "assistant",
					"stopReason": "error",
					"content": []
				}
			}`,
			wantError:   true,
			wantContent: "error",
			wantTurnEnd: true,
		},
		{
			name: "normal assistant message without error does not emit error event",
			line: `{
				"type": "message",
				"id": "ok-1",
				"message": {
					"role": "assistant",
					"stopReason": "stop",
					"content": [{"type": "text", "text": "All good"}]
				}
			}`,
			wantError:   false,
			wantContent: "",
			wantTurnEnd: true,
		},
		{
			name: "assistant message with stopReason == 'aborted' and errorMessage does not emit error event",
			line: `{
				"type": "message",
				"id": "abort-1",
				"message": {
					"role": "assistant",
					"stopReason": "aborted",
					"errorMessage": "Operation aborted by user",
					"content": []
				}
			}`,
			wantError:   false,
			wantContent: "",
			wantTurnEnd: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evs, err := ParsePiLine([]byte(tt.line), "/test/cwd")
			if err != nil {
				t.Fatalf("ParsePiLine failed: %v", err)
			}

			var errorEv *ParsedEvent
			var hasTurnEnd bool
			for i := range evs {
				if evs[i].EventType == "error" {
					errorEv = &evs[i]
				}
				if evs[i].EventType == "turn_end" {
					hasTurnEnd = true
				}
			}

			if tt.wantTurnEnd != hasTurnEnd {
				t.Errorf("hasTurnEnd = %v, want %v", hasTurnEnd, tt.wantTurnEnd)
			}

			if !tt.wantError {
				if errorEv != nil {
					t.Errorf("expected no error event, got %+v", errorEv)
				}
				return
			}

			if errorEv == nil {
				t.Fatalf("expected error event, but none was emitted. Events: %+v", evs)
			}

			if errorEv.EventType != "error" {
				t.Errorf("EventType = %q, want 'error'", errorEv.EventType)
			}
			if errorEv.Data.IsError == nil || !*errorEv.Data.IsError {
				t.Errorf("Data.IsError = %v, want true", errorEv.Data.IsError)
			}
			if errorEv.Data.Content == nil || *errorEv.Data.Content != tt.wantContent {
				got := "<nil>"
				if errorEv.Data.Content != nil {
					got = *errorEv.Data.Content
				}
				t.Errorf("Data.Content = %q, want %q", got, tt.wantContent)
			}
			if errorEv.Data.Role == nil || *errorEv.Data.Role != "assistant" {
				t.Errorf("Data.Role = %v, want 'assistant'", errorEv.Data.Role)
			}

			// When both turn_end and error are emitted, error must come AFTER turn_end
			// so the ingester classifier sets activity to ActivityError rather than ActivityIdle.
			if tt.wantTurnEnd {
				turnEndIdx := -1
				errorIdx := -1
				for i, ev := range evs {
					switch ev.EventType {
					case "turn_end":
						turnEndIdx = i
					case "error":
						errorIdx = i
					}
				}
				if errorIdx < turnEndIdx {
					t.Errorf("error event index (%d) must follow turn_end index (%d)", errorIdx, turnEndIdx)
				}
			}
		})
	}
}

