package parser

import (
	"encoding/json"
	"fmt"
)

// parseCodexResponseItem dispatches a response_item record by its payload type.
func parseCodexResponseItem(payload json.RawMessage, ts int64, p *CodexParser) ([]ParsedEvent, error) {
	var resp rawCodexResponseItemPayload
	if err := json.Unmarshal(payload, &resp); err != nil {
		return nil, fmt.Errorf("unmarshaling response_item payload: %w", err)
	}

	switch resp.Type {
	case "function_call", "custom_tool_call":
		return codexToolCallEvents(resp, ts, p), nil
	case "function_call_output", "custom_tool_call_output":
		return codexToolOutputEvents(resp, ts, p), nil
	case "message":
		return codexMessageEvents(resp, ts, p), nil
	case "reasoning":
		return codexThinkingEvents(resp.Summary, ts), nil
	case "tool_search_call":
		return []ParsedEvent{{
			EventType: "tool_call",
			Timestamp: ts,
			Data: EventData{
				Role:       stringPtr("assistant"),
				ToolName:   stringPtr("tool_search"),
				ToolCallID: optionalString(resp.CallID),
				ToolInput:  codexJSONObject(resp.Arguments),
			},
		}}, nil
	case "tool_search_output":
		return []ParsedEvent{{
			EventType: "tool_result",
			Timestamp: ts,
			Data: EventData{
				Role:       stringPtr("toolResult"),
				ToolCallID: optionalString(resp.CallID),
				ToolOutput: codexJSONText(resp.Tools),
			},
		}}, nil
	}
	return nil, nil
}

// codexQuestionTools are the tool names whose calls ask the user a question.
var codexQuestionTools = map[string]bool{
	"AskUserQuestion":          true,
	"ask_user":                 true,
	"ask_question":             true,
	"request_user_input":       true,
	"request_user_input_async": true,
}

func codexToolCallEvents(resp rawCodexResponseItemPayload, ts int64, p *CodexParser) []ParsedEvent {
	p.recordCallID(resp.CallID)
	inputMap := codexToolInput(resp)
	data := EventData{
		Role:             stringPtr("assistant"),
		ToolName:         stringPtr(resp.Name),
		ToolCallID:       optionalString(resp.CallID),
		ToolInput:        inputMap,
		FileModification: NormalizeFileModification(resp.Name, inputMap),
	}
	eventType := "tool_call"
	if codexQuestionTools[resp.Name] {
		eventType = "question_prompt"
		data.Question = stringPtr(codexQuestionText(inputMap))
	}
	return []ParsedEvent{{EventType: eventType, Timestamp: ts, Data: data}}
}

// codexToolInput decodes a call's arguments or input into a map, keeping text
// that is not a JSON object under "raw", and adds the input keys other
// harnesses use for the same values.
func codexToolInput(resp rawCodexResponseItemPayload) map[string]interface{} {
	var inputMap map[string]interface{}
	var arguments string
	if len(resp.Arguments) > 0 && json.Unmarshal(resp.Arguments, &arguments) != nil {
		arguments = ""
	}
	if arguments != "" {
		if err := json.Unmarshal([]byte(arguments), &inputMap); err != nil {
			inputMap = map[string]interface{}{"raw": arguments}
		}
	} else if resp.Input != nil {
		switch in := resp.Input.(type) {
		case map[string]interface{}:
			inputMap = in
		case string:
			if err := json.Unmarshal([]byte(in), &inputMap); err != nil {
				inputMap = map[string]interface{}{"raw": in}
			}
		}
	}
	if inputMap == nil {
		return nil
	}

	switch resp.Name {
	case "exec_command":
		if cmdVal, ok := inputMap["cmd"]; ok && inputMap["command"] == nil {
			inputMap["command"] = cmdVal
		}
	case "spawn_agent":
		if msgVal, ok := inputMap["message"]; ok && inputMap["prompt"] == nil {
			inputMap["prompt"] = msgVal
		}
		if typeVal, ok := inputMap["agent_type"]; ok && inputMap["description"] == nil {
			inputMap["description"] = typeVal
		}
	}
	return inputMap
}

// codexToolOutputEvents reports a tool result. Without a status header, the
// outcome comes from the completed item Codex recorded for the call, if any.
func codexToolOutputEvents(resp rawCodexResponseItemPayload, ts int64, p *CodexParser) []ParsedEvent {
	recorded := p.takeCallOutcome(resp.CallID)
	failed := codexToolFailed(resp.Output.head)
	if failed == nil {
		failed = recorded
	}
	return []ParsedEvent{{
		EventType: "tool_result",
		Timestamp: ts,
		Data: EventData{
			Role:       stringPtr("toolResult"),
			ToolCallID: optionalString(resp.CallID),
			ToolOutput: stringPtr(resp.Output.text),
			IsError:    failed,
			Images:     resp.Output.images,
		},
	}}
}

func codexMessageEvents(resp rawCodexResponseItemPayload, ts int64, p *CodexParser) []ParsedEvent {
	contentStr := extractContentString(resp.Content)
	switch resp.Role {
	case "user":
		if isCodexContextualMessage(resp.Content) {
			return nil
		}
		// Paginated history reports the prompt from the completed UserMessage
		// item that follows; keep this message's images for it.
		if p.isPaginated() {
			p.recordUserImages(codexImages(resp.Content))
			return nil
		}
		return p.promptEvents(codexUserPromptEvents(contentStr, codexImages(resp.Content), ts))
	case "assistant":
		// Paginated history reports the message from its completed
		// AgentMessage item instead.
		if p.isPaginated() {
			return nil
		}
		return codexAgentMessageEvents(contentStr, ts)
	}
	return nil
}
