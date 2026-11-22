package parser

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

type agyLiveUsage struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	ThinkingTokens      int64 `json:"thinking_tokens"`
	CacheReadTokens     int64 `json:"cache_read_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_tokens"`
	TotalTokens         int64 `json:"total_tokens"`
}

func (u agyLiveUsage) normalized() *TokenUsage {
	total := u.TotalTokens
	if total == 0 {
		total = u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheCreationTokens
	}
	return &TokenUsage{
		Input:      u.InputTokens,
		Output:     u.OutputTokens,
		CacheRead:  u.CacheReadTokens,
		CacheWrite: u.CacheCreationTokens,
		Reasoning:  u.ThinkingTokens,
		Total:      total,
	}
}

type agyLiveRecord struct {
	Event          string          `json:"event"`
	ConversationID string          `json:"conversation_id"`
	Init           json.RawMessage `json:"init"`
	StepUpdate     json.RawMessage `json:"step_update"`
	Result         json.RawMessage `json:"result"`
}

func (p *LiveParser) agy(line []byte) ([]ParsedEvent, error) {
	var r agyLiveRecord
	if err := json.Unmarshal(line, &r); err != nil {
		return nil, fmt.Errorf("decoding live agy record: %w", err)
	}
	if r.Event == "" {
		return nil, fmt.Errorf("live agy record is missing its event")
	}
	if r.ConversationID != "" {
		p.result.SessionID = r.ConversationID
	}
	switch r.Event {
	case "init":
		return p.agyInit(r.Init)
	case "step_update":
		return p.agyStepUpdate(r.StepUpdate)
	case "result":
		return p.agyResult(r.Result)
	default:
		return nil, nil
	}
}

func (p *LiveParser) agyInit(raw json.RawMessage) ([]ParsedEvent, error) {
	var initPayload struct {
		CWD   string `json:"cwd"`
		Model string `json:"model"`
	}
	if len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		if err := json.Unmarshal(raw, &initPayload); err != nil {
			return nil, fmt.Errorf("decoding live agy init payload: %w", err)
		}
	}
	if initPayload.Model != "" {
		p.result.Model = initPayload.Model
	}
	events := liveEvent("run_started", EventData{Model: textPointer(initPayload.Model)})
	events[0].CWD = initPayload.CWD
	return events, nil
}

func (p *LiveParser) agyStepUpdate(raw json.RawMessage) ([]ParsedEvent, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var step struct {
		ConversationID string        `json:"conversation_id"`
		StepIndex      int           `json:"step_index"`
		State          string        `json:"state"`
		StepType       string        `json:"step_type"`
		ToolName       string        `json:"tool_name"`
		TextDelta      *string       `json:"text_delta"`
		ThinkingDelta  *string       `json:"thinking_delta"`
		Usage          *agyLiveUsage `json:"usage"`
		ToolInfo       *struct {
			Name       string                 `json:"name"`
			Parameters map[string]interface{} `json:"parameters"`
			Output     *string                `json:"output"`
			Error      *struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"tool_info"`
		SubagentInfo json.RawMessage `json:"subagent_info"`
	}
	if err := json.Unmarshal(raw, &step); err != nil {
		return nil, fmt.Errorf("decoding live agy step_update: %w", err)
	}
	if step.ConversationID != "" {
		p.result.SessionID = step.ConversationID
	}
	switch step.StepType {
	case "user_input":
		p.ended = false
		return liveEvent("turn_start", EventData{}), nil
	case "agent_response":
		var events []ParsedEvent
		if step.ThinkingDelta != nil && *step.ThinkingDelta != "" {
			events = append(events, liveEvent("thinking_delta", EventData{Delta: step.ThinkingDelta, Native: bytes.Clone(raw)})...)
		}
		if step.TextDelta != nil && *step.TextDelta != "" {
			events = append(events, liveEvent("agent_message_delta", EventData{Delta: step.TextDelta, Native: bytes.Clone(raw)})...)
		}
		return events, nil
	case "tool":
		return p.agyToolStep(raw, step.StepIndex, step.State, step.ToolName, step.ToolInfo, step.SubagentInfo), nil
	default:
		return nil, nil
	}
}

func (p *LiveParser) agyToolStep(
	raw json.RawMessage,
	stepIndex int,
	state, toolName string,
	toolInfo *struct {
		Name       string                 `json:"name"`
		Parameters map[string]interface{} `json:"parameters"`
		Output     *string                `json:"output"`
		Error      *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	},
	subagentInfo json.RawMessage,
) []ParsedEvent {
	if toolName == "" && toolInfo != nil {
		toolName = toolInfo.Name
	}
	if toolName == "" && len(subagentInfo) > 0 && !bytes.Equal(subagentInfo, []byte("null")) {
		toolName = "invoke_subagent"
	}
	callID := "step-" + strconv.Itoa(stepIndex)
	var input map[string]interface{}
	if toolInfo != nil && len(toolInfo.Parameters) > 0 {
		input = toolInfo.Parameters
	}
	if p.agyToolSteps == nil {
		p.agyToolSteps = make(map[int]bool)
	}

	var events []ParsedEvent
	if !p.agyToolSteps[stepIndex] {
		p.agyToolSteps[stepIndex] = true
		callData := EventData{
			ToolCallID: &callID,
			ToolName:   textPointer(toolName),
			ToolInput:  input,
			Native:     bytes.Clone(raw),
		}
		if fm := NormalizeFileModification(toolName, input); fm != nil {
			callData.FileModification = fm
		}
		events = append(events, liveEvent("tool_call", callData)...)
	}
	if state == "DONE" {
		resultData := EventData{
			ToolCallID: &callID,
			ToolName:   textPointer(toolName),
			Native:     bytes.Clone(raw),
		}
		isErr := false
		if toolInfo != nil {
			if toolInfo.Output != nil {
				resultData.ToolOutput = toolInfo.Output
			}
			if toolInfo.Error != nil {
				isErr = true
				if resultData.ToolOutput == nil && toolInfo.Error.Message != "" {
					msg := toolInfo.Error.Message
					resultData.ToolOutput = &msg
				}
			}
		}
		resultData.IsError = boolPtr(isErr)
		events = append(events, liveEvent("tool_result", resultData)...)
	} else if len(events) == 0 {
		events = append(events, liveEvent("tool_update", EventData{
			ToolCallID: &callID,
			ToolName:   textPointer(toolName),
			Native:     bytes.Clone(raw),
		})...)
	}
	return events
}

func (p *LiveParser) agyResult(raw json.RawMessage) ([]ParsedEvent, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, fmt.Errorf("live agy result record is missing its payload")
	}
	var res struct {
		ConversationID   string          `json:"conversation_id"`
		Status           string          `json:"status"`
		Response         *string         `json:"response"`
		Error            string          `json:"error"`
		StructuredOutput json.RawMessage `json:"structured_output"`
		Usage            *agyLiveUsage   `json:"usage"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("decoding live agy result: %w", err)
	}
	if res.ConversationID != "" {
		p.result.SessionID = res.ConversationID
	}
	p.result.NativeSubtype = res.Status
	p.result.IsError = res.Status != "SUCCESS"
	p.result.ErrorMessage = res.Error
	p.result.Text = ""
	p.result.HasText = false
	if res.Response != nil && (res.Status == "SUCCESS" || *res.Response != "") {
		p.result.Text = *res.Response
		p.result.HasText = true
	}
	p.result.StructuredOutput = bytes.Clone(res.StructuredOutput)
	if res.Usage != nil {
		p.result.Usage = res.Usage.normalized()
	}
	return p.finish(), nil
}
