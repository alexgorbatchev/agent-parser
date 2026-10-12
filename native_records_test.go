package parser

import (
	"encoding/json"
	"testing"
)

func TestClaudeAwaySummary(t *testing.T) {
	for _, tt := range []struct {
		name, subtype, content string
		want                   int
	}{
		{"recap", "away_summary", "We're finishing the Yarn-to-Bun migration PR and its review fixes", 1},
		{"empty", "away_summary", "", 0},
		{"timing", "turn_duration", "timing metadata", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := NewClaudeCodeParser()
			line, err := json.Marshal(map[string]interface{}{"type": "system", "subtype": tt.subtype, "content": tt.content, "timestamp": "2026-10-09T23:47:11.636Z", "cwd": "/repo", "gitBranch": "migration"})
			if err != nil {
				t.Fatal(err)
			}
			events, err := p.ParseLine(line)
			if err != nil {
				t.Fatal(err)
			}
			var summaries []ParsedEvent
			for _, event := range events {
				if event.EventType == "summary" {
					summaries = append(summaries, event)
				}
			}
			if len(summaries) != tt.want {
				t.Fatalf("summary events = %d, want %d", len(summaries), tt.want)
			}
			if tt.want == 0 {
				return
			}
			event := summaries[0]
			if event.Data.Content == nil || *event.Data.Content != tt.content || event.Timestamp != 1791589631636 || event.CWD != "/repo" || event.GitBranch != "migration" {
				t.Fatalf("lost recap data: %+v", event)
			}
		})
	}
}

func TestCodexNativeAgentMessage(t *testing.T) {
	for _, tt := range []struct{ name, line, eventType, text string }{
		{"async tool", `{"type":"response_item","payload":{"type":"function_call","name":"request_user_input_async","call_id":"question-1","arguments":"{\"questions\":[{\"title\":\"Where does your dashboard run?\"},{\"title\":\"Which machine?\"}]}"}}`, "question_prompt", "Where does your dashboard run?\nWhich machine?"},
		{"sync tool", `{"type":"response_item","payload":{"type":"function_call","name":"request_user_input","arguments":"{\"questions\":[{\"question\":\"Which host?\"}]}"}}`, "question_prompt", "Which host?"},
		{"async message", `{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","id":"question-1","content":[{"type":"Text","text":"Where does your dashboard run?"}],"phase":"final_answer","delivery":"async","questions":[{"title":"Where does your dashboard run?"}]}}}`, "question_prompt", "Where does your dashboard run?"},
		{"ordinary message", `{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","content":[{"type":"Text","text":"Migration finished."}],"phase":"final_answer"}}}`, "agent_message", "Migration finished."},
		{"empty message", `{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","content":[]}}}`, "", ""},
		{"other item", `{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"ToolCall","content":[{"type":"Text","text":"not a message"}]}}}`, "", ""},
		{"unknown item fields", `{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"ToolCall","content":17,"questions":"other schema"}}}`, "", ""},
		{"missing item", `{"type":"event_msg","payload":{"type":"item_completed"}}`, "", ""},
		{"question without content", `{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","questions":[{"title":"Where?"}]}}}`, "question_prompt", "Where?"},
		{"blank questions", `{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","content":[{"type":"Text","text":"Done"}],"questions":[{"title":" "}]}}}`, "agent_message", "Done"},
		{"nontext content", `{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","content":[{"type":"Image","text":"not conversational text"}]}}}`, "", ""},
		{"prompt input", `{"type":"response_item","payload":{"type":"function_call","name":"request_user_input","arguments":"{\"prompt\":\"Which host?\"}"}}`, "question_prompt", "Which host?"},
		{"mixed questions", `{"type":"response_item","payload":{"type":"function_call","name":"request_user_input","arguments":"{\"questions\":[17,{\"title\":\" \"},{\"title\":\"Where?\"}]}"}}`, "question_prompt", "Where?"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			events, err := ParseCodexLine([]byte(tt.line))
			if err != nil {
				t.Fatal(err)
			}
			if tt.eventType == "" {
				if len(events) != 0 {
					t.Fatalf("unexpected events: %+v", events)
				}
				return
			}
			if len(events) != 1 || events[0].EventType != tt.eventType {
				t.Fatalf("events = %+v, want %s", events, tt.eventType)
			}
			text := events[0].Data.Content
			if tt.eventType == "question_prompt" {
				text = events[0].Data.Question
			}
			if text == nil || *text != tt.text {
				t.Fatalf("text = %v, want %q", text, tt.text)
			}
			if tt.name == "async tool" {
				if events[0].Data.ToolCallID == nil || *events[0].Data.ToolCallID != "question-1" || events[0].Data.ToolInput == nil {
					t.Fatalf("lost question tool metadata: %+v", events[0].Data)
				}
			}
			if tt.name == "async message" && (events[0].Data.ToolCallID == nil || *events[0].Data.ToolCallID != "question-1") {
				t.Fatalf("lost async question correlation: %+v", events[0].Data)
			}
		})
	}
}

func TestCodexMalformedCompletedItem(t *testing.T) {
	for _, line := range []string{
		`{"type":"event_msg","payload":{"type":"item_completed","item":17}}`,
		`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","content":17}}}`,
	} {
		if _, err := ParseCodexLine([]byte(line)); err == nil {
			t.Fatalf("expected parse error for %s", line)
		}
	}
}
