package parser

import (
	"encoding/json"
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
			if evs := parseCodexUserMessage(t, text); len(evs) != 0 {
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
			evs := parseCodexUserMessage(t, text)
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

func parseCodexUserMessage(t *testing.T, text string) []ParsedEvent {
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
