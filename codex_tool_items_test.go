package parser

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

const codexPaginatedMeta = `{"timestamp":"2026-11-07T10:00:00Z","type":"session_meta","payload":{"id":"t","cwd":"/work","history_mode":"paginated"}}`

func codexItemLine(item string) string {
	return `{"timestamp":"2026-11-07T10:00:05Z","type":"event_msg","payload":{"type":"item_completed","thread_id":"t","turn_id":"u","item":` + item + `,"started_at_ms":1794045601000,"completed_at_ms":1794045605000}}`
}

func TestCodexNestedToolItems(t *testing.T) {
	tests := []struct {
		name, item string
		want       string
	}{
		{"command succeeded",
			`{"type":"CommandExecution","id":"exec-1","command":["/usr/bin/zsh","-lc","ls -la"],"cwd":"file:///work/app","source":"unified_exec_startup","status":"completed","aggregated_output":"a\nb\n","exit_code":0}`,
			`exec_command command="ls -la" cwd="/work/app" | out="a\nb\n" err=false`},
		{"command failed",
			`{"type":"CommandExecution","id":"exec-2","command":["/usr/bin/zsh","-lc","false"],"cwd":"file:///work","status":"failed","aggregated_output":"","exit_code":1}`,
			`exec_command command="false" cwd="/work" | out="" err=true`},
		{"command declined",
			`{"type":"CommandExecution","id":"exec-3","command":["rm","-rf","x"],"cwd":"/work","status":"declined"}`,
			`exec_command command="rm -rf x" cwd="/work" | out=<nil> err=true`},
		{"command running",
			`{"type":"CommandExecution","id":"exec-4","command":["sleep","9"],"status":"in_progress"}`,
			`exec_command command="sleep 9" | out=<nil> err=nil`},
		{"mcp call",
			`{"type":"McpToolCall","id":"exec-5","server":"codex_apps","tool":"github.create_branch","arguments":{"repo":"o/r"},"status":"completed","result":{"content":[{"type":"text","text":"ok"}]}}`,
			`mcp__codex_apps__github.create_branch repo="o/r" | out={"content":[{"type":"text","text":"ok"}]} err=false`},
		{"mcp result error",
			`{"type":"McpToolCall","id":"exec-6","server":"s","tool":"t","arguments":{},"status":"completed","result":{"content":[],"isError":true}}`,
			`mcp__s__t | out={"content":[],"isError":true} err=true`},
		{"mcp failed",
			`{"type":"McpToolCall","id":"exec-7","server":"s","tool":"t","arguments":{},"status":"failed","error":{"message":"schema"}}`,
			`mcp__s__t | out={"message":"schema"} err=true`},
		{"web search",
			`{"type":"WebSearch","id":"ws_1","query":"go generics","action":{"type":"search","query":"go generics"}}`,
			`web_search action=map[query:go generics type:search] query="go generics" | out=<nil> err=nil`},
		{"extension",
			`{"type":"Extension","kind":"web.search","id":"exec-8","query":"https://example.test","action":{"type":"openPage","url":"https://example.test"},"results":[{"type":"text_result"}]}`,
			`web.search action=map[type:openPage url:https://example.test] kind="web.search" query="https://example.test" | out=[{"type":"text_result"}] err=nil`},
		{"extension failure",
			`{"type":"Extension","kind":"image.generate","id":"exec-9","failure":{"message":"blocked"}}`,
			`image.generate kind="image.generate" | out=<nil> err=true`},
		{"dynamic tool",
			`{"type":"DynamicToolCall","id":"exec-10","tool":"workspace_info","arguments":{},"status":"completed","content_items":[{"type":"inputText","text":"{}"}],"success":false}`,
			`workspace_info | out=[{"type":"inputText","text":"{}"}] err=true`},
		{"image view",
			`{"type":"ImageView","id":"exec-11","path":"file:///tmp/shot.png"}`,
			`view_image path="/tmp/shot.png" | out=<nil> err=nil`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewCodexParser()
			if _, err := p.ParseLine([]byte(codexPaginatedMeta)); err != nil {
				t.Fatal(err)
			}
			evs, err := p.ParseLine([]byte(codexItemLine(tt.item)))
			if err != nil {
				t.Fatal(err)
			}
			var calls []ParsedEvent
			for _, ev := range evs {
				if ev.EventType == "tool_call" || ev.EventType == "tool_result" {
					calls = append(calls, ev)
				}
			}
			if len(calls) != 2 || calls[0].EventType != "tool_call" || calls[1].EventType != "tool_result" {
				t.Fatalf("events = %+v; want tool_call then tool_result", evs)
			}
			if calls[0].Timestamp != 1794045601000 || calls[1].Timestamp != 1794045605000 {
				t.Errorf("timestamps = %d, %d; want the item's start and completion", calls[0].Timestamp, calls[1].Timestamp)
			}
			if *calls[0].Data.ToolCallID != *calls[1].Data.ToolCallID {
				t.Errorf("call and result IDs differ")
			}
			if got := describeToolItem(calls[0].Data, calls[1].Data); got != tt.want {
				t.Errorf("got  %s\nwant %s", got, tt.want)
			}
		})
	}
}

