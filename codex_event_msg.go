package parser

import (
	"encoding/json"
	"fmt"
	"strings"
)

// parseCodexEventMsg dispatches an event_msg record by its payload type.
func parseCodexEventMsg(payload json.RawMessage, ts int64, p *CodexParser) ([]ParsedEvent, error) {
	var evt rawCodexEventMsgPayload
	if err := json.Unmarshal(payload, &evt); err != nil {
		return nil, fmt.Errorf("unmarshaling event_msg payload: %w", err)
	}

	switch evt.Type {
	case "item_completed":
		return parseCodexCompletedItem(evt.Item, ts)
	case "user_message":
		return codexUserPromptEvents(evt.Message, nil, ts), nil
	case "agent_message":
		return codexAgentMessageEvents(evt.Message, ts), nil
	case "reasoning":
		return codexThinkingEvents(evt.Summary, ts), nil
	case "token_count":
		return codexTokenCountEvents(evt, ts, p), nil
	case "task_started":
		startedAt, err := parseCodexTaskTimestamp(evt.StartedAt, ts)
		if err != nil {
			return nil, fmt.Errorf("parsing task started_at: %w", err)
		}
		return []ParsedEvent{{
			EventType: "turn_start",
			Timestamp: startedAt,
			Data: EventData{
				Role: stringPtr("assistant"),
			},
		}}, nil
	case "task_complete":
		completedAt, err := parseCodexTaskTimestamp(evt.CompletedAt, ts)
		if err != nil {
			return nil, fmt.Errorf("parsing task completed_at: %w", err)
		}
		var contentPtr *string
		if evt.LastAgentMessage != "" {
			contentPtr = stringPtr(evt.LastAgentMessage)
		}
		return []ParsedEvent{{
			EventType: "turn_end",
			Timestamp: completedAt,
			Data: EventData{
				Role:    stringPtr("assistant"),
				Content: contentPtr,
			},
		}}, nil
	case "error":
		return codexErrorEvents(evt, ts, p), nil
	}
	return nil, nil
}

// codexUserPromptEvents emits turn_start for a user prompt that still has text
// or images once injected context is removed.
func codexUserPromptEvents(text string, images []Image, ts int64) []ParsedEvent {
	cleanPrompt := CleanCodexPrompt(text)
	if cleanPrompt == "" && len(images) == 0 {
		return nil
	}
	var titlePtr *string
	if title, hasTitle := ExtractCodexTitleFromPrompt(cleanPrompt); hasTitle {
		titlePtr = stringPtr(title)
	}
	return []ParsedEvent{{
		EventType: "turn_start",
		Timestamp: ts,
		Data: EventData{
			Role:       stringPtr("user"),
			Content:    stringPtr(cleanPrompt),
			LastPrompt: stringPtr(cleanPrompt),
			Title:      titlePtr,
			Images:     images,
		},
	}}
}

func codexAgentMessageEvents(text string, ts int64) []ParsedEvent {
	if text == "" {
		return nil
	}
	return []ParsedEvent{{
		EventType: "agent_message",
		Timestamp: ts,
		Data: EventData{
			Role:    stringPtr("assistant"),
			Content: stringPtr(text),
		},
	}}
}

// codexThinkingEvents joins the non-empty reasoning summary parts into one
// thinking event.
func codexThinkingEvents(summary []rawCodexSummaryPart, ts int64) []ParsedEvent {
	var sb strings.Builder
	for _, item := range summary {
		if item.Text == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(item.Text)
	}
	if sb.Len() == 0 {
		return nil
	}
	return []ParsedEvent{{
		EventType: "thinking",
		Timestamp: ts,
		Data: EventData{
			Role:    stringPtr("assistant"),
			Content: stringPtr(sb.String()),
		},
	}}
}

