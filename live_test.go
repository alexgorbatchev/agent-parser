package parser

import (
	"encoding/json"
	"testing"
)

func TestLiveStreams(t *testing.T) {
	cases := []struct {
		harness    string
		lines      []string
		text       string
		structured string
		failed     bool
		events     int
	}{
		{"codex", []string{`{"type":"thread.started","thread_id":"thread"}`, `{"type":"turn.started"}`, `{"type":"item.started","item":{"id":"tool","type":"command_execution"}}`, `{"type":"item.updated","item":{"id":"tool","type":"command_execution"}}`, `{"type":"item.completed","item":{"id":"tool","type":"command_execution","aggregated_output":"ok","exit_code":0}}`, `{"type":"item.updated","item":{"type":"agent_message","delta":"partial"}}`, `{"type":"item.updated","item":{"type":"reasoning","delta":"think"}}`, `{"type":"item.started","item":{"type":"agent_message","text":"provisional"}}`, `{"type":"item.completed","item":{"type":"reasoning","text":"thought"}}`, `{"type":"item.completed","item":{"type":"agent_message","text":"answer"}}`, `{"type":"turn.completed","usage":{"input_tokens":10,"cached_input_tokens":3,"output_tokens":2}}`}, "answer", "", false, 10},
		{"claude", []string{`{"type":"system","subtype":"init","session_id":"session","model":"model"}`, `{"type":"stream_event","event":{"type":"message_start","message":{"model":"model"}}}`, `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"part"}}}`, `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"thought"}}}`, `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{}"}}}`, `{"type":"assistant","message":{"model":"model","content":[{"type":"text","text":"draft"},{"type":"thinking","thinking":"think"},{"type":"tool_use","id":"id","name":"Read","input":{"file_path":"x"}}]}}`, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"id","content":"ok"},{"type":"text","text":"user"}]}}`, `{"type":"result","subtype":"success","result":"old","structured_output":{},"usage":{"input_tokens":2}}`, `{"type":"result","subtype":"success","result":"answer","structured_output":{"ok":true},"usage":{"input_tokens":10,"output_tokens":2,"cache_read_input_tokens":3}}`}, "answer", `{"ok":true}`, false, 11},
		{"codex", []string{`{"type":"error","message":"retry"}`, `{"type":"turn.failed","error":{"message":"failed"}}`}, "", "", true, 2},
		{"claude", []string{`{"type":"result","subtype":"error_max_turns","is_error":true,"errors":["failed"]}`}, "", "", true, 1},
	}
	for _, tt := range cases {
		t.Run(tt.harness+tt.text, func(t *testing.T) {
			p, err := NewLiveParser(tt.harness)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			if _, done := p.Result(); done {
				t.Fatal("premature result")
			}
			for _, line := range tt.lines {
				events, err := p.ParseLine([]byte(line))
				if err != nil {
					t.Fatal(err)
				}
				count += len(events)
				for _, e := range events {
					if e.Data.Result != nil {
						e.Data.Result.StructuredOutput = nil
						if e.Data.Result.Usage != nil {
							e.Data.Result.Usage.Input = 999
						}
					}
				}
			}
			r, done := p.Result()
			if !done || r.Text != tt.text || string(r.StructuredOutput) != tt.structured || r.IsError != tt.failed || count != tt.events {
				t.Fatalf("result=%+v done=%v count=%d", r, done, count)
			}
			if r.Usage != nil {
				wantTotal := int64(12)
				if tt.harness == "claude" {
					wantTotal = 15
				}
				if r.Usage.Total != wantTotal {
					t.Fatalf("total=%d want=%d", r.Usage.Total, wantTotal)
				}
				if r.Usage.Input != 10 || r.Usage.CacheRead != 3 {
					t.Fatalf("usage %+v", r.Usage)
				}
				r.Usage.Input = 999
				next, _ := p.Result()
				if next.Usage.Input != 10 {
					t.Fatal("aliased usage")
				}
			}
			if len(r.StructuredOutput) > 0 {
				r.StructuredOutput[0] = 'x'
				next, _ := p.Result()
				if !json.Valid(next.StructuredOutput) {
					t.Fatal("aliased structured output")
				}
			}
		})
	}
}

func TestLiveInvalidAndIgnored(t *testing.T) {
	if _, err := NewLiveParser("pi"); err == nil {
		t.Fatal("unsupported harness")
	}
	cases := []struct {
		harness, line string
		invalid       bool
	}{
		{"codex", "", false}, {"codex", "not json", true}, {"codex", "{}", true}, {"codex", `{"type":"future"}`, false}, {"codex", `{"type":"item.completed","item":42}`, true}, {"codex", `{"type":"turn.failed","error":"failure"}`, false}, {"codex", `{"type":"error","error":42}`, false},
		{"claude", `{"type":"system","subtype":"other"}`, false}, {"claude", `{"type":"assistant"}`, false}, {"claude", `{"type":"assistant","message":{"content":"hello"}}`, false}, {"claude", `{"type":"assistant","message":{"content":42}}`, true}, {"claude", `{"type":"assistant","message":42}`, true}, {"claude", `{"type":"stream_event","event":42}`, true}, {"claude", `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"future"}}}`, false},
	}
	for _, tt := range cases {
		t.Run(tt.harness+tt.line, func(t *testing.T) {
			p, err := NewLiveParser(tt.harness)
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.ParseLine([]byte(tt.line))
			if (err != nil) != tt.invalid {
				t.Fatalf("err=%v", err)
			}
		})
	}
}
