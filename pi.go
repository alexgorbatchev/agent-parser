// Package parser provides streaming log parsers for AI agent harnesses.
//
// Attribution:
// Derived from and inspired by Nick Nisi Sessions (https://github.com/nicknisi/sessions)
// and OpenUsage (https://github.com/janekbaraniewski/openusage).
package parser

import (
	"encoding/json"
	"fmt"
	"strings"
)

type rawPiContentBlock struct {
	Type             string                 `json:"type"` // "text" | "thinking" | "toolCall" | "tool_call" | "toolResult" | "tool_result"
	Text             string                 `json:"text,omitempty"`
	Thinking         string                 `json:"thinking,omitempty"`
	ToolName         string                 `json:"toolName,omitempty"`
	ToolNameSnake    string                 `json:"tool_name,omitempty"`
	ToolCallID       string                 `json:"toolCallId,omitempty"`
	ToolCallIDSnake  string                 `json:"tool_call_id,omitempty"`
	Name             string                 `json:"name,omitempty"`
	ID               string                 `json:"id,omitempty"`
	Input            map[string]interface{} `json:"input,omitempty"`
	Parameters       map[string]interface{} `json:"parameters,omitempty"`
	Arguments        map[string]interface{} `json:"arguments,omitempty"`
	Content          interface{}            `json:"content,omitempty"`
	IsError          *bool                  `json:"isError,omitempty"`
	IsErrorSnake     *bool                  `json:"is_error,omitempty"`
}

type rawPiCostBreakdown struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total      float64 `json:"total"`
}

type rawPiUsage struct {
	Input       int64    `json:"input"`
	Output      int64    `json:"output"`
	CacheRead   int64    `json:"cacheRead"`
	CacheWrite  int64    `json:"cacheWrite"`
	Reasoning   int64    `json:"reasoning"`
	Total       int64    `json:"total"`
	TotalTokens int64    `json:"totalTokens,omitempty"`
	Cost        *float64 `json:"cost,omitempty"`
}

