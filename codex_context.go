package parser

import (
	"encoding/xml"
	"strings"
)

// Codex records context it injects for the model, such as instructions,
// environment state and subagent notices, as user-role messages. It treats a
// user message as such contextual input, not a user prompt, when any of its
// text items is a contextual fragment (codex-rs core/src/event_mapping.rs
// is_contextual_user_message_content). The matchers below follow
// core/src/context/contextual_user_message.rs CONTEXTUAL_USER_FRAGMENT_MATCHERS.

// codexMarkedFragments are the fragments recognized by their start and end
// markers, compared without ASCII case after trimming whitespace
// (context-fragments matches_marked_text).
var codexMarkedFragments = [][2]string{
	{"# AGENTS.md instructions", "</INSTRUCTIONS>"},                               // UserInstructions
	{"<environment_context>", "</environment_context>"},                           // EnvironmentsState
	{"<agent_message_board_notification>", "</agent_message_board_notification>"}, // AgentMessageBoardNotification
	{"<skill>", "</skill>"},                                                       // SkillInstructions
	{"<user_shell_command>", "</user_shell_command>"},                             // UserShellCommand
	{"<turn_aborted>", "</turn_aborted>"},                                         // TurnAborted
	{"<subagent_notification>", "</subagent_notification>"},                       // SubagentNotification
	{"<recommended_plugins>", "</recommended_plugins>"},                           // RecommendedPluginsInstructions
}

// codexWarningPrefixes are legacy warnings Codex wrote as user messages.
var codexWarningPrefixes = []string{
	"Warning: The maximum number of unified exec processes you can keep open is",
	"Warning: Your account was flagged for potentially high-risk cyber activity",
}

// isCodexContextualMessage reports whether a user message's content holds a
// contextual fragment, which makes the whole message context rather than a
// prompt.
func isCodexContextualMessage(content interface{}) bool {
	switch c := content.(type) {
	case string:
		return isCodexContextualFragment(c)
	case []interface{}:
		for _, item := range c {
			m, ok := item.(map[string]interface{})
			if !ok || m["type"] != "input_text" {
				continue
			}
			if text, ok := m["text"].(string); ok && isCodexContextualFragment(text) {
				return true
			}
		}
	}
	return false
}

func isCodexContextualFragment(text string) bool {
	trimmed := strings.TrimSpace(text)
	for _, markers := range codexMarkedFragments {
		if hasFoldPrefix(trimmed, markers[0]) && hasFoldSuffix(trimmed, markers[1]) {
			return true
		}
	}
	for _, prefix := range codexWarningPrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	// LegacyApplyPatchExecCommandWarning
	if strings.HasPrefix(trimmed, "Warning: apply_patch was requested via ") &&
		strings.HasSuffix(trimmed, "Use the apply_patch tool instead of exec_command.") {
		return true
	}
	return isCodexAdditionalContext(trimmed) || isCodexInternalContext(trimmed) || isCodexHookPrompt(trimmed)
}

// isCodexAdditionalContext matches <external_KEY>…</external_KEY>
// (context-fragments additional_context.rs).
func isCodexAdditionalContext(trimmed string) bool {
	rest, ok := strings.CutPrefix(trimmed, "<external_")
	if !ok {
		return false
	}
	key, valueAndClose, ok := strings.Cut(rest, ">")
	return ok && strings.HasSuffix(valueAndClose, "</external_"+key+">")
}

// isCodexInternalContext matches <codex_internal_context source="…">…
// </codex_internal_context> with a lowercase source name, and the legacy
// <goal_context> block (core/src/context/internal_model_context.rs).
func isCodexInternalContext(trimmed string) bool {
	if strings.HasPrefix(trimmed, "<goal_context>") && strings.HasSuffix(trimmed, "</goal_context>") {
		return true
	}
	rest, ok := strings.CutPrefix(trimmed, `<codex_internal_context source="`)
	if !ok {
		return false
	}
	source, bodyAndClose, ok := strings.Cut(rest, `">`)
	return ok && isCodexContextSource(source) && strings.HasSuffix(bodyAndClose, "</codex_internal_context>")
}

func isCodexContextSource(source string) bool {
	if source == "" || source[0] < 'a' || source[0] > 'z' {
		return false
	}
	for _, r := range source[1:] {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

// isCodexHookPrompt matches a <hook_prompt hook_run_id="…">…</hook_prompt>
// element with a non-empty run id (protocol/src/items.rs
// parse_hook_prompt_fragment).
func isCodexHookPrompt(trimmed string) bool {
	if !strings.HasPrefix(trimmed, "<hook_prompt") {
		return false
	}
	var hook struct {
		XMLName   xml.Name `xml:"hook_prompt"`
		HookRunID string   `xml:"hook_run_id,attr"`
		Text      string   `xml:",chardata"`
	}
	return xml.Unmarshal([]byte(trimmed), &hook) == nil && strings.TrimSpace(hook.HookRunID) != ""
}

func hasFoldPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

func hasFoldSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && strings.EqualFold(s[len(s)-len(suffix):], suffix)
}
