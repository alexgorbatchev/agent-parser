package parser

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestCodexContextualUserMessages(t *testing.T) {
	contextual := map[string]string{
		"AGENTS.md instructions":   "# AGENTS.md instructions for /work\n\n<INSTRUCTIONS>\nUse tabs.\n</INSTRUCTIONS>",
		"environment":              "<environment_context>\n  <cwd>/work</cwd>\n</environment_context>",
		"environment, other case":  "  <ENVIRONMENT_CONTEXT>\n</Environment_Context>\n",
		"message board":            "<agent_message_board_notification>\nnew post\n</agent_message_board_notification>",
		"skill":                    "<skill>\n<name>demo</name>\n</skill>",
		"user shell command":       "<user_shell_command>\n<command>\nls\n</command>\n</user_shell_command>",
		"turn aborted":             "<turn_aborted>\nThe user interrupted the previous turn.\n</turn_aborted>",
		"subagent notification":    "<subagent_notification>\n{\"agent\":\"helper\"}\n</subagent_notification>",
		"recommended plugins":      "<recommended_plugins>\n- demo\n</recommended_plugins>",
		"external context":         "<external_ticket>\nDetails\n</external_ticket>",
		"internal context":         "<codex_internal_context source=\"goal_v2\">\nKeep going.\n</codex_internal_context>",
		"legacy goal context":      "<goal_context>\nShip it.\n</goal_context>",
		"hook prompt":              `<hook_prompt hook_run_id="run-1">Check the diff.</hook_prompt>`,
		"process limit warning":    "Warning: The maximum number of unified exec processes you can keep open is 8.",
		"cyber activity warning":   "Warning: Your account was flagged for potentially high-risk cyber activity and was rerouted.",
		"apply_patch exec warning": "Warning: apply_patch was requested via exec_command. Use the apply_patch tool instead of exec_command.",
	}
	for name, text := range contextual {
		t.Run("contextual/"+name, func(t *testing.T) {
			if evs := parseCodexUserResponseItem(t, text); len(evs) != 0 {
				t.Errorf("events = %+v; want none for injected context", evs)
			}
		})
	}

	prompts := map[string]string{
		"mentions a tag in prose":      "Why does <subagent_notification> show up in my transcript?",
		"opens a tag without closing":  "<turn_aborted> is what I see after Ctrl-C",
		"external tag with other key":  "<external_ticket>\nDetails\n</external_other>",
		"internal context bad source":  "<codex_internal_context source=\"Goal\">\nx\n</codex_internal_context>",
		"hook prompt without a run id": `<hook_prompt hook_run_id=" ">Check the diff.</hook_prompt>`,
		"unrelated warning":            "Warning: this test is flaky",
	}
	for name, text := range prompts {
		t.Run("prompt/"+name, func(t *testing.T) {
			evs := parseCodexUserResponseItem(t, text)
			if len(evs) != 1 || evs[0].EventType != "turn_start" || evs[0].Data.Content == nil || *evs[0].Data.Content != text {
				t.Errorf("events = %+v; want a turn_start for the prompt", evs)
			}
		})
	}
}

func TestCodexContextualFragmentMakesWholeMessageContext(t *testing.T) {
	line := `{"type":"response_item","timestamp":"2026-11-07T10:00:00Z","payload":{"type":"message","role":"user","content":[` +
		`{"type":"input_text","text":"<environment_context>\n</environment_context>"},` +
		`{"type":"input_text","text":"Real question"}]}}`
	if evs, err := ParseCodexLine([]byte(line)); err != nil || len(evs) != 0 {
		t.Errorf("events = %+v, %v; want none", evs, err)
	}

	// Plain string content follows the same rule.
	line = `{"type":"response_item","timestamp":"2026-11-07T10:00:00Z","payload":{"type":"message","role":"user","content":"<turn_aborted>\nx\n</turn_aborted>"}}`
	if evs, err := ParseCodexLine([]byte(line)); err != nil || len(evs) != 0 {
		t.Errorf("string content events = %+v, %v; want none", evs, err)
	}
}

