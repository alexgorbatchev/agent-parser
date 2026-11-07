package parser

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
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
		return p.promptEvents(codexUserPromptEvents(evt.Message, nil, ts)), nil
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
		return p.beginTurn(ParsedEvent{
			EventType: "turn_start",
			Timestamp: startedAt,
			Data: EventData{
				Role: stringPtr("assistant"),
			},
		}), nil
	case "task_complete":
		completedAt, err := parseCodexTaskTimestamp(evt.CompletedAt, ts)
		if err != nil {
			return nil, fmt.Errorf("parsing task completed_at: %w", err)
		}
		var contentPtr *string
		if evt.LastAgentMessage != "" {
			contentPtr = stringPtr(evt.LastAgentMessage)
		}
		return codexTurnEndEvents(evt, completedAt, p, EventData{
			Role:    stringPtr("assistant"),
			Content: contentPtr,
		}), nil
	case "turn_aborted":
		abortedAt, err := parseCodexTaskTimestamp(evt.CompletedAt, ts)
		if err != nil {
			return nil, fmt.Errorf("parsing turn_aborted completed_at: %w", err)
		}
		return codexTurnEndEvents(evt, abortedAt, p, EventData{
			Role:       stringPtr("assistant"),
			StopReason: optionalString(evt.Reason),
		}), nil
	case "error":
		return codexErrorEvents(evt, ts, p), nil
	}
	return nil, nil
}

// codexTurnEndEvents ends the open turn: its pending start if no event
// reported it yet, an error event when the turn failed, then turn_end.
func codexTurnEndEvents(evt rawCodexEventMsgPayload, ts int64, p *CodexParser, data EventData) []ParsedEvent {
	events := p.endTurn()
	if evt.Error != nil {
		events = append(events, codexErrorEvents(rawCodexEventMsgPayload{
			Message:        evt.Error.Message,
			CodexErrorInfo: evt.Error.CodexErrorInfo,
		}, ts, p)...)
	}
	return append(events, ParsedEvent{EventType: "turn_end", Timestamp: ts, Data: data})
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

// codexTokenCountEvents turns a token_count record into a usage event.
//
// Codex sends token_count after each model response, again unchanged when rate
// limits refresh, and with an estimated total in place of the last response's
// usage when it fills usage up to the context window
// (TokenUsageInfo::fill_to_context_window) or re-estimates the context
// (Session::recompute_token_usage). None of these mark the end of a turn:
// task_complete does.
func codexTokenCountEvents(evt rawCodexEventMsgPayload, ts int64, p *CodexParser) []ParsedEvent {
	resetAtPtr, retrySecPtr := selectCodexRateLimitReset(evt.RateLimits, ts)
	p.recordTokenCountRateLimits(resetAtPtr, retrySecPtr, evt.RateLimits, ts)

	// Quota-only updates report no token usage.
	if evt.Info == nil {
		return nil
	}
	info := *evt.Info
	last := info.LastTokenUsage
	if p.isRepeatedUsage(info.TotalTokenUsage) || last.InputTokens == 0 && last.OutputTokens == 0 {
		// A re-send, or an estimate that reports only a total: no model
		// response consumed these tokens.
		return nil
	}

	tot := last.TotalTokens
	if tot == 0 {
		tot = last.InputTokens + last.OutputTokens
	}

	// Input counts only fresh tokens, as for the other harnesses: Codex's
	// cached and cache-write counts are parts of input_tokens.
	freshInput := last.InputTokens - last.CachedInputTokens - last.CacheWriteInputTokens
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
		EventType: "usage",
		Timestamp: ts,
		Data: EventData{
			Role:               stringPtr("assistant"),
			Model:              modelPtr,
			ModelContextWindow: cwPtr,
			Usage: &TokenUsage{
				Input:      freshInput,
				Output:     last.OutputTokens,
				CacheRead:  last.CachedInputTokens,
				CacheWrite: last.CacheWriteInputTokens,
				Reasoning:  last.ReasoningOutputTokens,
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
	p.recordErrorRateLimits(lineResetAt, lineRetrySec, ts)

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
// limit, and any HTTP status code it carries. Codex serializes CodexErrorInfo
// as a snake_case string for unit variants and as an object keyed by the
// variant for variants with fields, such as
// {"http_connection_failed":{"http_status_code":429}}. A status code decides
// the rate-limit flag; otherwise the variant does. Keys are read in sorted
// order so the result does not depend on map iteration.
func classifyCodexErrorInfo(errorInfo interface{}) (*bool, *int) {
	var kindRL *bool
	var status *int
	switch info := errorInfo.(type) {
	case string:
		kindRL = codexErrorKindIsRateLimit(info)
	case map[string]interface{}:
		keys := make([]string, 0, len(info))
		for k := range info {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := info[k]
			switch k {
			case "http_status_code":
				if status == nil {
					status = extractInt(v)
				}
			case "type":
				if s, ok := v.(string); ok && kindRL == nil {
					kindRL = codexErrorKindIsRateLimit(s)
				}
			default:
				if kindRL == nil {
					kindRL = codexErrorKindIsRateLimit(k)
				}
				if fields, ok := v.(map[string]interface{}); ok && status == nil {
					if sc, ok := fields["http_status_code"]; ok && sc != nil {
						status = extractInt(sc)
					}
				}
			}
		}
	}
	if status != nil {
		return boolPtr(*status == 429), status
	}
	return kindRL, nil
}

// codexErrorKinds lists every CodexErrorInfo variant
// (codex-rs/protocol/src/protocol.rs). Only rate_limit_exceeded is a rate
// limit.
var codexErrorKinds = map[string]bool{
	"context_window_exceeded":           false,
	"session_budget_exceeded":           false,
	"usage_limit_exceeded":              false,
	"rate_limit_exceeded":               true,
	"flex_unavailable":                  false,
	"server_overloaded":                 false,
	"model_not_found":                   false,
	"policy_capacity_unavailable":       false,
	"cyber_policy":                      false,
	"bio_policy":                        false,
	"misalignment_policy_violation":     false,
	"too_many_denials":                  false,
	"http_connection_failed":            false,
	"response_stream_connection_failed": false,
	"internal_server_error":             false,
	"unauthorized":                      false,
	"bad_request":                       false,
	"invalid_prompt":                    false,
	"sandbox_error":                     false,
	"response_stream_disconnected":      false,
	"response_too_many_failed_attempts": false,
	"active_turn_not_steerable":         false,
	"thread_rollback_failed":            false,
	"other":                             false,
}

// codexErrorKindIsRateLimit reports whether an error kind, in snake_case or
// PascalCase, is a rate limit; nil for a kind Codex does not define.
func codexErrorKindIsRateLimit(kind string) *bool {
	isRL, known := codexErrorKinds[codexSnakeCase(kind)]
	if !known {
		return nil
	}
	return boolPtr(isRL)
}

func codexSnakeCase(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}