func codexTokenCountEvents(evt rawCodexEventMsgPayload, ts int64, p *CodexParser) []ParsedEvent {
	resetAtPtr, retrySecPtr := selectCodexRateLimitReset(evt.RateLimits, ts)
	p.recordTokenCountRateLimits(resetAtPtr, retrySecPtr, evt.RateLimits, ts)

	// Quota-only updates do not establish that a turn has completed.
	if evt.Info == nil {
		return nil
	}
	info := *evt.Info

	chosen := info.LastTokenUsage
	if chosen.TotalTokens == 0 && chosen.InputTokens == 0 && chosen.OutputTokens == 0 {
		chosen = info.TotalTokenUsage
	}

	tot := chosen.TotalTokens
	if tot == 0 {
		tot = chosen.InputTokens + chosen.OutputTokens
	}

	// Input counts only fresh tokens, as for the other harnesses: Codex's
	// cached and cache-write counts are parts of input_tokens.
	freshInput := chosen.InputTokens - chosen.CachedInputTokens - chosen.CacheWriteInputTokens
	if freshInput < 0 {
		freshInput = 0
	}

	activeModel, cw := p.usageContext(info.ModelContextWindow)
	var cwPtr *int64
	if cw > 0 {
		cwPtr = &cw
	}
	var modelPtr *string
	if activeModel != "" {
		modelPtr = stringPtr(activeModel)
	}

	return []ParsedEvent{{
		EventType: "turn_end",
		Timestamp: ts,
		Data: EventData{
			Role:               stringPtr("assistant"),
			Model:              modelPtr,
			ModelContextWindow: cwPtr,
			Usage: &TokenUsage{
				Input:      freshInput,
				Output:     chosen.OutputTokens,
				CacheRead:  chosen.CachedInputTokens,
				CacheWrite: chosen.CacheWriteInputTokens,
				Reasoning:  chosen.ReasoningOutputTokens,
				Total:      tot,
			},
			RateLimitResetAt:  resetAtPtr,
			RetryAfterSeconds: retrySecPtr,
		},
	}}
}

func codexErrorEvents(evt rawCodexEventMsgPayload, ts int64, p *CodexParser) []ParsedEvent {
	isRL, httpStatusCode := classifyCodexErrorInfo(evt.CodexErrorInfo)
	if isRL == nil && httpStatusCode != nil {
		isRL = boolPtr(*httpStatusCode == 429)
	}

	var lineResetAt, lineRetrySec *int64
	if evt.RateLimits != nil {
		lineResetAt, lineRetrySec = selectCodexRateLimitReset(evt.RateLimits, ts)
	}
	p.recordErrorRateLimits(evt.RateLimits != nil, lineResetAt, lineRetrySec, ts)

	var errResetAt, errRetrySec *int64
	switch {
	case isRL != nil && !*isRL:
		// Explicit non-rate-limit error: do not attach cached rate-limit metadata.
	case isRL != nil:
		// Explicit rate-limit error: attach line metadata, else the cached reset.
		errResetAt, errRetrySec = lineResetAt, lineRetrySec
		if errResetAt == nil {
			errResetAt, errRetrySec = p.pendingRateLimitReset(ts)
		}
	case lineResetAt != nil:
		// Unclassified error: only the record's own rate-limit data marks it as
		// a rate limit, never an unexpired cached reset.
		isRL = boolPtr(true)
		errResetAt, errRetrySec = lineResetAt, lineRetrySec
	}

	return []ParsedEvent{{
		EventType: "error",
		Timestamp: ts,
		Data: EventData{
			Role:              stringPtr("assistant"),
			Content:           stringPtr(evt.Message),
			IsError:           boolPtr(true),
			IsRateLimit:       isRL,
			RateLimitResetAt:  errResetAt,
			RetryAfterSeconds: errRetrySec,
		},
	}}
}

// classifyCodexErrorInfo reads whether a codex_error_info value names a rate
// limit, and any HTTP status code it carries.
func classifyCodexErrorInfo(errorInfo interface{}) (*bool, *int) {
	var isRL *bool
	var httpStatusCode *int

	setFromStatus := func(v interface{}) {
		if code := extractInt(v); code != nil {
			httpStatusCode = code
			isRL = boolPtr(*code == 429)
		}
	}

	switch info := errorInfo.(type) {
	case string:
		if rl, known := codexErrorKindIsRateLimit(info); known {
			isRL = boolPtr(rl)
		}
	case map[string]interface{}:
		for k, v := range info {
			switch k {
			case "type":
				if s, ok := v.(string); ok {
					if rl, known := codexErrorKindIsRateLimit(s); known {
						isRL = boolPtr(rl)
					}
				}
			case "HttpConnectionFailed", "http_connection_failed":
				if subMap, ok := v.(map[string]interface{}); ok {
					if sc, ok := subMap["http_status_code"]; ok && sc != nil {
						setFromStatus(sc)
					}
				}
			case "http_status_code":
				setFromStatus(v)
			default:
				if rl, known := codexErrorKindIsRateLimit(k); known {
					isRL = boolPtr(rl)
				}
			}
		}
	}
	return isRL, httpStatusCode
}

// codexErrorKindIsRateLimit reports whether an error kind is a rate limit, and
// whether the kind is one this parser classifies at all.
func codexErrorKindIsRateLimit(kind string) (isRateLimit bool, known bool) {
	switch kind {
	case "RateLimitExceeded", "rate_limit_exceeded":
		return true, true
	case "UsageLimitExceeded", "usage_limit_exceeded",
		"ServerOverloaded", "server_overloaded":
		return false, true
	}
	return false, false
}
