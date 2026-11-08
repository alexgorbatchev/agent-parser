package parser

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
)

// parseCodexCompacted reports a compacted record (RolloutItem::Compacted):
// Codex replaced the conversation history with a summary. Paginated history
// also writes a ContextCompaction item for the same compaction, which is not
// reported again.
func parseCodexCompacted(payload json.RawMessage, ts int64) ([]ParsedEvent, error) {
	var compacted struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(payload, &compacted); err != nil {
		return nil, fmt.Errorf("unmarshaling compacted payload: %w", err)
	}
	return []ParsedEvent{{
		EventType: "compaction",
		Timestamp: ts,
		Data: EventData{
			Role:    stringPtr("assistant"),
			Content: optionalString(compacted.Message),
		},
	}}, nil
}

// codexWebSearchCallEvents reports a web search the model ran
// (ResponseItem::WebSearchCall). Paginated history reports the same search
// from its completed WebSearch item instead.
func codexWebSearchCallEvents(resp rawCodexResponseItemPayload, ts int64, p *CodexParser) []ParsedEvent {
	if p.isPaginated() {
		return nil
	}
	callID := optionalString(resp.ID)
	return []ParsedEvent{
		{
			EventType: "tool_call",
			Timestamp: ts,
			Data: EventData{
				Role:       stringPtr("assistant"),
				ToolName:   stringPtr("web_search"),
				ToolCallID: callID,
				ToolInput:  codexJSONObject(resp.Action),
			},
		},
		{
			EventType: "tool_result",
			Timestamp: ts,
			Data: EventData{
				Role:       stringPtr("toolResult"),
				ToolCallID: callID,
				IsError:    codexStatusFailed(resp.Status),
			},
		},
	}
}

// codexLocalShellCallEvents reports a local shell call
// (ResponseItem::LocalShellCall); its output follows as a function call
// output with the same call ID.
func codexLocalShellCallEvents(resp rawCodexResponseItemPayload, ts int64, p *CodexParser) []ParsedEvent {
	p.recordCallID(resp.CallID)
	var action struct {
		Command          []string `json:"command"`
		TimeoutMs        *int64   `json:"timeout_ms"`
		WorkingDirectory string   `json:"working_directory"`
	}
	var input map[string]interface{}
	if json.Unmarshal(resp.Action, &action) == nil && len(action.Command) > 0 {
		input = map[string]interface{}{"command": codexShellScript(action.Command), "argv": action.Command}
		if action.WorkingDirectory != "" {
			input["cwd"] = action.WorkingDirectory
		}
		if action.TimeoutMs != nil {
			input["timeout_ms"] = *action.TimeoutMs
		}
	}
	return []ParsedEvent{{
		EventType: "tool_call",
		Timestamp: ts,
		Data: EventData{
			Role:       stringPtr("assistant"),
			ToolName:   stringPtr("local_shell"),
			ToolCallID: optionalString(resp.CallID),
			ToolInput:  input,
		},
	}}
}

// codexImageGenerationCallEvents reports an image the model generated
// (ResponseItem::ImageGenerationCall); its result is the image as base64.
func codexImageGenerationCallEvents(resp rawCodexResponseItemPayload, ts int64) []ParsedEvent {
	callID := optionalString(resp.ID)
	var input map[string]interface{}
	if resp.RevisedPrompt != "" {
		input = map[string]interface{}{"revised_prompt": resp.RevisedPrompt}
	}
	return []ParsedEvent{
		{
			EventType: "tool_call",
			Timestamp: ts,
			Data: EventData{
				Role:       stringPtr("assistant"),
				ToolName:   stringPtr("image_generation"),
				ToolCallID: callID,
				ToolInput:  input,
			},
		},
		{
			EventType: "tool_result",
			Timestamp: ts,
			Data: EventData{
				Role:       stringPtr("toolResult"),
				ToolCallID: callID,
				Images:     codexBase64Images(resp.Result),
				IsError:    codexStatusFailed(resp.Status),
			},
		},
	}
}

// codexInterAgentMessageEvents reports a message another agent sent to this
// one (ResponseItem::AgentMessage), such as a new task, a message or a
// subagent's final answer. It is input to the agent's turn, like a prompt
// from another agent, and does not set the session title.
func codexInterAgentMessageEvents(resp rawCodexResponseItemPayload, ts int64, p *CodexParser) []ParsedEvent {
	text := extractContentString(resp.Content)
	if text == "" {
		return nil
	}
	return p.promptEvents([]ParsedEvent{{
		EventType: "turn_start",
		Timestamp: ts,
		Data: EventData{
			Role:       stringPtr("agent"),
			Content:    stringPtr(text),
			LastPrompt: stringPtr(text),
		},
	}})
}

// codexBase64Images returns a base64 image as an Image with its media type
// detected from its first bytes.
func codexBase64Images(data string) []Image {
	if data == "" {
		return nil
	}
	head, err := base64.StdEncoding.DecodeString(data[:min(len(data), 64)&^3])
	if err != nil {
		return nil
	}
	return []Image{{MediaType: http.DetectContentType(head), Data: data}}
}
