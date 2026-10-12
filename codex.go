// Package parser provides streaming log parsers for AI agent harnesses.
//
// Attribution:
// Derived from and inspired by OpenAI Codex CLI specifications (docs/harness-logs/codex.ts),
// AgentHUD (https://github.com/neochoon/agenthud),
// Codex Transcripts (https://github.com/prateek/codex-transcripts),
// Claude Code Session Viewer (https://github.com/eric050828/claude-code-session-viewer), and
// Sessions (https://github.com/nicknisi/sessions).
package parser

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	codexPartitionedPathRegex = regexp.MustCompile(`(?:^|/)sessions/(\d{4})/(\d{2})/(\d{2})/rollout-(\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}-\d{2})?)-([0-9a-zA-Z_-]+)\.jsonl$`)
	codexFlatPathRegex        = regexp.MustCompile(`(?:^|/)rollout-(\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}-\d{2})?)-([0-9a-zA-Z_-]+)\.jsonl$`)

	codexPartitionedFallbackRegex = regexp.MustCompile(`(?:^|/)sessions/(\d{4})/(\d{2})/(\d{2})/rollout-(.+?)-([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\.jsonl$`)
	codexFlatFallbackRegex        = regexp.MustCompile(`(?:^|/)rollout-(.+?)-([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\.jsonl$`)
)

// CodexRolloutFilenameInfo contains parsed components from a Codex rollout file path.
type CodexRolloutFilenameInfo struct {
	FilePath        string `json:"filePath"`
	Year            string `json:"year,omitempty"`
	Month           string `json:"month,omitempty"`
	Day             string `json:"day,omitempty"`
	TimestampString string `json:"timestampString"`
	SessionID       string `json:"sessionId"`
}

// ParseCodexRolloutPath parses a Codex rollout file path into constituent components.
func ParseCodexRolloutPath(filePath string) (*CodexRolloutFilenameInfo, error) {
	if filePath == "" {
		return nil, fmt.Errorf("empty file path")
	}

	normalized := strings.ReplaceAll(filePath, "\\", "/")
	if match := codexPartitionedPathRegex.FindStringSubmatch(normalized); len(match) == 6 {
		return &CodexRolloutFilenameInfo{
			FilePath:        filePath,
			Year:            match[1],
			Month:           match[2],
			Day:             match[3],
			TimestampString: match[4],
			SessionID:       match[5],
		}, nil
	}

	if match := codexPartitionedFallbackRegex.FindStringSubmatch(normalized); len(match) == 6 {
		return &CodexRolloutFilenameInfo{
			FilePath:        filePath,
			Year:            match[1],
			Month:           match[2],
			Day:             match[3],
			TimestampString: match[4],
			SessionID:       match[5],
		}, nil
	}

	if match := codexFlatPathRegex.FindStringSubmatch(normalized); len(match) == 3 {
		return &CodexRolloutFilenameInfo{
			FilePath:        filePath,
			TimestampString: match[1],
			SessionID:       match[2],
		}, nil
	}

	if match := codexFlatFallbackRegex.FindStringSubmatch(normalized); len(match) == 3 {
		return &CodexRolloutFilenameInfo{
			FilePath:        filePath,
			TimestampString: match[1],
			SessionID:       match[2],
		}, nil
	}

	return nil, fmt.Errorf("invalid codex rollout path: %q", filePath)
}

// CleanCodexPrompt filters synthetic <environment_context> and <session_context> blocks from user prompts.
func CleanCodexPrompt(promptText string) string {
	trimmed := strings.TrimSpace(promptText)

	for {
		changed := false
		if strings.HasPrefix(trimmed, "<environment_context>") {
			if idx := strings.Index(trimmed, "</environment_context>"); idx != -1 {
				trimmed = strings.TrimSpace(trimmed[idx+len("</environment_context>"):])
				changed = true
			} else {
				return ""
			}
		}
		if strings.HasPrefix(trimmed, "<session_context>") {
			if idx := strings.Index(trimmed, "</session_context>"); idx != -1 {
				trimmed = strings.TrimSpace(trimmed[idx+len("</session_context>"):])
				changed = true
			} else {
				return ""
			}
		}
		if !changed {
			break
		}
	}

	return strings.TrimSpace(trimmed)
}

