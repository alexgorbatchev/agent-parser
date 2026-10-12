package parser

import (
	"fmt"
	"testing"
)

func TestCodexToolOutputBodies(t *testing.T) {
	for _, kind := range []string{"function_call_output", "custom_tool_call_output"} {
		for _, tt := range []struct {
			name, body, want string
			isError, wantErr bool
		}{
			{"text", `"done\nnext"`, "done\nnext", false, false},
			{"text blocks", `[ {"type":"input_text","text":"first"}, {"type":"input_text","text":"second"} ]`, `[{"type":"input_text","text":"first"},{"type":"input_text","text":"second"}]`, false, false},
			{"error blocks", `[{"type":"input_text","text":"Error: command failed"}]`, `[{"type":"input_text","text":"Error: command failed"}]`, true, false},
			{"media blocks", `[{"type":"input_text","text":"image"},{"type":"input_image","image_url":"data:image/png;base64,AA==","detail":"high"},{"type":"input_audio","audio_url":"data:audio/wav;base64,AA=="},{"type":"encrypted_content","encrypted_content":"opaque"}]`, `[{"type":"input_text","text":"image"},{"type":"input_image","image_url":"data:image/png;base64,AA==","detail":"high"},{"type":"input_audio","audio_url":"data:audio/wav;base64,AA=="},{"type":"encrypted_content","encrypted_content":"opaque"}]`, false, false},
			{"empty array", `[]`, `[]`, false, false},
			{"null", `null`, "", false, false},
			{"number", `42`, "", false, true},
			{"object", `{"text":"invalid"}`, "", false, true},
		} {
			t.Run(kind+"/"+tt.name, func(t *testing.T) {
				line := fmt.Sprintf(`{"type":"response_item","timestamp":"2026-10-09T21:37:22Z","payload":{"type":%q,"call_id":"call-1","output":%s}}`, kind, tt.body)
				events, err := NewCodexParser().ParseLine([]byte(line))
				if (err != nil) != tt.wantErr {
					t.Fatalf("ParseLine error = %v, wantErr %v", err, tt.wantErr)
				}
				if tt.wantErr {
					return
				}
				if len(events) != 1 || events[0].EventType != "tool_result" {
					t.Fatalf("events = %+v, want one tool_result", events)
				}
				data := events[0].Data
				if data.ToolOutput == nil || *data.ToolOutput != tt.want {
					t.Fatalf("output = %v, want %q", data.ToolOutput, tt.want)
				}
				if data.ToolCallID == nil || *data.ToolCallID != "call-1" || data.IsError == nil || *data.IsError != tt.isError {
					t.Fatalf("result metadata = %+v", data)
				}
			})
		}
	}
}
