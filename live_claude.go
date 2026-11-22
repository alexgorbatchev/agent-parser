package parser

import (
	"bytes"
	"encoding/json"
	"fmt"
)

func (p *LiveParser) claudeDelta(raw json.RawMessage) ([]ParsedEvent, error) {
	var event struct {
		Type    string      `json:"type"`
		Message *rawMessage `json:"message"`
		Delta   struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			Thinking    string `json:"thinking"`
			PartialJSON string `json:"partial_json"`
		} `json:"delta"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		return nil, fmt.Errorf("decoding live Claude stream event: %w", err)
	}
	if event.Message != nil && event.Message.Model != "" {
		p.result.Model = event.Message.Model
	}
	if event.Type != "content_block_delta" {
		return nil, nil
	}
	switch event.Delta.Type {
	case "text_delta":
		return liveEvent("agent_message_delta", EventData{Delta: &event.Delta.Text, Native: bytes.Clone(raw)}), nil
	case "thinking_delta":
		return liveEvent("thinking_delta", EventData{Delta: &event.Delta.Thinking, Native: bytes.Clone(raw)}), nil
	case "input_json_delta":
		return liveEvent("tool_input_delta", EventData{Delta: &event.Delta.PartialJSON, Native: bytes.Clone(raw)}), nil
	}
	return nil, nil
}

func (p *LiveParser) claudeMessage(r liveRecord) ([]ParsedEvent, error) {
	if len(r.Message) == 0 || bytes.Equal(r.Message, []byte("null")) {
		return nil, nil
	}
	var message rawMessage
	if err := json.Unmarshal(r.Message, &message); err != nil {
		return nil, fmt.Errorf("decoding live Claude message: %w", err)
	}
	if message.Model != "" {
		p.result.Model = message.Model
	}
	var content []rawContentBlock
	data, err := json.Marshal(message.Content)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &content); err != nil {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return nil, fmt.Errorf("decoding live Claude message: %w", err)
		}
		content = []rawContentBlock{{Type: "text", Text: text}}
	}
	var events []ParsedEvent
	for _, block := range content {
		switch block.Type {
		case "text":
			kind := "agent_message"
			if r.Type == "user" {
				kind = "user_message"
			}
			events = append(events, liveEvent(kind, EventData{Content: &block.Text})...)
		case "thinking":
			events = append(events, liveEvent("thinking", EventData{Content: &block.Thinking})...)
		case "tool_use":
			events = append(events, liveEvent("tool_call", EventData{ToolCallID: &block.ID, ToolName: &block.Name, ToolInput: block.Input})...)
		case "tool_result":
			output := extractContentString(block.Content)
			events = append(events, liveEvent("tool_result", EventData{ToolCallID: &block.ToolUseID, ToolOutput: &output, IsError: block.IsError, Native: bytes.Clone(r.Message)})...)
		}
	}
	return events, nil
}