// ExtractCodexTitleFromPrompt extracts a session title from prompt text, ignoring synthetic context blocks.
func ExtractCodexTitleFromPrompt(promptText string) (string, bool) {
	cleanPrompt := CleanCodexPrompt(promptText)
	if cleanPrompt == "" {
		return "", false
	}
	firstLine := strings.Split(cleanPrompt, "\n")[0]
	firstLine = strings.TrimSpace(firstLine)
	if len(firstLine) > 100 {
		firstLine = firstLine[:100]
	}
	return firstLine, true
}

type rawCodexGitMeta struct {
	CommitHash    string `json:"commit_hash"`
	Branch        string `json:"branch"`
	RepositoryURL string `json:"repository_url"`
}

type rawCodexThreadSpawn struct {
	ParentThreadID string  `json:"parent_thread_id"`
	Depth          int     `json:"depth"`
	AgentPath      *string `json:"agent_path"`
	AgentNickname  string  `json:"agent_nickname"`
	AgentRole      string  `json:"agent_role"`
}

type rawCodexSessionMetaPayload struct {
	ID               string      `json:"id"`
	SessionID        string      `json:"session_id,omitempty"`
	Timestamp        string      `json:"timestamp"`
	CWD              string      `json:"cwd"`
	Originator       string      `json:"originator"`
	CLIVersion       string      `json:"cli_version"`
	Source           interface{} `json:"source"`
	Model            string      `json:"model,omitempty"`
	ModelProvider    string      `json:"model_provider"`
	BaseInstructions *struct {
		Text string `json:"text"`
	} `json:"base_instructions"`
	Git               *rawCodexGitMeta `json:"git"`
	ParentThreadID    string           `json:"parent_thread_id"`
	AgentNickname     string           `json:"agent_nickname"`
	AgentRole         string           `json:"agent_role"`
	MultiAgentVersion string           `json:"multi_agent_version"`
}

type rawCodexTurnContextPayload struct {
	TurnID            string                 `json:"turn_id"`
	CWD               string                 `json:"cwd"`
	CurrentDate       string                 `json:"current_date"`
	Timezone          string                 `json:"timezone"`
	Model             string                 `json:"model"`
	Personality       string                 `json:"personality"`
	ApprovalPolicy    string                 `json:"approval_policy"`
	SandboxPolicy     map[string]interface{} `json:"sandbox_policy"`
	CollaborationMode map[string]interface{} `json:"collaboration_mode"`
	Summary           string                 `json:"summary"`
	RealtimeActive    bool                   `json:"realtime_active"`
}

type rawCodexTokenBreakdown struct {
	TotalTokens        int64 `json:"total_tokens"`
	PromptTokens       int64 `json:"prompt_tokens"`
	CompletionTokens   int64 `json:"completion_tokens"`
	CachedPromptTokens int64 `json:"cached_prompt_tokens"`
	ReasoningTokens    int64 `json:"reasoning_tokens"`
}

type rawCodexTokenCountInfo struct {
	TotalTokenUsage    rawCodexTokenBreakdown `json:"total_token_usage"`
	LastTokenUsage     rawCodexTokenBreakdown `json:"last_token_usage"`
	ModelContextWindow int64                  `json:"model_context_window"`
}

type rawCodexRateLimits struct {
	LimitRequests     *int64 `json:"limit_requests,omitempty"`
	RemainingRequests *int64 `json:"remaining_requests,omitempty"`
	ResetRequests     string `json:"reset_requests,omitempty"`
	LimitTokens       *int64 `json:"limit_tokens,omitempty"`
	RemainingTokens   *int64 `json:"remaining_tokens,omitempty"`
	ResetTokens       string `json:"reset_tokens,omitempty"`
}