func (u *rawPiUsage) UnmarshalJSON(data []byte) error {
	type rawUsageAlias rawPiUsage
	var aux struct {
		rawUsageAlias
		RawCost json.RawMessage `json:"cost"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*u = rawPiUsage(aux.rawUsageAlias)

	if len(aux.RawCost) > 0 && string(aux.RawCost) != "null" {
		var numCost float64
		if err := json.Unmarshal(aux.RawCost, &numCost); err == nil {
			u.Cost = &numCost
		} else {
			var costObj rawPiCostBreakdown
			if err := json.Unmarshal(aux.RawCost, &costObj); err == nil {
				u.Cost = &costObj.Total
			}
		}
	}
	return nil
}

type rawPiMessage struct {
	Role            string      `json:"role"` // "user" | "assistant" | "toolResult" | "tool_result" | "system"
	Model           string      `json:"model,omitempty"`
	Provider        string      `json:"provider,omitempty"`
	Content         interface{} `json:"content,omitempty"`
	Usage           *rawPiUsage `json:"usage,omitempty"`
	StopReason      string      `json:"stopReason,omitempty"`
	ErrorMessage    string      `json:"errorMessage,omitempty"`
	ToolName        string      `json:"toolName,omitempty"`
	ToolNameSnake   string      `json:"tool_name,omitempty"`
	ToolCallID      string      `json:"toolCallId,omitempty"`
	ToolCallIDSnake string      `json:"tool_call_id,omitempty"`
	IsError         *bool       `json:"isError,omitempty"`
	IsErrorSnake    *bool       `json:"is_error,omitempty"`
}

type rawPiLine struct {
	Type          string        `json:"type"` // "session" | "message" | "model_change" | "thinking_level_change" | "custom_message" | "label" | "session_info"
	ID            string        `json:"id"`
	ParentID      *string       `json:"parentId"`
	Timestamp     interface{}   `json:"timestamp"`
	CWD           string        `json:"cwd,omitempty"`
	Message       *rawPiMessage `json:"message,omitempty"`
	Model         string        `json:"model,omitempty"`
	ModelID       string        `json:"modelId,omitempty"`
	Model_ID      string        `json:"model_id,omitempty"`
	Provider      string        `json:"provider,omitempty"`
	Title         string        `json:"title,omitempty"`
	Name          string        `json:"name,omitempty"`
	Label         string        `json:"label,omitempty"`
	TargetID      string        `json:"targetId,omitempty"`
	ThinkingLevel string        `json:"thinkingLevel,omitempty"`
}

// ExtractPiTitleFromPrompt extracts a session title / task description from user prompt text.
func ExtractPiTitleFromPrompt(promptText string) (string, bool) {
	trimmed := strings.TrimSpace(promptText)
	if trimmed == "" {
		return "", false
	}
	firstLine := strings.Split(trimmed, "\n")[0]
	firstLine = strings.TrimSpace(firstLine)
	if len(firstLine) > 100 {
		firstLine = firstLine[:100]
	}
	return firstLine, firstLine != ""
}

func parsePiContentBlocks(content interface{}) []rawPiContentBlock {
	if content == nil {
		return nil
	}

	switch v := content.(type) {
	case string:
		return []rawPiContentBlock{{Type: "text", Text: v}}
	case []interface{}:
		var blocks []rawPiContentBlock
		for _, item := range v {
			if m, ok := item.(map[string]interface{}); ok {
				var b rawPiContentBlock
				data, err := json.Marshal(m)
				if err == nil && json.Unmarshal(data, &b) == nil {
					blocks = append(blocks, b)
				}
			}
		}
		return blocks
	case map[string]interface{}:
		var b rawPiContentBlock
		data, err := json.Marshal(v)
		if err == nil && json.Unmarshal(data, &b) == nil {
			return []rawPiContentBlock{b}
		}
	}
	return nil
}

// ParsePiLine parses a single JSONL line from a Pi session file.
func ParsePiLine(line []byte, fallbackCWD string) ([]ParsedEvent, error) {
	if len(line) == 0 {
		return nil, nil
	}

	var raw rawPiLine
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, fmt.Errorf("unmarshaling pi line: %w", err)
	}

	ts := parseTimestamp(raw.Timestamp)
	cwd := raw.CWD
	if cwd == "" {
		cwd = fallbackCWD
	}

	var events []ParsedEvent

	switch raw.Type {
	case "session":
		// Session header
		sessionModel := raw.ModelID
		if sessionModel == "" {
			sessionModel = raw.Model
		}
		if sessionModel == "" {
			sessionModel = raw.Model_ID
		}
		var sessModelPtr *string
		if sessionModel != "" {
			sessModelPtr = stringPtr(sessionModel)
		}

		events = append(events, ParsedEvent{
			EventType: "run_started",
			Timestamp: ts,
			CWD:       cwd,
			Data: EventData{
				Title: stringPtr("Pi Session " + raw.ID),
				Model: sessModelPtr,
			},
		})

	case "label", "session_info":
		titleText := raw.Label
		if titleText == "" {
			titleText = raw.Title
		}
		if titleText == "" {
			titleText = raw.Name
		}
		if titleText != "" {
			events = append(events, ParsedEvent{
				EventType: "title_change",
				Timestamp: ts,
				CWD:       cwd,
				Data: EventData{
					Title: stringPtr(titleText),
				},
			})
		}

	case "model_change":
		modelName := raw.ModelID
		if modelName == "" {
			modelName = raw.Model
		}
		if modelName == "" {
			modelName = raw.Model_ID
		}
		if modelName != "" {
			events = append(events, ParsedEvent{
				EventType: "model_change",
				Timestamp: ts,
				CWD:       cwd,
				Data: EventData{
					Model: stringPtr(modelName),
				},
			})
		}

	case "message":
		if raw.Message == nil {
			break
		}
		msg := raw.Message

		switch msg.Role {
		case "user":
			blocks := parsePiContentBlocks(msg.Content)
			var userTexts []string
			for _, b := range blocks {
				if b.Text != "" {
					userTexts = append(userTexts, b.Text)
				}
			}
			fullPrompt := strings.Join(userTexts, "\n")
			if fullPrompt == "" {
				fullPrompt = extractContentString(msg.Content)
			}

			title, hasTitle := ExtractPiTitleFromPrompt(fullPrompt)
			var titlePtr *string
			if hasTitle {
				titlePtr = stringPtr(title)
			}

			events = append(events, ParsedEvent{
				EventType: "turn_start",
				Timestamp: ts,
				CWD:       cwd,
				Data: EventData{
					Title:      titlePtr,
					Role:       stringPtr("user"),
					Content:    stringPtr(fullPrompt),
					LastPrompt: stringPtr(fullPrompt),
				},
			})

		case "toolResult", "tool_result":
			output := extractContentString(msg.Content)
			isErr := false
			if msg.IsError != nil {
				isErr = *msg.IsError
			} else if msg.IsErrorSnake != nil {
				isErr = *msg.IsErrorSnake
			}

			toolCallIDStr := msg.ToolCallID
			if toolCallIDStr == "" {
				toolCallIDStr = msg.ToolCallIDSnake
			}
			var toolCallID *string
			if toolCallIDStr != "" {
				toolCallID = stringPtr(toolCallIDStr)
			}
			toolNameStr := msg.ToolName
			if toolNameStr == "" {
				toolNameStr = msg.ToolNameSnake
			}
			var toolName *string
			if toolNameStr != "" {
				toolName = stringPtr(toolNameStr)
			}

			events = append(events, ParsedEvent{
				EventType: "tool_result",
				Timestamp: ts,
				CWD:       cwd,
				Data: EventData{
					Role:       stringPtr("toolResult"),
					ToolName:   toolName,
					ToolCallID: toolCallID,
					ToolOutput: stringPtr(output),
					IsError:    boolPtr(isErr),
				},
			})

		case "assistant":
			var modelPtr *string
			if msg.Model != "" {
				modelPtr = stringPtr(msg.Model)
			}
			blocks := parsePiContentBlocks(msg.Content)
			var textParts []string

			for _, b := range blocks {
				switch b.Type {
				case "thinking":
					thought := b.Thinking
					if thought == "" {
						thought = b.Text
					}
					if thought != "" {
						events = append(events, ParsedEvent{
							EventType: "thinking",
							Timestamp: ts,
							CWD:       cwd,
							Data: EventData{
								Role:    stringPtr("assistant"),
								Content: stringPtr(thought),
								Model:   modelPtr,
							},
						})
					}

				case "text":
					if b.Text != "" {
						textParts = append(textParts, b.Text)
					}

				case "toolCall", "tool_call", "tool_use":
					toolName := b.ToolName
					if toolName == "" {
						toolName = b.ToolNameSnake
					}
					if toolName == "" {
						toolName = b.Name
					}
					callID := b.ToolCallID
					if callID == "" {
						callID = b.ToolCallIDSnake
					}
					if callID == "" {
						callID = b.ID
					}

					var callIDPtr *string
					if callID != "" {
						callIDPtr = stringPtr(callID)
					}

					params := b.Input
					if params == nil {
						params = b.Parameters
					}
					if params == nil {
						params = b.Arguments
					}

					if toolName == "AskUserQuestion" || toolName == "ask_user" || toolName == "ask_question" {
						qText := ""
						if params != nil {
							if q, ok := params["question"].(string); ok {
								qText = q
							} else if p, ok := params["prompt"].(string); ok {
								qText = p
							}
						}
						fileMod := NormalizeFileModification(toolName, params)
						events = append(events, ParsedEvent{
							EventType: "question_prompt",
							Timestamp: ts,
							CWD:       cwd,
							Data: EventData{
								Role:             stringPtr("assistant"),
								ToolName:         stringPtr(toolName),
								ToolCallID:       callIDPtr,
								ToolInput:        params,
								FileModification: fileMod,
								Question:         stringPtr(qText),
								Model:            modelPtr,
							},
						})
					} else {
						fileMod := NormalizeFileModification(toolName, params)
						events = append(events, ParsedEvent{
							EventType: "tool_call",
							Timestamp: ts,
							CWD:       cwd,
							Data: EventData{
								Role:             stringPtr("assistant"),
								ToolName:         stringPtr(toolName),
								ToolCallID:       callIDPtr,
								ToolInput:        params,
								FileModification: fileMod,
								Model:            modelPtr,
							},
						})
					}
				}
			}

			// Usage & Turn End
			var usage *TokenUsage
			var harnessCost *float64

			if msg.Usage != nil {
				u := msg.Usage
				total := u.Total
				if total == 0 {
					total = u.TotalTokens
				}
				if total == 0 {
					total = u.Input + u.Output + u.CacheRead + u.CacheWrite + u.Reasoning
				}
				usage = &TokenUsage{
					Input:      u.Input,
					Output:     u.Output,
					CacheRead:  u.CacheRead,
					CacheWrite: u.CacheWrite,
					Reasoning:  u.Reasoning,
					Total:      total,
				}
				harnessCost = u.Cost
			}

			var stopReason *string
			if msg.StopReason != "" {
				stopReason = stringPtr(msg.StopReason)
			}

			var contentPtr *string
			if len(textParts) > 0 {
				contentPtr = stringPtr(strings.Join(textParts, "\n"))
			}

			events = append(events, ParsedEvent{
				EventType: "turn_end",
				Timestamp: ts,
				CWD:       cwd,
				Data: EventData{
					Role:           stringPtr("assistant"),
					Content:        contentPtr,
					Model:          modelPtr,
					StopReason:     stopReason,
					Usage:          usage,
					RawHarnessCost: harnessCost,
				},
			})

			if msg.StopReason == "error" {
				errText := msg.ErrorMessage
				if errText == "" {
					if len(textParts) > 0 {
						errText = strings.Join(textParts, "\n")
					}
					if errText == "" {
						errText = "error"
					}
				}

				events = append(events, ParsedEvent{
					EventType: "error",
					Timestamp: ts,
					CWD:       cwd,
					Data: EventData{
						Role:    stringPtr("assistant"),
						Content: stringPtr(errText),
						Model:   modelPtr,
						IsError: boolPtr(true),
					},
				})
			}
		}
	}

	return events, nil
}
