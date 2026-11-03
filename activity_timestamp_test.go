package parser

import (
	"fmt"
	"testing"
	"time"
)

func TestActivityTimestamps(t *testing.T) {
	const stamp = "2026-10-22T15:55:32.576-07:00"
	const want = int64(1792709732576)
	tests := []struct {
		name  string
		parse func([]byte) (int64, bool, error)
		line  string
	}{
		{"claude user", ClaudeCodeActivityTimestamp, `{"type":"user","timestamp":"` + stamp + `","message":{"role":"user","content":"Run tests"}}`},
		{"claude assistant", ClaudeCodeActivityTimestamp, `{"type":"assistant","timestamp":"` + stamp + `","message":{"role":"assistant","content":[{"type":"text","text":"Working"}]}}`},
		{"claude subagent progress", ClaudeCodeActivityTimestamp, `{"type":"progress","timestamp":"` + stamp + `","data":{"type":"agent_progress","agentId":"child"}}`},
		{"claude attachment", ClaudeCodeActivityTimestamp, `{"type":"attachment","timestamp":"` + stamp + `","attachment":{"type":"hook_success"}}`},
		{"claude system recap", ClaudeCodeActivityTimestamp, `{"type":"system","subtype":"away_summary","timestamp":"` + stamp + `","content":"Tests passed"}`},
		{"pi message envelope wins", PiActivityTimestamp, `{"type":"message","id":"m1","parentId":null,"timestamp":"` + stamp + `","message":{"role":"user","content":"Run tests","timestamp":1733234401000}}`},
		{"pi tool result", PiActivityTimestamp, `{"type":"message","timestamp":"` + stamp + `","message":{"role":"toolResult","toolCallId":"c1","content":[{"type":"text","text":"passed"}]}}`},
		{"pi custom message", PiActivityTimestamp, `{"type":"custom_message","timestamp":"` + stamp + `","customType":"notification","content":"Ready","display":true}`},
		{"pi compaction", PiActivityTimestamp, `{"type":"compaction","timestamp":"` + stamp + `","summary":"Earlier work","tokensBefore":100}`},
		{"pi branch summary", PiActivityTimestamp, `{"type":"branch_summary","timestamp":"` + stamp + `","fromId":"m1","summary":"Earlier branch"}`},
		{"codex user event", CodexActivityTimestamp, `{"type":"event_msg","timestamp":"` + stamp + `","payload":{"type":"user_message","message":"Run tests"}}`},
		{"codex completion envelope wins", CodexActivityTimestamp, `{"type":"event_msg","timestamp":"` + stamp + `","payload":{"type":"task_complete","completed_at":1792709700}}`},
		{"codex tool call", CodexActivityTimestamp, `{"type":"response_item","timestamp":"` + stamp + `","payload":{"type":"function_call","name":"exec_command","call_id":"c1","arguments":"{}"}}`},
		{"codex tool output", CodexActivityTimestamp, `{"type":"response_item","timestamp":"` + stamp + `","payload":{"type":"function_call_output","call_id":"c1","output":"passed"}}`},
		{"codex turn context", CodexActivityTimestamp, `{"type":"turn_context","timestamp":"` + stamp + `","payload":{"model":"codex","turn_id":"t1"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found, err := tt.parse([]byte(tt.line))
			if err != nil || !found || got != want {
				t.Fatalf("activity timestamp = %d, %v, %v; want %d, true, nil", got, found, err, want)
			}
		})
	}
}

func TestActivityTimestampStrictness(t *testing.T) {
	parsers := []struct {
		name  string
		parse func([]byte) (int64, bool, error)
		kind  string
		body  string
	}{
		{"claude", ClaudeCodeActivityTimestamp, "user", `"message":{"role":"user","timestamp":1792709732576}`},
		{"pi", PiActivityTimestamp, "message", `"message":{"role":"user","timestamp":1792709732576}`},
		{"codex", CodexActivityTimestamp, "event_msg", `"payload":{"type":"user_message","timestamp":"2026-10-22T22:55:32Z"}`},
	}
	for _, p := range parsers {
		t.Run(p.name, func(t *testing.T) {
			for _, tt := range []struct {
				name, value    string
				found, wantErr bool
				want           int64
			}{
				{"absent", "", false, false, 0},
				{"null", "null", false, false, 0},
				{"empty", `""`, false, false, 0},
				{"zone absent", `"2026-10-22T22:55:32"`, false, true, 0},
				{"local datetime", `"2026-10-22 22:55:32"`, false, true, 0},
				{"seconds not guessed", `1792709732`, false, true, 0},
				{"milliseconds not guessed", `1792709732576`, false, true, 0},
				{"microseconds not guessed", `1792709732576000`, false, true, 0},
				{"nanoseconds not guessed", `1792709732576000000`, false, true, 0},
				{"boolean", `true`, false, true, 0},
				{"object", `{}`, false, true, 0},
				{"invalid date", `"2026-02-30T00:00:00Z"`, false, true, 0},
				{"epoch", `"1970-01-01T00:00:00Z"`, true, false, 0},
				{"nanosecond string", `"2026-10-22T22:55:32.576123456Z"`, true, false, 1792709732576},
			} {
				t.Run(tt.name, func(t *testing.T) {
					field := ""
					if tt.value != "" {
						field = `,"timestamp":` + tt.value
					}
					line := fmt.Sprintf(`{"type":%q,%s%s}`, p.kind, p.body, field)
					got, found, err := p.parse([]byte(line))
					if (err != nil) != tt.wantErr || found != tt.found || got != tt.want {
						t.Fatalf("timestamp = %d, %v, %v; want %d, %v, error=%v", got, found, err, tt.want, tt.found, tt.wantErr)
					}
				})
			}
			for _, line := range []string{`{"type":`, `[`, `42`} {
				if _, _, err := p.parse([]byte(line)); err == nil {
					t.Fatalf("malformed record accepted: %s", line)
				}
			}
			for _, line := range []string{"", "  \n", "null", `{}`, `{"type":"turn_end","timestamp":"2026-10-22T22:55:32Z"}`} {
				if got, found, err := p.parse([]byte(line)); err != nil || found || got != 0 {
					t.Fatalf("nonactivity = %d, %v, %v for %s", got, found, err, line)
				}
			}
		})
	}
}

func TestActivityTimestampIgnoresMetadataAndForeignTypes(t *testing.T) {
	for _, tt := range []struct {
		name  string
		parse func([]byte) (int64, bool, error)
		kinds []string
	}{
		{"claude", ClaudeCodeActivityTimestamp, []string{"last-prompt", "ai-title", "custom-title", "file-history-snapshot", "queue-operation", "session", "message", "event_msg", "tool_call"}},
		{"pi", PiActivityTimestamp, []string{"session", "session_info", "model_change", "thinking_level_change", "label", "custom", "custom-message", "user", "assistant", "event_msg"}},
		{"codex", CodexActivityTimestamp, []string{"session_meta", "user", "assistant", "message", "event-msg", "response-item", "function-call", "tool_call", "turn_end"}},
	} {
		for _, kind := range tt.kinds {
			t.Run(tt.name+"/"+kind, func(t *testing.T) {
				line := fmt.Sprintf(`{"type":%q,"timestamp":"2026-10-22T22:55:32Z"}`, kind)
				if got, found, err := tt.parse([]byte(line)); err != nil || found || got != 0 {
					t.Fatalf("metadata/foreign type = %d, %v, %v", got, found, err)
				}
			})
		}
	}
	for _, kind := range []string{"event_msg", "response_item"} {
		line := fmt.Sprintf(`{"type":%q,"timestamp":"2026-10-22T22:55:32Z","payload":{"type":"turn_end"}}`, kind)
		if _, found, err := CodexActivityTimestamp([]byte(line)); err != nil || found {
			t.Fatalf("normalized nested type counted as native activity: %v, %v", found, err)
		}
	}
}

func TestActivityTimestampsAgreeWithParsedRecords(t *testing.T) {
	stamp := "2026-10-22T22:55:32.576Z"
	for _, tt := range []struct {
		line     string
		activity func([]byte) (int64, bool, error)
		parse    func([]byte) ([]ParsedEvent, error)
	}{
		{`{"type":"user","timestamp":"` + stamp + `","message":{"role":"user","content":"Run tests"}}`, ClaudeCodeActivityTimestamp, NewClaudeCodeParser().ParseLine},
		{`{"type":"message","timestamp":"` + stamp + `","message":{"role":"user","content":"Run tests"}}`, PiActivityTimestamp, func(line []byte) ([]ParsedEvent, error) { return ParsePiLine(line, "") }},
		{`{"type":"event_msg","timestamp":"` + stamp + `","payload":{"type":"user_message","message":"Run tests"}}`, CodexActivityTimestamp, ParseCodexLine},
	} {
		got, found, err := tt.activity([]byte(tt.line))
		if err != nil || !found {
			t.Fatalf("activity = %d, %v, %v", got, found, err)
		}
		events, err := tt.parse([]byte(tt.line))
		if err != nil || len(events) == 0 {
			t.Fatalf("events = %v, %v", events, err)
		}
		if events[0].Timestamp != got || time.UnixMilli(got).UTC().Format(time.RFC3339Nano) != stamp {
			t.Fatalf("activity %d disagrees with parsed event %+v", got, events[0])
		}
	}
}

func TestCodexActivityTimestampRejectsMalformedPayload(t *testing.T) {
	for _, kind := range []string{"event_msg", "response_item"} {
		for _, payload := range []string{`"invalid"`, `42`, `[]`} {
			line := fmt.Sprintf(`{"type":%q,"timestamp":"2026-10-22T22:55:32Z","payload":%s}`, kind, payload)
			if got, found, err := CodexActivityTimestamp([]byte(line)); err == nil || found || got != 0 {
				t.Fatalf("malformed payload = %d, %v, %v for %s", got, found, err, line)
			}
		}
	}
}