type rawCodexEventMsgPayload struct {
	Item               json.RawMessage         `json:"item,omitempty"`
	Type               string                  `json:"type"`
	Message            string                  `json:"message"`
	TurnID             string                  `json:"turn_id"`
	StartedAt          json.RawMessage         `json:"started_at"`
	CompletedAt        json.RawMessage         `json:"completed_at"`
	DurationMS         int64                   `json:"duration_ms"`
	LastAgentMessage   string                  `json:"last_agent_message"`
	CodexErrorInfo     interface{}             `json:"codex_error_info,omitempty"`
	Info               *rawCodexTokenCountInfo `json:"info"`
	RateLimits         *rawCodexRateLimits     `json:"rate_limits,omitempty"`
	ModelContextWindow int64                   `json:"model_context_window,omitempty"`
	Summary            []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"summary"`
	EncryptedContent string `json:"encrypted_content"`
}

type rawCodexAgentMessage struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Questions []map[string]interface{} `json:"questions"`
}

func parseCodexCompletedItem(raw json.RawMessage, ts int64) ([]ParsedEvent, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var header struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return nil, fmt.Errorf("unmarshaling completed item: %w", err)
	}
	if header.Type != "AgentMessage" {
		return nil, nil
	}
	var item rawCodexAgentMessage
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, fmt.Errorf("unmarshaling completed agent message: %w", err)
	}
	var content strings.Builder
	for _, block := range item.Content {
		if block.Type == "Text" {
			content.WriteString(block.Text)
		}
	}
	data := EventData{Role: stringPtr("assistant"), Content: stringPtr(content.String())}
	eventType := "agent_message"
	var questions []string
	for _, question := range item.Questions {
		if text := codexQuestionText(question); text != "" {
			questions = append(questions, text)
		}
	}
	if len(questions) > 0 {
		eventType = "question_prompt"
		data.Question = stringPtr(strings.Join(questions, "\n"))
		if item.ID != "" {
			data.ToolCallID = stringPtr(item.ID)
		}
	}
	if strings.TrimSpace(content.String()) == "" && data.Question == nil {
		return nil, nil
	}
	return []ParsedEvent{{EventType: eventType, Timestamp: ts, Data: data}}, nil
}

func codexQuestionText(input map[string]interface{}) string {
	for _, key := range []string{"question", "prompt", "title"} {
		if text, ok := input[key].(string); ok && strings.TrimSpace(text) != "" {
			return text
		}
	}
	var parts []string
	if questions, ok := input["questions"].([]interface{}); ok {
		for _, question := range questions {
			if q, ok := question.(map[string]interface{}); ok {
				if text := codexQuestionText(q); text != "" {
					parts = append(parts, text)
				}
			}
		}
	}
	return strings.Join(parts, "\n")
}

type rawCodexResponseItemPayload struct {
	Type      string             `json:"type"`
	Role      string             `json:"role"`
	Name      string             `json:"name"`
	CallID    string             `json:"call_id"`
	Arguments string             `json:"arguments"`
	Input     interface{}        `json:"input,omitempty"`
	Output    rawCodexToolOutput `json:"output"`
	Query     string             `json:"query"`
	Content   interface{}        `json:"content"`
	Summary   []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"summary"`
	EncryptedContent string `json:"encrypted_content"`
}

