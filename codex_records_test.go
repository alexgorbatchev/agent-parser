package parser

import (
	"fmt"
	"strings"
	"testing"
)

func TestCodexCompaction(t *testing.T) {
	for line, want := range map[string]string{
		`{"timestamp":"2026-11-07T10:00:00Z","type":"compacted","payload":{"message":"Summary of earlier work.","replacement_history":[]}}`: "Summary of earlier work.",
		`{"timestamp":"2026-11-07T10:00:00Z","type":"compacted","payload":{"message":"","replacement_history":[],"window_number":2}}`:   "",
	} {
		evs, err := ParseCodexLine([]byte(line))
		if err != nil || len(evs) != 1 || evs[0].EventType != "compaction" {
			t.Fatalf("events = %+v, %v; want one compaction", evs, err)
		}
		got := ""
		if evs[0].Data.Content != nil {
			got = *evs[0].Data.Content
		}
		if got != want {
			t.Errorf("content = %q, want %q", got, want)
		}
	}
	// The ContextCompaction item repeats the compacted record.
	item := `{"timestamp":"2026-11-07T10:00:00Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"ContextCompaction","id":"c1"}}}`
	if evs, err := ParseCodexLine([]byte(item)); err != nil || len(evs) != 0 {
		t.Errorf("ContextCompaction item = %+v, %v; want none", evs, err)
	}
	if _, err := ParseCodexLine([]byte(`{"type":"compacted","payload":"bad"}`)); err == nil {
		t.Error("expected an error for a malformed compacted record")
	}
}

func TestCodexWebSearchCall(t *testing.T) {
	line := `{"timestamp":"2026-11-07T10:00:00Z","type":"response_item","payload":{"type":"web_search_call","id":"ws_1","status":"completed","action":{"type":"search","query":"go generics"}}}`
	evs, err := ParseCodexLine([]byte(line))
	if err != nil || len(evs) != 2 || evs[0].EventType != "tool_call" || evs[1].EventType != "tool_result" {
		t.Fatalf("events = %+v, %v", evs, err)
	}
	if *evs[0].Data.ToolName != "web_search" || evs[0].Data.ToolInput["query"] != "go generics" || *evs[1].Data.ToolCallID != "ws_1" || *evs[1].Data.IsError {
		t.Errorf("events = %+v", evs)
	}

	// Paginated history reports the search from its WebSearch item instead.
	p := NewCodexParser()
	if _, err := p.ParseLine([]byte(codexPaginatedMeta)); err != nil {
		t.Fatal(err)
	}
	if evs, err := p.ParseLine([]byte(line)); err != nil || len(evs) != 0 {
		t.Errorf("paginated web_search_call = %+v, %v; want none", evs, err)
	}
}

func TestCodexLocalShellCall(t *testing.T) {
	p := NewCodexParser()
	lines := []string{
		`{"timestamp":"2026-11-07T10:00:00Z","type":"response_item","payload":{"type":"local_shell_call","id":"lsh_1","call_id":"call_1","status":"completed","action":{"type":"exec","command":["bash","-lc","ls"],"timeout_ms":1000,"working_directory":"/work"}}}`,
		`{"timestamp":"2026-11-07T10:00:01Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_1","output":"Exit code: 0\nOutput:\nREADME.md\n"}}`,
	}
	var got []string
	for _, line := range lines {
		evs, err := p.ParseLine([]byte(line))
		if err != nil {
			t.Fatal(err)
		}
		for _, ev := range evs {
			switch ev.EventType {
			case "tool_call":
				got = append(got, fmt.Sprintf("call %s %v %v %v", *ev.Data.ToolName, ev.Data.ToolInput["command"], ev.Data.ToolInput["cwd"], ev.Data.ToolInput["timeout_ms"]))
			case "tool_result":
				got = append(got, "result "+*ev.Data.ToolCallID+" err="+describeBool(ev.Data.IsError))
			}
		}
	}
	want := "call local_shell ls /work 1000|result call_1 err=false"
	if strings.Join(got, "|") != want {
		t.Errorf("events = %q, want %q", strings.Join(got, "|"), want)
	}
}

func TestCodexImageGenerationCall(t *testing.T) {
	png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="
	line := `{"timestamp":"2026-11-07T10:00:00Z","type":"response_item","payload":{"type":"image_generation_call","id":"ig_1","status":"completed","revised_prompt":"A dot","result":"` + png + `"}}`
	evs, err := ParseCodexLine([]byte(line))
	if err != nil || len(evs) != 2 {
		t.Fatalf("events = %+v, %v", evs, err)
	}
	if *evs[0].Data.ToolName != "image_generation" || evs[0].Data.ToolInput["revised_prompt"] != "A dot" {
		t.Errorf("call = %+v", evs[0].Data)
	}
	images := evs[1].Data.Images
	if len(images) != 1 || images[0].MediaType != "image/png" || images[0].Data != png {
		t.Errorf("result images = %+v", images)
	}
	if got := codexBase64Images("not base64!"); got != nil {
		t.Errorf("invalid base64 = %+v, want nil", got)
	}
}