func parseCodexUserResponseItem(t *testing.T, text string) []ParsedEvent {
	t.Helper()
	quoted, err := json.Marshal(text)
	if err != nil {
		t.Fatal(err)
	}
	line := `{"type":"response_item","timestamp":"2026-11-07T10:00:00Z","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":` + string(quoted) + `}]}}`
	evs, err := ParseCodexLine([]byte(line))
	if err != nil {
		t.Fatalf("ParseCodexLine: %v", err)
	}
	return evs
}

func TestCodexPaginatedPromptsComeFromUserMessageItems(t *testing.T) {
	meta := func(mode string) string {
		return `{"timestamp":"2026-11-07T10:00:00Z","type":"session_meta","payload":{"id":"thread","cwd":"/work","source":"cli","model_provider":"openai"` + mode + `}}`
	}
	start := `{"timestamp":"2026-11-07T10:00:01Z","type":"event_msg","payload":{"type":"task_started","turn_id":"t1","started_at":1794045601}}`
	userItem := func(text, extra string) string {
		return `{"timestamp":"2026-11-07T10:00:03Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + text + `"}` + extra + `]}}`
	}
	completed := func(content string) string {
		return `{"timestamp":"2026-11-07T10:00:03Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u","content":[` + content + `]}}}`
	}
	image := `,{"type":"input_image","image_url":"data:image/png;base64,AA=="}`
	tests := []struct {
		name  string
		lines []string
		want  string
	}{
		{
			name: "prompt with images from the adjacent response item",
			lines: []string{meta(`,"history_mode":"paginated"`), start,
				userItem("Look at this", image),
				completed(`{"type":"text","text":"Look at this","text_elements":[]},{"type":"local_image","path":"/tmp/shot.png"},{"type":"skill","name":"demo","path":"/skills/demo/SKILL.md"}`)},
			want: "turn_start:Look at this images=1|skill_invoked",
		},
		{
			// Context Codex injects with a marker this parser does not know yet
			// gets no UserMessage item, so it is not a prompt.
			name:  "unknown injected context",
			lines: []string{meta(`,"history_mode":"paginated"`), start, userItem("<future_context>\\nx\\n</future_context>", "")},
			want:  "",
		},
		{
			name: "UserMessage without an adjacent response item",
			lines: []string{meta(`,"history_mode":"paginated"`), start,
				completed(`{"type":"text","text":"Remote prompt","text_elements":[]},{"type":"image","image_url":"https://example.test/a.png"}`)},
			want: "turn_start:Remote prompt images=1",
		},
		{
			name: "UserMessage image uploaded as a file",
			lines: []string{meta(`,"history_mode":"paginated"`), start,
				completed(`{"type":"text","text":"Uploaded","text_elements":[]},{"type":"image","file_id":"file-1"}`)},
			want: "turn_start:Uploaded images=1",
		},
		{
			name: "legacy history uses the response item",
			lines: []string{meta(""), start,
				userItem("Legacy prompt", ""),
				completed(`{"type":"text","text":"Legacy prompt","text_elements":[]}`)},
			want: "turn_start:Legacy prompt images=0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewCodexParser()
			var got []string
			for _, line := range tt.lines {
				evs, err := p.ParseLine([]byte(line))
				if err != nil {
					t.Fatalf("ParseLine(%s): %v", line, err)
				}
				for _, ev := range evs {
					switch ev.EventType {
					case "turn_start":
						if ev.Data.Content != nil {
							got = append(got, "turn_start:"+*ev.Data.Content+" images="+fmt.Sprint(len(ev.Data.Images)))
						}
					case "skill_invoked", "message_absorbed":
						got = append(got, ev.EventType)
					}
				}
			}
			if strings.Join(got, "|") != tt.want {
				t.Errorf("events = %q, want %q", strings.Join(got, "|"), tt.want)
			}
		})
	}

	// The stateless parser has no session_meta, keeps the response item as the
	// prompt source, and reads only skills from the UserMessage item.
	evs, err := ParseCodexLine([]byte(completed(`{"type":"text","text":"x","text_elements":[]},{"type":"skill","name":"demo","path":"/s/SKILL.md"}`)))
	if err != nil || len(evs) != 1 || evs[0].EventType != "skill_invoked" {
		t.Errorf("stateless UserMessage = %+v, %v; want only skill_invoked", evs, err)
	}
}
