package parser

import (
	"fmt"
	"testing"
)

// TestCodexToolResultStatus covers each status header Codex writes into tool
// outputs. Bodies follow the formats in codex-rs core/src/tools; their content
// is synthetic.
func TestCodexToolResultStatus(t *testing.T) {
	failed, succeeded := boolPtr(true), boolPtr(false)
	codeMode := func(status string) string {
		return fmt.Sprintf(`[{"type":"input_text","text":"%s\nWall time 0.200 seconds (code-mode 0.150 seconds; overhead 0.050 seconds)\nOutput:\n"},{"type":"input_text","text":"error: not a status line"}]`, status)
	}
	tests := []struct {
		name, kind, body string
		want             *bool
	}{
		{"code mode completed with error text in output", "custom_tool_call_output", codeMode("Script completed"), succeeded},
		{"code mode failed", "custom_tool_call_output",
			`[{"type":"input_text","text":"Script failed\nWall time 0.100 seconds (code-mode 0.090 seconds; overhead 0.010 seconds)\nOutput:\n"},{"type":"input_text","text":"Script error:\nReferenceError: x is not defined"}]`, failed},
		{"code mode terminated", "custom_tool_call_output", codeMode("Script terminated"), failed},
		{"code mode still running", "custom_tool_call_output", codeMode("Script running with cell ID 12"), nil},
		{"code mode status as plain text", "custom_tool_call_output", `"Script completed\nWall time 0.200 seconds\nOutput:\n"`, succeeded},
		{"code mode output starting with an image", "custom_tool_call_output", `[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]`, nil},

		{"unified exec exit 0 with error text in output", "function_call_output",
			`"Chunk ID: 4f2a\nWall time: 0.0213 seconds\nProcess exited with code 0\nOriginal token count: 12\nOutput:\nwarning: error: is just text here"`, succeeded},
		{"unified exec nonzero exit", "function_call_output",
			`"Chunk ID: 9b1c\nWall time: 1.5000 seconds\nProcess exited with code 2\nOutput:\nno such file"`, failed},
		{"unified exec still running", "function_call_output",
			`"Chunk ID: 77aa\nWall time: 10.0000 seconds\nProcess running with session ID 5\nOutput:\nbuilding"`, nil},
		{"unified exec exit line only in output", "function_call_output",
			`"Wall time: 0.0100 seconds\nOutput:\nProcess exited with code 1"`, nil},
		{"unified exec aborted", "function_call_output", `"Wall time: 3.2 seconds\naborted by user"`, nil},

		{"shell exit 0", "function_call_output", `"Exit code: 0\nWall time: 0.4 seconds\nOutput:\nok"`, succeeded},
		{"shell nonzero exit", "function_call_output", `"Exit code: 127\nWall time: 0.1 seconds\nOutput:\ncommand not found"`, failed},
		{"shell unreadable exit code", "function_call_output", `"Exit code: n/a\nOutput:\n"`, nil},
		{"legacy JSON exit 0", "function_call_output", `"{\"output\":\"Success. Updated the following files:\\nM a.go\\n\",\"metadata\":{\"exit_code\":0,\"duration_seconds\":0.1}}"`, succeeded},
		{"legacy JSON nonzero exit", "function_call_output", `"{\"output\":\"patch rejected\",\"metadata\":{\"exit_code\":1,\"duration_seconds\":0.1}}"`, failed},
		{"JSON without exit code", "function_call_output", `"{\"result\":\"ok\"}"`, nil},

		{"aborted before dispatch", "function_call_output", `"aborted before dispatch"`, nil},
		{"aborted after start", "custom_tool_call_output", `"aborted by user after 4.0s"`, nil},
		{"handler error message", "function_call_output", `"failed to parse function arguments: missing field cmd"`, nil},
		{"MCP result", "function_call_output", `"Wall time: 0.3000 seconds\nOutput:\n{\"content\":[]}"`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line := fmt.Sprintf(`{"type":"response_item","timestamp":"2026-11-07T10:00:00Z","payload":{"type":%q,"call_id":"call-1","output":%s}}`, tt.kind, tt.body)
			evs, err := ParseCodexLine([]byte(line))
			if err != nil || len(evs) != 1 {
				t.Fatalf("ParseCodexLine = %+v, %v", evs, err)
			}
			got := evs[0].Data.IsError
			if (got == nil) != (tt.want == nil) || got != nil && *got != *tt.want {
				t.Errorf("IsError = %s, want %s", describeBool(got), describeBool(tt.want))
			}
		})
	}
}

func describeBool(b *bool) string {
	if b == nil {
		return "nil"
	}
	return fmt.Sprint(*b)
}