func TestCodexInterAgentMessages(t *testing.T) {
	msg := func(ts, text string) string {
		return `{"timestamp":"2026-11-07T10:00:` + ts + `Z","type":"response_item","payload":{"type":"agent_message","id":"amsg_` + ts + `","author":"/root","recipient":"/root/helper","content":[{"type":"input_text","text":"` + text + `"}]}}`
	}
	p := NewCodexParser()
	lines := []string{
		codexPaginatedMeta,
		`{"timestamp":"2026-11-07T10:00:01Z","type":"event_msg","payload":{"type":"task_started","turn_id":"t1","started_at":1794045601}}`,
		`{"timestamp":"2026-11-07T10:00:02Z","type":"inter_agent_communication_metadata","payload":{"trigger_turn":true}}`,
		msg("02", `Message Type: NEW_TASK\nTask name: /root/helper\nPayload:\nCount the files.`),
		msg("05", `Message Type: MESSAGE\nPayload:\nAlso list them.`),
	}
	var got []string
	for _, line := range lines {
		evs, err := p.ParseLine([]byte(line))
		if err != nil {
			t.Fatal(err)
		}
		for _, ev := range evs {
			if ev.EventType == "turn_start" || ev.EventType == "message_absorbed" {
				if ev.Data.Title != nil {
					t.Errorf("%s set a title", ev.EventType)
				}
				got = append(got, ev.EventType+"/"+*ev.Data.Role+":"+strings.SplitN(*ev.Data.Content, "\n", 2)[0])
			}
		}
	}
	want := "turn_start/agent:Message Type: NEW_TASK|message_absorbed/agent:Message Type: MESSAGE"
	if strings.Join(got, "|") != want {
		t.Errorf("events = %q, want %q", strings.Join(got, "|"), want)
	}
	if evs, err := ParseCodexLine([]byte(`{"type":"response_item","payload":{"type":"agent_message","content":[]}}`)); err != nil || len(evs) != 0 {
		t.Errorf("empty message = %+v, %v", evs, err)
	}
}

func TestCodexPreTurnCompactionKeepsThePrompt(t *testing.T) {
	// Codex compacts before recording the turn's prompt (run_turn's pre-turn
	// auto-compaction): the prompt still opens the turn, and the compaction
	// follows the turn's start.
	p := NewCodexParser()
	lines := []string{
		codexPaginatedMeta,
		`{"timestamp":"2026-11-07T10:00:01Z","type":"event_msg","payload":{"type":"task_started","turn_id":"t1","started_at":1794045601}}`,
		`{"timestamp":"2026-11-07T10:00:02Z","type":"compacted","payload":{"message":"","replacement_history":[]}}`,
		`{"timestamp":"2026-11-07T10:00:03Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Continue"}]}}`,
		`{"timestamp":"2026-11-07T10:00:03Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u1","content":[{"type":"text","text":"Continue","text_elements":[]}]}}}`,
	}
	var got []string
	for _, line := range lines {
		evs, err := p.ParseLine([]byte(line))
		if err != nil {
			t.Fatal(err)
		}
		for _, ev := range evs {
			desc := ev.EventType
			if ev.Data.Content != nil {
				desc += ":" + *ev.Data.Content
			}
			got = append(got, desc)
		}
	}
	want := "run_started|turn_start:Continue|compaction"
	if strings.Join(got, "|") != want {
		t.Errorf("events = %q, want %q", strings.Join(got, "|"), want)
	}
}

func TestCodexManualCompactionStaysInItsTurn(t *testing.T) {
	p := NewCodexParser()
	lines := []string{
		codexPaginatedMeta,
		`{"timestamp":"2026-11-07T10:00:01Z","type":"event_msg","payload":{"type":"task_started","turn_id":"t1","started_at":1794045601}}`,
		`{"timestamp":"2026-11-07T10:00:02Z","type":"compacted","payload":{"message":"Summary.","replacement_history":[]}}`,
		`{"timestamp":"2026-11-07T10:00:03Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","completed_at":1794045603}}`,
	}
	var got []string
	var stamps []int64
	for _, line := range lines {
		evs, err := p.ParseLine([]byte(line))
		if err != nil {
			t.Fatal(err)
		}
		for _, ev := range evs {
			if ev.EventType != "run_started" {
				got = append(got, ev.EventType)
				stamps = append(stamps, ev.Timestamp)
			}
		}
	}
	if strings.Join(got, "|") != "turn_start|compaction|turn_end" {
		t.Errorf("events = %q, want turn_start|compaction|turn_end", got)
	}
	for i := 1; i < len(stamps); i++ {
		if stamps[i] < stamps[i-1] {
			t.Errorf("timestamps go backwards: %v", stamps)
		}
	}
}