type rawCodexLine struct {
	Type      string          `json:"type"`
	Timestamp interface{}     `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

// CodexParser maintains session stream state when parsing Codex rollout transcripts.
type CodexParser struct {
	mu                sync.Mutex
	currentModel      string
	contextWindow     int64
	rateLimitResetAt  *int64
	retryAfterSeconds *int64
}

// NewCodexParser creates a new stateful parser stream for Codex transcripts.
func NewCodexParser() *CodexParser {
	return &CodexParser{}
}

// Reset resets the parser stream state.
func (p *CodexParser) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.currentModel = ""
	p.contextWindow = 0
	p.rateLimitResetAt = nil
	p.retryAfterSeconds = nil
}

// ParseLine parses a single line using this CodexParser instance.
func (p *CodexParser) ParseLine(line []byte) ([]ParsedEvent, error) {
	return parseCodexLineWithState(line, p)
}

// ParseCodexLine parses a single line of Codex rollout JSONL log into ParsedEvent structs without stream state.
func ParseCodexLine(line []byte) ([]ParsedEvent, error) {
	return parseCodexLineWithState(line, nil)
}

func parseCodexLineWithState(line []byte, p *CodexParser) ([]ParsedEvent, error) {
	trimmed := strings.TrimSpace(string(line))
	if len(trimmed) == 0 {
		return nil, nil
	}

	var raw rawCodexLine
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return nil, fmt.Errorf("unmarshaling codex line: %w", err)
	}

	ts := parseTimestamp(raw.Timestamp)
	var events []ParsedEvent

	switch raw.Type {
	case "session_meta":
		var meta rawCodexSessionMetaPayload
		if err := json.Unmarshal(raw.Payload, &meta); err != nil {
			return nil, fmt.Errorf("unmarshaling session_meta payload: %w", err)
		}

		if meta.Timestamp != "" {
			ts = parseTimestamp(meta.Timestamp)
		}

		var gitBranch string
		if meta.Git != nil {
			gitBranch = meta.Git.Branch
		}

		// Detect subagent metadata
		parentThreadID := meta.ParentThreadID
		nickname := meta.AgentNickname
		role := meta.AgentRole

		if meta.Source != nil {
			if sm, ok := meta.Source.(map[string]interface{}); ok {
				if subVal, exists := sm["subagent"]; exists && subVal != nil {
					if subMap, ok := subVal.(map[string]interface{}); ok {
						if tsVal, ok := subMap["thread_spawn"]; ok && tsVal != nil {
							var ts rawCodexThreadSpawn
							data, err := json.Marshal(tsVal)
							if err == nil && json.Unmarshal(data, &ts) == nil {
								if ts.ParentThreadID != "" {
									parentThreadID = ts.ParentThreadID
								}
								if ts.AgentNickname != "" {
									nickname = ts.AgentNickname
								}
								if ts.AgentRole != "" {
									role = ts.AgentRole
								}
							}
						}
					} else if subStr, ok := subVal.(string); ok && subStr != "" {
						if role == "" {
							role = subStr
						}
					}
				}
			}
		}

		isSubagent := parentThreadID != "" || nickname != "" || role != ""

		sessionID := meta.ID
		if sessionID == "" {
			sessionID = meta.SessionID
		}

		var title string
		if isSubagent {
			if nickname != "" && role != "" {
				title = fmt.Sprintf("Codex Subagent: %s (%s)", nickname, role)
			} else if nickname != "" {
				title = fmt.Sprintf("Codex Subagent: %s", nickname)
			} else if role != "" {
				title = fmt.Sprintf("Codex Subagent: (%s)", role)
			} else if parentThreadID != "" {
				title = fmt.Sprintf("Codex Subagent %s", sessionID)
			} else {
				title = fmt.Sprintf("Codex Session %s", sessionID)
			}
		} else {
			if sessionID != "" {
				title = fmt.Sprintf("Codex Session %s", sessionID)
			} else {
				title = "Codex Session"
			}
		}

		var modelPtr *string
		if meta.Model != "" {
			modelPtr = stringPtr(meta.Model)
			if p != nil {
				p.mu.Lock()
				p.currentModel = meta.Model
				p.mu.Unlock()
			}
		} else if meta.ModelProvider != "" {
			modelPtr = stringPtr(meta.ModelProvider)
			if p != nil {
				p.mu.Lock()
				p.currentModel = meta.ModelProvider
				p.mu.Unlock()
			}
		}

		events = append(events, ParsedEvent{
			EventType: "run_started",
			Timestamp: ts,
			CWD:       meta.CWD,
			GitBranch: gitBranch,
			Data: EventData{
				Title: stringPtr(title),
				Model: modelPtr,
			},
		})

	case "turn_context":
		var ctx rawCodexTurnContextPayload
		if err := json.Unmarshal(raw.Payload, &ctx); err != nil {
			return nil, fmt.Errorf("unmarshaling turn_context payload: %w", err)
		}

		if ctx.Model != "" {
			if p != nil {
				p.mu.Lock()
				p.currentModel = ctx.Model
				p.mu.Unlock()
			}
			events = append(events, ParsedEvent{
				EventType: "model_change",
				Timestamp: ts,
				CWD:       ctx.CWD,
				Data: EventData{
					Model: stringPtr(ctx.Model),
				},
			})
		}

	case "event_msg":
		var evt rawCodexEventMsgPayload
		if err := json.Unmarshal(raw.Payload, &evt); err != nil {
			return nil, fmt.Errorf("unmarshaling event_msg payload: %w", err)
		}

		switch evt.Type {
		case "item_completed":
			completed, err := parseCodexCompletedItem(evt.Item, ts)
			if err != nil {
				return nil, err
			}
			events = append(events, completed...)

		case "user_message":
			cleanPrompt := CleanCodexPrompt(evt.Message)
			if cleanPrompt != "" {
				title, hasTitle := ExtractCodexTitleFromPrompt(cleanPrompt)
				var titlePtr *string
				if hasTitle {
					titlePtr = stringPtr(title)
				}
				events = append(events, ParsedEvent{
					EventType: "turn_start",
					Timestamp: ts,
					Data: EventData{
						Role:       stringPtr("user"),
						Content:    stringPtr(cleanPrompt),
						LastPrompt: stringPtr(cleanPrompt),
						Title:      titlePtr,
					},
				})
			}

		case "agent_message":
			if evt.Message != "" {
				events = append(events, ParsedEvent{
					EventType: "agent_message",
					Timestamp: ts,
					Data: EventData{
						Role:    stringPtr("assistant"),
						Content: stringPtr(evt.Message),
					},
				})
			}

		case "reasoning":
			var sb strings.Builder
			for _, item := range evt.Summary {
				if item.Text != "" {
					if sb.Len() > 0 {
						sb.WriteString("\n")
					}
					sb.WriteString(item.Text)
				}
			}
			reasoningText := sb.String()
			if reasoningText != "" {
				events = append(events, ParsedEvent{
					EventType: "thinking",
					Timestamp: ts,
					Data: EventData{
						Role:    stringPtr("assistant"),
						Content: stringPtr(reasoningText),
					},
				})
			}

		case "token_count":
			if evt.Info != nil {
				last := evt.Info.LastTokenUsage
				totalUsage := evt.Info.TotalTokenUsage

				chosen := last
				if chosen.TotalTokens == 0 && chosen.PromptTokens == 0 && chosen.CompletionTokens == 0 {
					chosen = totalUsage
				}

				tot := chosen.TotalTokens
				if tot == 0 {
					tot = chosen.PromptTokens + chosen.CompletionTokens
				}

				freshInput := chosen.PromptTokens - chosen.CachedPromptTokens
				if freshInput < 0 {
					freshInput = 0
				}

				usage := &TokenUsage{
					Input:      freshInput,
					Output:     chosen.CompletionTokens,
					CacheRead:  chosen.CachedPromptTokens,
					CacheWrite: 0,
					Reasoning:  chosen.ReasoningTokens,
					Total:      tot,
				}

				var activeModel string
				var activeCw int64
				if p != nil {
					p.mu.Lock()
					activeModel = p.currentModel
					activeCw = p.contextWindow
					p.mu.Unlock()
				}

				cw := evt.Info.ModelContextWindow
				if cw == 0 {
					cw = activeCw
				}

				var cwPtr *int64
				if cw > 0 {
					cwPtr = &cw
					if p != nil {
						p.mu.Lock()
						p.contextWindow = cw
						p.mu.Unlock()
					}
				}

				var modelPtr *string
				if activeModel != "" {
					modelPtr = stringPtr(activeModel)
				}

				var resetAtPtr *int64
				var retrySecPtr *int64
				if evt.RateLimits != nil {
					resetAtPtr, retrySecPtr = selectCodexRateLimitReset(evt.RateLimits, ts, false)
				}
				if resetAtPtr != nil && *resetAtPtr <= ts {
					resetAtPtr = nil
					retrySecPtr = nil
				}
				if p != nil {
					p.mu.Lock()
					if resetAtPtr != nil {
						p.rateLimitResetAt = resetAtPtr
						p.retryAfterSeconds = retrySecPtr
					} else if evt.RateLimits != nil &&
						(evt.RateLimits.RemainingTokens == nil || *evt.RateLimits.RemainingTokens > 0) &&
						(evt.RateLimits.RemainingRequests == nil || *evt.RateLimits.RemainingRequests > 0) &&
						(evt.RateLimits.RemainingTokens != nil || evt.RateLimits.RemainingRequests != nil) {
						p.rateLimitResetAt = nil
						p.retryAfterSeconds = nil
					} else if p.rateLimitResetAt != nil && *p.rateLimitResetAt <= ts {
						p.rateLimitResetAt = nil
						p.retryAfterSeconds = nil
					}
					p.mu.Unlock()
				}

				events = append(events, ParsedEvent{
					EventType: "turn_end",
					Timestamp: ts,
					Data: EventData{
						Role:               stringPtr("assistant"),
						Model:              modelPtr,
						ModelContextWindow: cwPtr,
						Usage:              usage,
						RateLimitResetAt:   resetAtPtr,
						RetryAfterSeconds:  retrySecPtr,
					},
				})
			}

		case "task_started":
			ts, err := parseCodexTaskTimestamp(evt.StartedAt, ts)
			if err != nil {
				return nil, fmt.Errorf("parsing task started_at: %w", err)
			}
			events = append(events, ParsedEvent{
				EventType: "turn_start",
				Timestamp: ts,
				Data: EventData{
					Role: stringPtr("assistant"),
				},
			})

		case "task_complete":
			ts, err := parseCodexTaskTimestamp(evt.CompletedAt, ts)
			if err != nil {
				return nil, fmt.Errorf("parsing task completed_at: %w", err)
			}
			var contentPtr *string
			if evt.LastAgentMessage != "" {
				contentPtr = stringPtr(evt.LastAgentMessage)
			}
			events = append(events, ParsedEvent{
				EventType: "turn_end",
				Timestamp: ts,
				Data: EventData{
					Role:    stringPtr("assistant"),
					Content: contentPtr,
				},
			})

		case "error":
			var isRL *bool
			var httpStatusCode *int

			if evt.CodexErrorInfo != nil {
				switch info := evt.CodexErrorInfo.(type) {
				case string:
					switch info {
					case "RateLimitExceeded", "rate_limit_exceeded":
						isRL = boolPtr(true)
					case "UsageLimitExceeded", "usage_limit_exceeded",
						"ServerOverloaded", "server_overloaded":
						isRL = boolPtr(false)
					}
				case map[string]interface{}:
					for k, v := range info {
						switch k {
						case "RateLimitExceeded", "rate_limit_exceeded":
							isRL = boolPtr(true)
						case "UsageLimitExceeded", "usage_limit_exceeded",
							"ServerOverloaded", "server_overloaded":
							isRL = boolPtr(false)
						case "type":
							if s, ok := v.(string); ok {
								switch s {
								case "RateLimitExceeded", "rate_limit_exceeded":
									isRL = boolPtr(true)
								case "UsageLimitExceeded", "usage_limit_exceeded",
									"ServerOverloaded", "server_overloaded":
									isRL = boolPtr(false)
								}
							}
						case "HttpConnectionFailed", "http_connection_failed":
							if subMap, ok := v.(map[string]interface{}); ok {
								if sc, ok := subMap["http_status_code"]; ok && sc != nil {
									if code := extractInt(sc); code != nil {
										httpStatusCode = code
										if *code == 429 {
											isRL = boolPtr(true)
										} else {
											isRL = boolPtr(false)
										}
									}
								}
							}
						case "http_status_code":
							if code := extractInt(v); code != nil {
								httpStatusCode = code
								if *code == 429 {
									isRL = boolPtr(true)
								} else {
									isRL = boolPtr(false)
								}
							}
						}
					}
				}
			}

			if isRL == nil && httpStatusCode != nil {
				if *httpStatusCode == 429 {
					isRL = boolPtr(true)
				} else {
					isRL = boolPtr(false)
				}
			}

			var lineResetAt *int64
			var lineRetrySec *int64
			if evt.RateLimits != nil {
				rAt, rSec := selectCodexRateLimitReset(evt.RateLimits, ts, true)
				if rAt != nil && *rAt > ts {
					lineResetAt = rAt
					lineRetrySec = rSec
				}
				if p != nil {
					p.mu.Lock()
					if rAt != nil {
						if *rAt > ts {
							p.rateLimitResetAt = rAt
							p.retryAfterSeconds = rSec
						} else {
							p.rateLimitResetAt = nil
							p.retryAfterSeconds = nil
						}
					} else if p.rateLimitResetAt != nil && *p.rateLimitResetAt <= ts {
						p.rateLimitResetAt = nil
						p.retryAfterSeconds = nil
					}
					p.mu.Unlock()
				}
			}

			if p != nil {
				p.mu.Lock()
				if p.rateLimitResetAt != nil && *p.rateLimitResetAt <= ts {
					p.rateLimitResetAt = nil
					p.retryAfterSeconds = nil
				}
				p.mu.Unlock()
			}

			var errResetAt *int64
			var errRetrySec *int64

			if isRL != nil && !*isRL {
				// Explicit non-rate-limit error: preserve isRL = false, do NOT attach cached rate limit metadata.
				errResetAt = nil
				errRetrySec = nil
			} else if isRL != nil && *isRL {
				// Explicit rate-limit error: attach line metadata or cached metadata.
				errResetAt = lineResetAt
				errRetrySec = lineRetrySec
				if errResetAt == nil && p != nil {
					p.mu.Lock()
					if p.rateLimitResetAt != nil && *p.rateLimitResetAt > ts {
						errResetAt = p.rateLimitResetAt
						remSec := (*p.rateLimitResetAt - ts + 999) / 1000
						if remSec > 0 {
							errRetrySec = &remSec
						} else {
							errRetrySec = nil
						}
					}
					p.mu.Unlock()
				}
			} else {
				// isRL == nil: only attach rate-limit metadata if the event payload itself provides structured rate-limit data.
				// Do NOT infer isRL = true from unexpired p.rateLimitResetAt.
				if lineResetAt != nil {
					isRL = boolPtr(true)
					errResetAt = lineResetAt
					errRetrySec = lineRetrySec
				}
			}

			events = append(events, ParsedEvent{
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
			})
		}

	case "response_item":
		var resp rawCodexResponseItemPayload
		if err := json.Unmarshal(raw.Payload, &resp); err != nil {
			return nil, fmt.Errorf("unmarshaling response_item payload: %w", err)
		}

		switch resp.Type {
		case "function_call", "custom_tool_call":
			var inputMap map[string]interface{}
			if resp.Arguments != "" {
				if err := json.Unmarshal([]byte(resp.Arguments), &inputMap); err != nil {
					inputMap = map[string]interface{}{"raw": resp.Arguments}
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

			// Normalize common tool input properties for target extraction
			if resp.Name == "exec_command" && inputMap != nil {
				if cmdVal, ok := inputMap["cmd"]; ok && inputMap["command"] == nil {
					inputMap["command"] = cmdVal
				}
			} else if resp.Name == "spawn_agent" && inputMap != nil {
				if msgVal, ok := inputMap["message"]; ok && inputMap["prompt"] == nil {
					inputMap["prompt"] = msgVal
				}
				if typeVal, ok := inputMap["agent_type"]; ok && inputMap["description"] == nil {
					inputMap["description"] = typeVal
				}
			}

			var callIDPtr *string
			if resp.CallID != "" {
				callIDPtr = stringPtr(resp.CallID)
			}

			if resp.Name == "AskUserQuestion" || resp.Name == "ask_user" || resp.Name == "ask_question" || resp.Name == "request_user_input" || resp.Name == "request_user_input_async" {
				qText := codexQuestionText(inputMap)
				fileMod := NormalizeFileModification(resp.Name, inputMap)
				events = append(events, ParsedEvent{
					EventType: "question_prompt",
					Timestamp: ts,
					Data: EventData{
						Role:             stringPtr("assistant"),
						ToolName:         stringPtr(resp.Name),
						ToolCallID:       callIDPtr,
						ToolInput:        inputMap,
						FileModification: fileMod,
						Question:         stringPtr(qText),
					},
				})
			} else {
				fileMod := NormalizeFileModification(resp.Name, inputMap)
				events = append(events, ParsedEvent{
					EventType: "tool_call",
					Timestamp: ts,
					Data: EventData{
						Role:             stringPtr("assistant"),
						ToolName:         stringPtr(resp.Name),
						ToolCallID:       callIDPtr,
						ToolInput:        inputMap,
						FileModification: fileMod,
					},
				})
			}

		case "function_call_output", "custom_tool_call_output":
			output := string(resp.Output)
			var callIDPtr *string
			if resp.CallID != "" {
				callIDPtr = stringPtr(resp.CallID)
			}
			isErr := strings.Contains(strings.ToLower(output), "exit code: 1") ||
				strings.Contains(strings.ToLower(output), "error:") ||
				strings.Contains(strings.ToLower(output), "command failed")

			events = append(events, ParsedEvent{
				EventType: "tool_result",
				Timestamp: ts,
				Data: EventData{
					Role:       stringPtr("toolResult"),
					ToolCallID: callIDPtr,
					ToolOutput: stringPtr(output),
					IsError:    boolPtr(isErr),
				},
			})

		case "message":
			contentStr := extractContentString(resp.Content)
			if resp.Role == "user" {
				cleanPrompt := CleanCodexPrompt(contentStr)
				if cleanPrompt != "" {
					title, hasTitle := ExtractCodexTitleFromPrompt(cleanPrompt)
					var titlePtr *string
					if hasTitle {
						titlePtr = stringPtr(title)
					}
					events = append(events, ParsedEvent{
						EventType: "turn_start",
						Timestamp: ts,
						Data: EventData{
							Role:       stringPtr("user"),
							Content:    stringPtr(cleanPrompt),
							LastPrompt: stringPtr(cleanPrompt),
							Title:      titlePtr,
						},
					})
				}
			} else if resp.Role == "assistant" && contentStr != "" {
				events = append(events, ParsedEvent{
					EventType: "agent_message",
					Timestamp: ts,
					Data: EventData{
						Role:    stringPtr("assistant"),
						Content: stringPtr(contentStr),
					},
				})
			}

		case "reasoning":
			var sb strings.Builder
			for _, item := range resp.Summary {
				if item.Text != "" {
					if sb.Len() > 0 {
						sb.WriteString("\n")
					}
					sb.WriteString(item.Text)
				}
			}
			reasoningText := sb.String()
			if reasoningText != "" {
				events = append(events, ParsedEvent{
					EventType: "thinking",
					Timestamp: ts,
					Data: EventData{
						Role:    stringPtr("assistant"),
						Content: stringPtr(reasoningText),
					},
				})
			}

		case "tool_search_call":
			var callIDPtr *string
			if resp.CallID != "" {
				callIDPtr = stringPtr(resp.CallID)
			}
			events = append(events, ParsedEvent{
				EventType: "tool_call",
				Timestamp: ts,
				Data: EventData{
					Role:       stringPtr("assistant"),
					ToolName:   stringPtr("tool_search"),
					ToolCallID: callIDPtr,
					ToolInput:  map[string]interface{}{"query": resp.Query},
				},
			})

		case "tool_search_output":
			var callIDPtr *string
			if resp.CallID != "" {
				callIDPtr = stringPtr(resp.CallID)
			}
			events = append(events, ParsedEvent{
				EventType: "tool_result",
				Timestamp: ts,
				Data: EventData{
					Role:       stringPtr("toolResult"),
					ToolCallID: callIDPtr,
					ToolOutput: stringPtr(string(resp.Output)),
				},
			})
		}
	}

	return events, nil
}

func extractInt(v interface{}) *int {
	switch val := v.(type) {
	case float64:
		i := int(val)
		return &i
	case int:
		return &val
	case int64:
		i := int(val)
		return &i
	}
	return nil
}

func selectCodexRateLimitReset(rateLimits *rawCodexRateLimits, nowMs int64, isError bool) (*int64, *int64) {
	if rateLimits == nil {
		return nil, nil
	}
	var evalRequests bool
	var evalTokens bool

	if isError {
		evalRequests = rateLimits.ResetRequests != "" && (rateLimits.RemainingRequests == nil || *rateLimits.RemainingRequests <= 0)
		evalTokens = rateLimits.ResetTokens != "" && (rateLimits.RemainingTokens == nil || *rateLimits.RemainingTokens <= 0)
	} else {
		evalRequests = rateLimits.ResetRequests != "" && rateLimits.RemainingRequests != nil && *rateLimits.RemainingRequests <= 0
		evalTokens = rateLimits.ResetTokens != "" && rateLimits.RemainingTokens != nil && *rateLimits.RemainingTokens <= 0
	}
	if !evalRequests && !evalTokens {
		return nil, nil
	}
	var maxResetAt *int64
	var maxSec *int64

	if evalRequests {
		maxResetAt, maxSec = parseCodexRateLimitReset(rateLimits.ResetRequests, nowMs)
	}
	if evalTokens {
		tokResetAt, tokSec := parseCodexRateLimitReset(rateLimits.ResetTokens, nowMs)
		if tokResetAt != nil {
			if maxResetAt == nil || *tokResetAt > *maxResetAt {
				maxResetAt = tokResetAt
				maxSec = tokSec
			}
		}
	}
	return maxResetAt, maxSec
}

func parseCodexRateLimitReset(resetStr string, nowMs int64) (*int64, *int64) {
	trimmed := strings.TrimSpace(resetStr)
	if trimmed == "" {
		return nil, nil
	}
	// 1. Try Go duration (e.g. "45s", "1m30s", "500ms")
	if dur, err := time.ParseDuration(trimmed); err == nil {
		sec := int64(dur.Seconds())
		resetAt := nowMs + dur.Milliseconds()
		return &resetAt, &sec
	}
	// 2. Try numeric seconds (e.g. "45" or "45.0")
	if val, err := strconv.ParseFloat(trimmed, 64); err == nil && val > 0 {
		sec := int64(val)
		resetAt := nowMs + int64(val*1000)
		return &resetAt, &sec
	}
	// 3. Try RFC3339 / RFC3339Nano
	if t, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
		resetAt := t.UnixMilli()
		sec := (resetAt - nowMs) / 1000
		if sec < 0 {
			sec = 0
		}
		return &resetAt, &sec
	}
	if t, err := time.Parse(time.RFC3339, trimmed); err == nil {
		resetAt := t.UnixMilli()
		sec := (resetAt - nowMs) / 1000
		if sec < 0 {
			sec = 0
		}
		return &resetAt, &sec
	}
	return nil, nil
}