func TestCodexToolItemsRepeatingRecordedCallsAreSkipped(t *testing.T) {
	p := NewCodexParser()
	lines := []string{
		codexPaginatedMeta,
		`{"timestamp":"2026-11-07T10:00:01Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"ls\"}","call_id":"call_1"}}`,
		codexItemLine(`{"type":"CommandExecution","id":"call_1","command":["/usr/bin/zsh","-lc","ls"],"status":"completed","exit_code":0,"aggregated_output":""}`),
	}
	var calls int
	for _, line := range lines {
		evs, err := p.ParseLine([]byte(line))
		if err != nil {
			t.Fatal(err)
		}
		for _, ev := range evs {
			if ev.EventType == "tool_call" {
				calls++
			}
		}
	}
	if calls != 1 {
		t.Errorf("tool_call count = %d, want 1", calls)
	}
}

func TestCodexToolItemsOutsidePaginatedStatefulParsing(t *testing.T) {
	item := codexItemLine(`{"type":"McpToolCall","id":"exec-1","server":"s","tool":"t","arguments":{},"status":"completed","result":{}}`)
	if evs, err := ParseCodexLine([]byte(item)); err != nil || len(evs) != 0 {
		t.Errorf("stateless = %+v, %v; want none", evs, err)
	}
	p := NewCodexParser()
	if _, err := p.ParseLine([]byte(`{"type":"session_meta","payload":{"id":"t"}}`)); err != nil {
		t.Fatal(err)
	}
	if evs, err := p.ParseLine([]byte(item)); err != nil || len(evs) != 0 {
		t.Errorf("legacy = %+v, %v; want none", evs, err)
	}
	p = NewCodexParser()
	if _, err := p.ParseLine([]byte(codexPaginatedMeta)); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ParseLine([]byte(codexItemLine(`{"type":"McpToolCall","id":7}`))); err == nil {
		t.Error("expected an error for a malformed item")
	}
}

func describeToolItem(call, result EventData) string {
	parts := []string{*call.ToolName}
	keys := make([]string, 0, len(call.ToolInput))
	for k := range call.ToolInput {
		if k != "argv" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		if s, ok := call.ToolInput[k].(string); ok {
			parts = append(parts, fmt.Sprintf("%s=%q", k, s))
		} else {
			parts = append(parts, fmt.Sprintf("%s=%v", k, call.ToolInput[k]))
		}
	}
	out := "<nil>"
	if result.ToolOutput != nil {
		out = *result.ToolOutput
		if !strings.HasPrefix(out, "{") && !strings.HasPrefix(out, "[") {
			out = fmt.Sprintf("%q", out)
		}
	}
	return strings.Join(parts, " ") + " | out=" + out + " err=" + describeBool(result.IsError)
}
