package parser

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseCodexRolloutPath(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		wantYear    string
		wantMonth   string
		wantDay     string
		wantTS      string
		wantSessID  string
		wantErr     bool
	}{
		{
			name:       "standard date-partitioned unix path",
			path:       "/Users/dev/.codex/sessions/2026/06/12/rollout-2026-06-12T16-08-36-018f4a7c-1234-7000-8000-abcdef123456.jsonl",
			wantYear:   "2026",
			wantMonth:  "06",
			wantDay:    "12",
			wantTS:     "2026-06-12T16-08-36",
			wantSessID: "018f4a7c-1234-7000-8000-abcdef123456",
			wantErr:    false,
		},
		{
			name:       "windows date-partitioned path",
			path:       `C:\Users\dev\.codex\sessions\2026\09\20\rollout-2026-09-20T10-15-00-uuid-1234.jsonl`,
			wantYear:   "2026",
			wantMonth:  "09",
			wantDay:    "20",
			wantTS:     "2026-09-20T10-15-00",
			wantSessID: "uuid-1234",
			wantErr:    false,
		},
		{
			name:       "flat rollout filename",
			path:       "rollout-2026-06-12T16-08-36-my-session-uuid.jsonl",
			wantYear:   "",
			wantMonth:  "",
			wantDay:    "",
			wantTS:     "2026-06-12T16-08-36",
			wantSessID: "my-session-uuid",
			wantErr:    false,
		},
		{
			name:       "codex partitioned fallback rollout filename",
			path:       "/Users/dev/.codex/sessions/2026/06/12/rollout-customtimestamp-01900e70-659f-728b-b8dc-4c4f9bcde27a.jsonl",
			wantYear:   "2026",
			wantMonth:  "06",
			wantDay:    "12",
			wantTS:     "customtimestamp",
			wantSessID: "01900e70-659f-728b-b8dc-4c4f9bcde27a",
			wantErr:    false,
		},
		{
			name:       "codex flat fallback rollout filename",
			path:       "/Users/dev/rollout-customtimestamp-01900e70-659f-728b-b8dc-4c4f9bcde27a.jsonl",
			wantYear:   "",
			wantMonth:  "",
			wantDay:    "",
			wantTS:     "customtimestamp",
			wantSessID: "01900e70-659f-728b-b8dc-4c4f9bcde27a",
			wantErr:    false,
		},
		{
			name:    "empty path",
			path:    "",
			wantErr: true,
		},
		{
			name:    "unrelated file path",
			path:    "/var/log/syslog",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := ParseCodexRolloutPath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseCodexRolloutPath(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if info.Year != tt.wantYear {
				t.Errorf("Year = %q, want %q", info.Year, tt.wantYear)
			}
			if info.Month != tt.wantMonth {
				t.Errorf("Month = %q, want %q", info.Month, tt.wantMonth)
			}
			if info.Day != tt.wantDay {
				t.Errorf("Day = %q, want %q", info.Day, tt.wantDay)
			}
			if info.TimestampString != tt.wantTS {
				t.Errorf("TimestampString = %q, want %q", info.TimestampString, tt.wantTS)
			}
			if info.SessionID != tt.wantSessID {
				t.Errorf("SessionID = %q, want %q", info.SessionID, tt.wantSessID)
			}
		})
	}
}

func TestCleanCodexPromptAndTitle(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantClean   string
		wantTitle   string
		wantHasTile bool
	}{
		{
			name:        "pure environment context is filtered out",
			input:       "<environment_context>\nOS: Darwin\nCWD: /repo\n</environment_context>",
			wantClean:   "",
			wantTitle:   "",
			wantHasTile: false,
		},
		{
			name:        "pure unclosed environment context is filtered out",
			input:       "<environment_context>\nOS: Linux",
			wantClean:   "",
			wantTitle:   "",
			wantHasTile: false,
		},
		{
			name:        "pure session context is filtered out",
			input:       "<session_context>\nPrior messages: ...\n</session_context>",
			wantClean:   "",
			wantTitle:   "",
			wantHasTile: false,
		},
		{
			name:        "environment context followed by user prompt",
			input:       "<environment_context>\nCWD: /repo\n</environment_context>\n\nRefactor the auth handler in auth.go",
			wantClean:   "Refactor the auth handler in auth.go",
			wantTitle:   "Refactor the auth handler in auth.go",
			wantHasTile: true,
		},
		{
			name:        "user prompt exceeding 100 characters is truncated",
			input:       "This is an extremely long user task prompt that goes on and on beyond one hundred characters in total length to verify truncation",
			wantClean:   "This is an extremely long user task prompt that goes on and on beyond one hundred characters in total length to verify truncation",
			wantTitle:   "This is an extremely long user task prompt that goes on and on beyond one hundred characters in tota",
			wantHasTile: true,
		},
		{
			name:        "multi-line user prompt extracts first line as title",
			input:       "Fix the database query deadlock\n\nMake sure to run with WAL mode and verify under load.",
			wantClean:   "Fix the database query deadlock\n\nMake sure to run with WAL mode and verify under load.",
			wantTitle:   "Fix the database query deadlock",
			wantHasTile: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotClean := CleanCodexPrompt(tt.input)
			if gotClean != tt.wantClean {
				t.Errorf("CleanCodexPrompt() = %q, want %q", gotClean, tt.wantClean)
			}

			title, hasTitle := ExtractCodexTitleFromPrompt(tt.input)
			if hasTitle != tt.wantHasTile {
				t.Errorf("ExtractCodexTitleFromPrompt() hasTitle = %v, want %v", hasTitle, tt.wantHasTile)
			}
			if title != tt.wantTitle {
				t.Errorf("ExtractCodexTitleFromPrompt() title = %q, want %q", title, tt.wantTitle)
			}
		})
	}
}

func TestParseCodexLine(t *testing.T) {
	t.Run("empty or whitespace line", func(t *testing.T) {
		evs, err := ParseCodexLine([]byte("   \n"))
		if err != nil || len(evs) != 0 {
			t.Errorf("expected empty events for whitespace line, got evs=%v err=%v", evs, err)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		_, err := ParseCodexLine([]byte("{not valid json"))
		if err == nil {
			t.Errorf("expected error for invalid json")
		}
	})

	t.Run("session_meta root CLI session", func(t *testing.T) {
		line := `{
			"type": "session_meta",
			"timestamp": "2026-06-12T16:08:36.123Z",
			"payload": {
				"id": "sess-root-123",
				"timestamp": "2026-06-12T16:08:36.123Z",
				"cwd": "/Users/dev/project",
				"originator": "codex-tui",
				"cli_version": "0.121.0",
				"source": "cli",
				"model_provider": "openai",
				"git": {
					"commit_hash": "abcdef123",
					"branch": "feature/rollout",
					"repository_url": "git@github.com:org/repo.git"
				}
			}
		}`
		evs, err := ParseCodexLine([]byte(line))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(evs) != 1 {
			t.Fatalf("expected 1 event, got %d", len(evs))
		}
		e := evs[0]
		if e.EventType != "run_started" {
			t.Errorf("EventType = %q, want 'run_started'", e.EventType)
		}
		if e.CWD != "/Users/dev/project" {
			t.Errorf("CWD = %q, want '/Users/dev/project'", e.CWD)
		}
		if e.GitBranch != "feature/rollout" {
			t.Errorf("GitBranch = %q, want 'feature/rollout'", e.GitBranch)
		}
		if e.Data.Title == nil || *e.Data.Title != "Codex Session sess-root-123" {
			t.Errorf("Title = %v, want 'Codex Session sess-root-123'", e.Data.Title)
		}
		if e.Data.Model == nil || *e.Data.Model != "openai" {
			t.Errorf("Model = %v, want 'openai'", e.Data.Model)
		}
	})

	t.Run("session_meta subagent with thread_spawn source", func(t *testing.T) {
		line := `{
			"type": "session_meta",
			"timestamp": "2026-06-12T16:08:36.123Z",
			"payload": {
				"id": "sess-child-456",
				"cwd": "/Users/dev/project",
				"source": {
					"subagent": {
						"thread_spawn": {
							"parent_thread_id": "sess-root-123",
							"depth": 1,
							"agent_nickname": "Hubble",
							"agent_role": "explorer"
						}
					}
				}
			}
		}`
		evs, err := ParseCodexLine([]byte(line))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(evs) != 1 {
			t.Fatalf("expected 1 event, got %d", len(evs))
		}
		if evs[0].EventType != "run_started" {
			t.Errorf("EventType = %q, want 'run_started'", evs[0].EventType)
		}
		if evs[0].Data.Title == nil || *evs[0].Data.Title != "Codex Subagent: Hubble (explorer)" {
			t.Errorf("Title = %v, want 'Codex Subagent: Hubble (explorer)'", evs[0].Data.Title)
		}
	})

	t.Run("turn_context model switch", func(t *testing.T) {
		line := `{
			"type": "turn_context",
			"timestamp": "2026-06-12T16:08:37.000Z",
			"payload": {
				"turn_id": "turn-1",
				"cwd": "/Users/dev/project",
				"model": "gpt-5.4",
				"approval_policy": "on-request"
			}
		}`
		evs, err := ParseCodexLine([]byte(line))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(evs) != 1 {
			t.Fatalf("expected 1 event, got %d", len(evs))
		}
		if evs[0].EventType != "model_change" {
			t.Errorf("EventType = %q, want 'model_change'", evs[0].EventType)
		}
		if evs[0].Data.Model == nil || *evs[0].Data.Model != "gpt-5.4" {
			t.Errorf("Model = %v, want 'gpt-5.4'", evs[0].Data.Model)
		}
	})

	t.Run("event_msg user_message with synthetic context filtering", func(t *testing.T) {
		// Pure synthetic context -> no event
		syntheticLine := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:38.000Z",
			"payload": {
				"type": "user_message",
				"message": "<environment_context>\nOS: Darwin\n</environment_context>"
			}
		}`
		evs1, err := ParseCodexLine([]byte(syntheticLine))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(evs1) != 0 {
			t.Errorf("expected 0 events for pure synthetic user message, got %d", len(evs1))
		}

		// User prompt with environment context header
		mixedLine := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:39.000Z",
			"payload": {
				"type": "user_message",
				"message": "<environment_context>\nCWD: /repo\n</environment_context>\n\nImplement streaming tests"
			}
		}`
		evs2, err := ParseCodexLine([]byte(mixedLine))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(evs2) != 1 {
			t.Fatalf("expected 1 event, got %d", len(evs2))
		}
		if evs2[0].EventType != "turn_start" {
			t.Errorf("EventType = %q, want 'turn_start'", evs2[0].EventType)
		}
		if evs2[0].Data.Content == nil || *evs2[0].Data.Content != "Implement streaming tests" {
			t.Errorf("Content = %v, want 'Implement streaming tests'", evs2[0].Data.Content)
		}
		if evs2[0].Data.Title == nil || *evs2[0].Data.Title != "Implement streaming tests" {
			t.Errorf("Title = %v, want 'Implement streaming tests'", evs2[0].Data.Title)
		}
	})

	t.Run("event_msg agent_message and reasoning", func(t *testing.T) {
		agentMsgLine := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:40.000Z",
			"payload": {
				"type": "agent_message",
				"message": "All unit tests pass."
			}
		}`
		evs1, err := ParseCodexLine([]byte(agentMsgLine))
		if err != nil || len(evs1) != 1 {
			t.Fatalf("failed agent_message parsing: evs=%v err=%v", evs1, err)
		}
		if evs1[0].EventType != "agent_message" || *evs1[0].Data.Content != "All unit tests pass." {
			t.Errorf("unexpected agent_message event: %+v", evs1[0])
		}

		reasoningLine := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:41.000Z",
			"payload": {
				"type": "reasoning",
				"summary": [
					{"type": "text", "text": "Analyzing the parser code structure..."}
				]
			}
		}`
		evs2, err := ParseCodexLine([]byte(reasoningLine))
		if err != nil || len(evs2) != 1 {
			t.Fatalf("failed reasoning parsing: evs=%v err=%v", evs2, err)
		}
		if evs2[0].EventType != "thinking" || *evs2[0].Data.Content != "Analyzing the parser code structure..." {
			t.Errorf("unexpected thinking event: %+v", evs2[0])
		}
	})

	t.Run("event_msg token_count usage breakdown", func(t *testing.T) {
		line := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:42.000Z",
			"payload": {
				"type": "token_count",
				"info": {
					"total_token_usage": {
						"total_tokens": 5000,
						"prompt_tokens": 4000,
						"completion_tokens": 1000,
						"cached_prompt_tokens": 500,
						"reasoning_tokens": 200
					},
					"last_token_usage": {
						"total_tokens": 800,
						"prompt_tokens": 600,
						"completion_tokens": 200,
						"cached_prompt_tokens": 100,
						"reasoning_tokens": 50
					},
					"model_context_window": 128000
				}
			}
		}`
		evs, err := ParseCodexLine([]byte(line))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(evs) != 1 {
			t.Fatalf("expected 1 event, got %d", len(evs))
		}
		if evs[0].EventType != "turn_end" {
			t.Errorf("EventType = %q, want 'turn_end'", evs[0].EventType)
		}
		u := evs[0].Data.Usage
		if u == nil {
			t.Fatalf("expected non-nil Usage")
		}
		if u.Input != 500 || u.Output != 200 || u.CacheRead != 100 || u.Reasoning != 50 || u.Total != 800 {
			t.Errorf("Usage mismatch: %+v", u)
		}
		if evs[0].Data.ModelContextWindow == nil || *evs[0].Data.ModelContextWindow != 128000 {
			t.Errorf("ModelContextWindow = %v, want 128000", evs[0].Data.ModelContextWindow)
		}
		if evs[0].Data.ContextFullness != nil {
			t.Errorf("expected nil ContextFullness, got %v", *evs[0].Data.ContextFullness)
		}
	})

	t.Run("event_msg error without structured info does not classify as rate limit from text", func(t *testing.T) {
		line := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:43.000Z",
			"payload": {
				"type": "error",
				"message": "Connection rate limit exceeded"
			}
		}`
		evs, err := ParseCodexLine([]byte(line))
		if err != nil || len(evs) != 1 {
			t.Fatalf("failed error event parsing: evs=%v err=%v", evs, err)
		}
		if evs[0].EventType != "error" || *evs[0].Data.Content != "Connection rate limit exceeded" || !*evs[0].Data.IsError {
			t.Errorf("unexpected error event: %+v", evs[0])
		}
		if evs[0].Data.IsRateLimit != nil && *evs[0].Data.IsRateLimit {
			t.Errorf("expected isRateLimit = false/nil for rate limit text without structured info")
		}
	})

	t.Run("event_msg error with codex_error_info RateLimitExceeded", func(t *testing.T) {
		line := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:43.000Z",
			"payload": {
				"type": "error",
				"message": "Failed to complete request",
				"codex_error_info": "RateLimitExceeded"
			}
		}`
		evs, err := ParseCodexLine([]byte(line))
		if err != nil || len(evs) != 1 {
			t.Fatalf("failed error event parsing: evs=%v err=%v", evs, err)
		}
		if evs[0].EventType != "error" || *evs[0].Data.Content != "Failed to complete request" || !*evs[0].Data.IsError {
			t.Errorf("unexpected error event: %+v", evs[0])
		}
		if evs[0].Data.IsRateLimit == nil || !*evs[0].Data.IsRateLimit {
			t.Errorf("expected isRateLimit = true for RateLimitExceeded")
		}
	})

	t.Run("event_msg error with codex_error_info UsageLimitExceeded", func(t *testing.T) {
		line := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:43.000Z",
			"payload": {
				"type": "error",
				"message": "You've hit your usage limit. Visit https://chatgpt.com/codex/settings/usage to purchase more credits.",
				"codex_error_info": "usage_limit_exceeded"
			}
		}`
		evs, err := ParseCodexLine([]byte(line))
		if err != nil || len(evs) != 1 {
			t.Fatalf("failed error event parsing: evs=%v err=%v", evs, err)
		}
		if evs[0].EventType != "error" || !*evs[0].Data.IsError {
			t.Errorf("unexpected error event: %+v", evs[0])
		}
		if evs[0].Data.IsRateLimit != nil && *evs[0].Data.IsRateLimit {
			t.Errorf("expected isRateLimit = false (quota exhaustion is fatal error, not rate limit) for usage_limit_exceeded")
		}
	})

	t.Run("event_msg error with codex_error_info ServerOverloaded", func(t *testing.T) {
		line := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:43.000Z",
			"payload": {
				"type": "error",
				"message": "Backend server overloaded",
				"codex_error_info": "ServerOverloaded"
			}
		}`
		evs, err := ParseCodexLine([]byte(line))
		if err != nil || len(evs) != 1 {
			t.Fatalf("failed error event parsing: evs=%v err=%v", evs, err)
		}
		if evs[0].EventType != "error" || !*evs[0].Data.IsError {
			t.Errorf("unexpected error event: %+v", evs[0])
		}
		if evs[0].Data.IsRateLimit != nil && *evs[0].Data.IsRateLimit {
			t.Errorf("expected isRateLimit = false for ServerOverloaded")
		}
	})

	t.Run("token_count with both reset_requests and reset_tokens selects max duration", func(t *testing.T) {
		parser := NewCodexParser()
		line := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:43.000Z",
			"payload": {
				"type": "token_count",
				"info": {
					"total_token_usage": { "total_tokens": 1500, "prompt_tokens": 1000, "completion_tokens": 500 },
					"last_token_usage": { "total_tokens": 150, "prompt_tokens": 100, "completion_tokens": 50 },
					"model_context_window": 128000
				},
				"rate_limits": {
					"limit_requests": 500,
					"remaining_requests": 490,
					"reset_requests": "10ms",
					"limit_tokens": 100000,
					"remaining_tokens": 0,
					"reset_tokens": "60s"
				}
			}
		}`
		evs, err := parser.ParseLine([]byte(line))
		if err != nil || len(evs) != 1 {
			t.Fatalf("failed token_count parsing: evs=%v err=%v", evs, err)
		}
		if evs[0].Data.RetryAfterSeconds == nil || *evs[0].Data.RetryAfterSeconds != 60 {
			t.Fatalf("expected RetryAfterSeconds = 60, got %v", evs[0].Data.RetryAfterSeconds)
		}

		// Subsequent error should inherit the token reset (60s)
		errLine := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:44.000Z",
			"payload": {
				"type": "error",
				"message": "Rate limit exceeded",
				"codex_error_info": "RateLimitExceeded"
			}
		}`
		errEvs, err := parser.ParseLine([]byte(errLine))
		if err != nil || len(errEvs) != 1 {
			t.Fatalf("failed error parsing: evs=%v err=%v", errEvs, err)
		}
		if errEvs[0].Data.RetryAfterSeconds == nil || *errEvs[0].Data.RetryAfterSeconds != 59 {
			t.Fatalf("expected error RetryAfterSeconds = 59 (60s discounted by 1s), got %v", errEvs[0].Data.RetryAfterSeconds)
		}
	})

	t.Run("event_msg error with rate_limits selects max reset duration", func(t *testing.T) {
		line := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:43.000Z",
			"payload": {
				"type": "error",
				"message": "Rate limit exceeded",
				"codex_error_info": "RateLimitExceeded",
				"rate_limits": {
					"reset_requests": "10ms",
					"reset_tokens": "2m"
				}
			}
		}`
		evs, err := ParseCodexLine([]byte(line))
		if err != nil || len(evs) != 1 {
			t.Fatalf("failed error parsing: evs=%v err=%v", evs, err)
		}
		if evs[0].Data.RetryAfterSeconds == nil || *evs[0].Data.RetryAfterSeconds != 120 {
			t.Fatalf("expected RetryAfterSeconds = 120, got %v", evs[0].Data.RetryAfterSeconds)
		}
	})

	t.Run("token_count with rate_limits metadata", func(t *testing.T) {
		line := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:43.000Z",
			"payload": {
				"type": "token_count",
				"info": {
					"total_token_usage": {
						"total_tokens": 1500,
						"prompt_tokens": 1000,
						"completion_tokens": 500
					},
					"last_token_usage": {
						"total_tokens": 150,
						"prompt_tokens": 100,
						"completion_tokens": 50
					},
					"model_context_window": 128000
				},
				"rate_limits": {
					"limit_requests": 500,
					"remaining_requests": 0,
					"reset_requests": "45s"
				}
			}
		}`
		evs, err := ParseCodexLine([]byte(line))
		if err != nil || len(evs) != 1 {
			t.Fatalf("failed token_count parsing: evs=%v err=%v", evs, err)
		}
		if evs[0].Data.RetryAfterSeconds == nil || *evs[0].Data.RetryAfterSeconds != 45 {
			t.Errorf("expected RetryAfterSeconds = 45, got %v", evs[0].Data.RetryAfterSeconds)
		}
		if evs[0].Data.RateLimitResetAt == nil {
			t.Errorf("expected RateLimitResetAt to be populated from reset_requests")
		}
	})

	t.Run("event_msg error with codex_error_info HttpConnectionFailed 429", func(t *testing.T) {
		line := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:43.000Z",
			"payload": {
				"type": "error",
				"message": "Failed to connect to backend",
				"codex_error_info": {
					"HttpConnectionFailed": {
						"http_status_code": 429
					}
				}
			}
		}`
		evs, err := ParseCodexLine([]byte(line))
		if err != nil || len(evs) != 1 {
			t.Fatalf("failed error event parsing: evs=%v err=%v", evs, err)
		}
		if evs[0].EventType != "error" {
			t.Errorf("unexpected event type: %+v", evs[0])
		}
		if evs[0].Data.ExitCode != nil {
			t.Errorf("ExitCode = %v, want nil", evs[0].Data.ExitCode)
		}
		if evs[0].Data.IsRateLimit == nil || !*evs[0].Data.IsRateLimit {
			t.Errorf("expected isRateLimit = true for HttpConnectionFailed 429")
		}
	})

	t.Run("event_msg error with codex_error_info HttpConnectionFailed 503", func(t *testing.T) {
		line := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:43.000Z",
			"payload": {
				"type": "error",
				"message": "Service unavailable",
				"codex_error_info": {
					"http_connection_failed": {
						"http_status_code": 503
					}
				}
			}
		}`
		evs, err := ParseCodexLine([]byte(line))
		if err != nil || len(evs) != 1 {
			t.Fatalf("failed error event parsing: evs=%v err=%v", evs, err)
		}
		if evs[0].EventType != "error" {
			t.Errorf("unexpected event type: %+v", evs[0])
		}
		if evs[0].Data.ExitCode != nil {
			t.Errorf("ExitCode = %v, want nil", evs[0].Data.ExitCode)
		}
		if evs[0].Data.IsRateLimit != nil && *evs[0].Data.IsRateLimit {
			t.Errorf("expected isRateLimit = false/nil for 503")
		}
	})

	t.Run("parseCodexRateLimitReset variants", func(t *testing.T) {
		nowMs := int64(1700000000000)

		// 1. Duration string
		resetAt, sec := parseCodexRateLimitReset("30s", nowMs)
		if resetAt == nil || *resetAt != nowMs+30000 || sec == nil || *sec != 30 {
			t.Errorf("failed duration: resetAt=%v, sec=%v", resetAt, sec)
		}

		// 2. Numeric seconds
		resetAt, sec = parseCodexRateLimitReset("60", nowMs)
		if resetAt == nil || *resetAt != nowMs+60000 || sec == nil || *sec != 60 {
			t.Errorf("failed numeric: resetAt=%v, sec=%v", resetAt, sec)
		}

		// 3. RFC3339
		rfcStr := "2026-06-12T16:08:43Z"
		parsedT, _ := time.Parse(time.RFC3339, rfcStr)
		resetAt, sec = parseCodexRateLimitReset(rfcStr, nowMs)
		expectedSec := (parsedT.UnixMilli() - nowMs) / 1000
		if resetAt == nil || *resetAt != parsedT.UnixMilli() || sec == nil || *sec != expectedSec {
			t.Errorf("failed RFC3339: resetAt=%v, sec=%v", resetAt, sec)
		}

		// 4. RFC3339Nano
		rfcNanoStr := "2026-06-12T16:08:43.123456789Z"
		parsedNanoT, _ := time.Parse(time.RFC3339Nano, rfcNanoStr)
		resetAt, sec = parseCodexRateLimitReset(rfcNanoStr, nowMs)
		expectedNanoSec := (parsedNanoT.UnixMilli() - nowMs) / 1000
		if resetAt == nil || *resetAt != parsedNanoT.UnixMilli() || sec == nil || *sec != expectedNanoSec {
			t.Errorf("failed RFC3339Nano: resetAt=%v, sec=%v", resetAt, sec)
		}

		// 5. Empty and invalid
		resetAt, sec = parseCodexRateLimitReset("", nowMs)
		if resetAt != nil || sec != nil {
			t.Errorf("expected nil for empty")
		}
		resetAt, sec = parseCodexRateLimitReset("invalid-string", nowMs)
		if resetAt != nil || sec != nil {
			t.Errorf("expected nil for invalid")
		}
	})

	t.Run("ParseCodexLine is stateless and does not persist state across calls", func(t *testing.T) {
		tokenLine := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:43.000Z",
			"payload": {
				"type": "token_count",
				"info": {
					"total_token_usage": {"total_tokens": 100, "prompt_tokens": 80, "completion_tokens": 20},
					"last_token_usage": {"total_tokens": 100, "prompt_tokens": 80, "completion_tokens": 20}
				},
				"rate_limits": {
					"remaining_tokens": 0,
					"reset_tokens": "60s"
				}
			}
		}`
		errLine := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:44.000Z",
			"payload": {
				"type": "error",
				"message": "Rate limit exceeded",
				"codex_error_info": "RateLimitExceeded"
			}
		}`

		// Using ParseCodexLine (stateless): error line should NOT inherit rate limit reset info
		_, _ = ParseCodexLine([]byte(tokenLine))
		evs, err := ParseCodexLine([]byte(errLine))
		if err != nil || len(evs) != 1 {
			t.Fatalf("ParseCodexLine failed: %v", err)
		}
		if evs[0].Data.RetryAfterSeconds != nil {
			t.Errorf("expected nil RetryAfterSeconds for stateless ParseCodexLine, got %v", *evs[0].Data.RetryAfterSeconds)
		}

		// Using NewCodexParser (stateful): error line DOES inherit rate limit reset info
		inst := NewCodexParser()
		_, _ = inst.ParseLine([]byte(tokenLine))
		evsStateful, err := inst.ParseLine([]byte(errLine))
		if err != nil || len(evsStateful) != 1 {
			t.Fatalf("inst.ParseLine failed: %v", err)
		}
		if evsStateful[0].Data.RetryAfterSeconds == nil || *evsStateful[0].Data.RetryAfterSeconds != 59 {
			t.Errorf("expected RetryAfterSeconds 59 for stateful parser, got %v", evsStateful[0].Data.RetryAfterSeconds)
		}
	})

	t.Run("event_msg error with RateLimits on payload", func(t *testing.T) {
		parser := NewCodexParser()
		line := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:43.000Z",
			"payload": {
				"type": "error",
				"message": "Rate limit exceeded on request",
				"codex_error_info": "RateLimitExceeded",
				"rate_limits": {
					"reset_tokens": "20s"
				}
			}
		}`
		evs, err := parser.ParseLine([]byte(line))
		if err != nil || len(evs) != 1 {
			t.Fatalf("failed parse: evs=%v err=%v", evs, err)
		}
		if evs[0].Data.RetryAfterSeconds == nil || *evs[0].Data.RetryAfterSeconds != 20 {
			t.Errorf("expected RetryAfterSeconds = 20, got %v", evs[0].Data.RetryAfterSeconds)
		}
	})

	t.Run("token_count does not extract reset metadata when quota is not exhausted", func(t *testing.T) {
		parser := NewCodexParser()
		line := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:43.000Z",
			"payload": {
				"type": "token_count",
				"info": {
					"total_token_usage": { "total_tokens": 100 }
				},
				"rate_limits": {
					"remaining_tokens": 50000,
					"remaining_requests": 200,
					"reset_tokens": "30s",
					"reset_requests": "10s"
				}
			}
		}`
		evs, err := parser.ParseLine([]byte(line))
		if err != nil || len(evs) != 1 {
			t.Fatalf("failed parse: evs=%v err=%v", evs, err)
		}
		if evs[0].Data.RateLimitResetAt != nil {
			t.Errorf("expected RateLimitResetAt == nil when quota not exhausted, got %v", evs[0].Data.RateLimitResetAt)
		}
		if evs[0].Data.RetryAfterSeconds != nil {
			t.Errorf("expected RetryAfterSeconds == nil when quota not exhausted, got %v", evs[0].Data.RetryAfterSeconds)
		}
	})

	t.Run("token_count extracts reset metadata when remaining_tokens is 0", func(t *testing.T) {
		parser := NewCodexParser()
		line := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:43.000Z",
			"payload": {
				"type": "token_count",
				"info": {
					"total_token_usage": { "total_tokens": 100 }
				},
				"rate_limits": {
					"remaining_tokens": 0,
					"remaining_requests": 200,
					"reset_tokens": "30s",
					"reset_requests": "10s"
				}
			}
		}`
		evs, err := parser.ParseLine([]byte(line))
		if err != nil || len(evs) != 1 {
			t.Fatalf("failed parse: evs=%v err=%v", evs, err)
		}
		if evs[0].Data.RetryAfterSeconds == nil || *evs[0].Data.RetryAfterSeconds != 30 {
			t.Errorf("expected RetryAfterSeconds == 30, got %v", evs[0].Data.RetryAfterSeconds)
		}
	})

	t.Run("token_count extracts reset metadata when remaining_requests is 0", func(t *testing.T) {
		parser := NewCodexParser()
		line := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:43.000Z",
			"payload": {
				"type": "token_count",
				"info": {
					"total_token_usage": { "total_tokens": 100 }
				},
				"rate_limits": {
					"remaining_tokens": 50000,
					"remaining_requests": 0,
					"reset_tokens": "10s",
					"reset_requests": "45s"
				}
			}
		}`
		evs, err := parser.ParseLine([]byte(line))
		if err != nil || len(evs) != 1 {
			t.Fatalf("failed parse: evs=%v err=%v", evs, err)
		}
		if evs[0].Data.RetryAfterSeconds == nil || *evs[0].Data.RetryAfterSeconds != 45 {
			t.Errorf("expected RetryAfterSeconds == 45, got %v", evs[0].Data.RetryAfterSeconds)
		}
	})

	t.Run("token_count isolates exhausted quota: tokens exhausted ignores requests reset", func(t *testing.T) {
		parser := NewCodexParser()
		line := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:43.000Z",
			"payload": {
				"type": "token_count",
				"info": {
					"total_token_usage": { "total_tokens": 100 }
				},
				"rate_limits": {
					"remaining_tokens": 0,
					"remaining_requests": 200,
					"reset_tokens": "15s",
					"reset_requests": "2h"
				}
			}
		}`
		evs, err := parser.ParseLine([]byte(line))
		if err != nil || len(evs) != 1 {
			t.Fatalf("failed parse: evs=%v err=%v", evs, err)
		}
		if evs[0].Data.RetryAfterSeconds == nil || *evs[0].Data.RetryAfterSeconds != 15 {
			t.Errorf("expected RetryAfterSeconds == 15 (ignored non-exhausted 2h requests reset), got %v", evs[0].Data.RetryAfterSeconds)
		}
	})

	t.Run("token_count isolates exhausted quota: requests exhausted ignores tokens reset", func(t *testing.T) {
		parser := NewCodexParser()
		line := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:43.000Z",
			"payload": {
				"type": "token_count",
				"info": {
					"total_token_usage": { "total_tokens": 100 }
				},
				"rate_limits": {
					"remaining_tokens": 50000,
					"remaining_requests": 0,
					"reset_tokens": "3h",
					"reset_requests": "20s"
				}
			}
		}`
		evs, err := parser.ParseLine([]byte(line))
		if err != nil || len(evs) != 1 {
			t.Fatalf("failed parse: evs=%v err=%v", evs, err)
		}
		if evs[0].Data.RetryAfterSeconds == nil || *evs[0].Data.RetryAfterSeconds != 20 {
			t.Errorf("expected RetryAfterSeconds == 20 (ignored non-exhausted 3h tokens reset), got %v", evs[0].Data.RetryAfterSeconds)
		}
	})

	t.Run("selectCodexRateLimitReset takes max of resetRequests and resetTokens", func(t *testing.T) {
		parser := NewCodexParser()
		line := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:43.000Z",
			"payload": {
				"type": "error",
				"message": "Rate limit exceeded",
				"rate_limits": {
					"remaining_tokens": 0,
					"reset_requests": "60s",
					"reset_tokens": "15s"
				}
			}
		}`
		evs, err := parser.ParseLine([]byte(line))
		if err != nil || len(evs) != 1 {
			t.Fatalf("failed parse: evs=%v err=%v", evs, err)
		}
		if evs[0].Data.RetryAfterSeconds == nil || *evs[0].Data.RetryAfterSeconds != 60 {
			t.Errorf("expected max(60, 15) = 60s even when remaining_tokens=0, got %v", evs[0].Data.RetryAfterSeconds)
		}
	})

	t.Run("cached rateLimitResetAt invalidated once expired", func(t *testing.T) {
		parser := NewCodexParser()
		// First line: error at 16:00:00 with reset in 10s (expires 16:00:10)
		line1 := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:00:00.000Z",
			"payload": {
				"type": "error",
				"message": "Rate limit reached",
				"rate_limits": {
					"reset_tokens": "10s"
				}
			}
		}`
		evs1, err := parser.ParseLine([]byte(line1))
		if err != nil || len(evs1) != 1 {
			t.Fatalf("failed parse 1: evs=%v err=%v", evs1, err)
		}
		if evs1[0].Data.RetryAfterSeconds == nil || *evs1[0].Data.RetryAfterSeconds != 10 {
			t.Fatalf("expected RetryAfterSeconds = 10, got %v", evs1[0].Data.RetryAfterSeconds)
		}

		// Second line: error at 16:00:20 (20s later, reset has expired) without rate_limits
		line2 := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:00:20.000Z",
			"payload": {
				"type": "error",
				"message": "Rate limit reached again"
			}
		}`
		evs2, err := parser.ParseLine([]byte(line2))
		if err != nil || len(evs2) != 1 {
			t.Fatalf("failed parse 2: evs=%v err=%v", evs2, err)
		}
		if evs2[0].Data.RateLimitResetAt != nil {
			t.Errorf("expected cached RateLimitResetAt to be invalidated after expiration, got %v", evs2[0].Data.RateLimitResetAt)
		}
		if evs2[0].Data.RetryAfterSeconds != nil {
			t.Errorf("expected cached RetryAfterSeconds to be invalidated after expiration, got %v", evs2[0].Data.RetryAfterSeconds)
		}
	})

	t.Run("event_msg error with codex_error_info HttpConnectionFailed 503 does not fallback to substring match", func(t *testing.T) {
		line := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:43.000Z",
			"payload": {
				"type": "error",
				"message": "Service unavailable: internal server error while evaluating rate limit policy",
				"codex_error_info": {
					"http_connection_failed": {
						"http_status_code": 503
					}
				}
			}
		}`
		evs, err := ParseCodexLine([]byte(line))
		if err != nil || len(evs) != 1 {
			t.Fatalf("failed error event parsing: evs=%v err=%v", evs, err)
		}
		if evs[0].EventType != "error" {
			t.Errorf("unexpected event type: %+v", evs[0])
		}
		if evs[0].Data.ExitCode != nil {
			t.Errorf("ExitCode = %v, want nil", evs[0].Data.ExitCode)
		}
		if evs[0].Data.IsRateLimit == nil || *evs[0].Data.IsRateLimit {
			t.Errorf("expected isRateLimit = false for 503, got %v", evs[0].Data.IsRateLimit)
		}
	})

	t.Run("stateful codex parser attaches rate limit metadata to error event", func(t *testing.T) {
		parser := NewCodexParser()
		tokenCountLine := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:40.000Z",
			"payload": {
				"type": "token_count",
				"info": {
					"total_token_usage": {"total_tokens": 1000, "prompt_tokens": 800, "completion_tokens": 200},
					"last_token_usage": {"total_tokens": 100, "prompt_tokens": 80, "completion_tokens": 20}
				},
				"rate_limits": {
					"remaining_requests": 0,
					"reset_requests": "45s"
				}
			}
		}`
		_, err := parser.ParseLine([]byte(tokenCountLine))
		if err != nil {
			t.Fatalf("token_count parse failed: %v", err)
		}

		errorLine := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:42.000Z",
			"payload": {
				"type": "error",
				"message": "Rate limit exceeded",
				"codex_error_info": "RateLimitExceeded"
			}
		}`
		evs, err := parser.ParseLine([]byte(errorLine))
		if err != nil || len(evs) != 1 {
			t.Fatalf("errorLine parse failed: evs=%v err=%v", evs, err)
		}
		errEv := evs[0]
		if errEv.EventType != "error" {
			t.Fatalf("expected error event, got %s", errEv.EventType)
		}
		if errEv.Data.IsRateLimit == nil || !*errEv.Data.IsRateLimit {
			t.Errorf("expected IsRateLimit = true")
		}
		if errEv.Data.RetryAfterSeconds == nil || *errEv.Data.RetryAfterSeconds != 43 {
			t.Errorf("expected RetryAfterSeconds = 43 (45s discounted by 2s), got %v", errEv.Data.RetryAfterSeconds)
		}
		if errEv.Data.RateLimitResetAt == nil {
			t.Errorf("expected RateLimitResetAt to be set")
		}

		// Reset should clear parser metadata
		parser.Reset()
		evs2, err := parser.ParseLine([]byte(errorLine))
		if err != nil || len(evs2) != 1 {
			t.Fatalf("second errorLine parse failed: %v", err)
		}
		if evs2[0].Data.RetryAfterSeconds != nil {
			t.Errorf("expected nil RetryAfterSeconds after reset, got %v", evs2[0].Data.RetryAfterSeconds)
		}
	})

	t.Run("unrelated errors are not classified as rate-limited even with active cached reset timestamp", func(t *testing.T) {
		parser := NewCodexParser()
		tokenCountLine := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:00.000Z",
			"payload": {
				"type": "token_count",
				"info": {
					"total_tokens": 100
				},
				"rate_limits": {
					"remaining_requests": 0,
					"reset_requests": "60s"
				}
			}
		}`
		_, err := parser.ParseLine([]byte(tokenCountLine))
		if err != nil {
			t.Fatalf("token_count parse failed: %v", err)
		}

		// Generic unclassified error without explicit rate limit words or 429 code
		errorLine := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:10.000Z",
			"payload": {
				"type": "error",
				"message": "Stream interrupted connection reset by peer"
			}
		}`
		evs, err := parser.ParseLine([]byte(errorLine))
		if err != nil || len(evs) != 1 {
			t.Fatalf("errorLine parse failed: evs=%v err=%v", evs, err)
		}
		errEv := evs[0]
		if errEv.EventType != "error" {
			t.Fatalf("expected error event, got %s", errEv.EventType)
		}
		if errEv.Data.IsRateLimit != nil && *errEv.Data.IsRateLimit {
			t.Fatalf("expected unclassified error to NOT be classified as rate limit, got %v", errEv.Data.IsRateLimit)
		}
		if errEv.Data.RateLimitResetAt != nil {
			t.Fatalf("expected unclassified error to have nil RateLimitResetAt, got %v", errEv.Data.RateLimitResetAt)
		}
		if errEv.Data.RetryAfterSeconds != nil {
			t.Fatalf("expected unclassified error to have nil RetryAfterSeconds, got %v", errEv.Data.RetryAfterSeconds)
		}

		// Tool crash error
		toolCrashLine := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:12.000Z",
			"payload": {
				"type": "error",
				"message": "Tool execution failed: exit status 1"
			}
		}`
		evsCrash, err := parser.ParseLine([]byte(toolCrashLine))
		if err != nil || len(evsCrash) != 1 {
			t.Fatalf("toolCrashLine parse failed: evs=%v err=%v", evsCrash, err)
		}
		if evsCrash[0].Data.IsRateLimit != nil && *evsCrash[0].Data.IsRateLimit {
			t.Fatalf("expected tool crash to NOT be classified as rate limit, got %v", evsCrash[0].Data.IsRateLimit)
		}
		if evsCrash[0].Data.RateLimitResetAt != nil {
			t.Fatalf("expected tool crash to have nil RateLimitResetAt, got %v", evsCrash[0].Data.RateLimitResetAt)
		}

		// Syntax error
		syntaxErrLine := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:14.000Z",
			"payload": {
				"type": "error",
				"message": "SyntaxError: Unexpected token < in JSON at position 0"
			}
		}`
		evsSyntax, err := parser.ParseLine([]byte(syntaxErrLine))
		if err != nil || len(evsSyntax) != 1 {
			t.Fatalf("syntaxErrLine parse failed: evs=%v err=%v", evsSyntax, err)
		}
		if evsSyntax[0].Data.IsRateLimit != nil && *evsSyntax[0].Data.IsRateLimit {
			t.Fatalf("expected syntax error to NOT be classified as rate limit, got %v", evsSyntax[0].Data.IsRateLimit)
		}
		if evsSyntax[0].Data.RateLimitResetAt != nil {
			t.Fatalf("expected syntax error to have nil RateLimitResetAt, got %v", evsSyntax[0].Data.RateLimitResetAt)
		}

		// Positively identified rate limit DOES inherit active cached rate limit metadata
		rlLine := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:16.000Z",
			"payload": {
				"type": "error",
				"message": "Rate limit reached for requests per minute",
				"codex_error_info": "RateLimitExceeded"
			}
		}`
		evsRL, err := parser.ParseLine([]byte(rlLine))
		if err != nil || len(evsRL) != 1 {
			t.Fatalf("rlLine parse failed: evs=%v err=%v", evsRL, err)
		}
		if evsRL[0].Data.IsRateLimit == nil || !*evsRL[0].Data.IsRateLimit {
			t.Fatalf("expected positively identified rate limit to have IsRateLimit = true, got %v", evsRL[0].Data.IsRateLimit)
		}
		if evsRL[0].Data.RateLimitResetAt == nil {
			t.Fatalf("expected positively identified rate limit to inherit cached RateLimitResetAt")
		}
		if evsRL[0].Data.RetryAfterSeconds == nil {
			t.Fatalf("expected positively identified rate limit to inherit cached RetryAfterSeconds")
		}

		// Explicit non-429 error (HTTP 500) preserves IsRateLimit = false and does NOT attach cached rate limit metadata even if prior cached reset timestamps exist
		error500Line := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:15.000Z",
			"payload": {
				"type": "error",
				"message": "Gateway error",
				"codex_error_info": {
					"HttpConnectionFailed": {
						"http_status_code": 500
					}
				}
			}
		}`
		evs500, err := parser.ParseLine([]byte(error500Line))
		if err != nil || len(evs500) != 1 {
			t.Fatalf("error500Line parse failed: evs=%v err=%v", evs500, err)
		}
		if evs500[0].Data.IsRateLimit == nil || *evs500[0].Data.IsRateLimit {
			t.Fatalf("expected error event with HTTP 500 to have IsRateLimit = false, got %v", evs500[0].Data.IsRateLimit)
		}
		if evs500[0].Data.RateLimitResetAt != nil {
			t.Fatalf("expected error event with HTTP 500 to NOT attach RateLimitResetAt, got %v", evs500[0].Data.RateLimitResetAt)
		}
		if evs500[0].Data.RetryAfterSeconds != nil {
			t.Fatalf("expected error event with HTTP 500 to NOT attach RetryAfterSeconds, got %v", evs500[0].Data.RetryAfterSeconds)
		}

		// Explicit non-rate-limit error (UsageLimitExceeded) preserves IsRateLimit = false and does NOT attach cached rate limit metadata even if prior cached reset timestamps exist
		usageLimitLine := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:18.000Z",
			"payload": {
				"type": "error",
				"message": "Usage limit reached for this month",
				"codex_error_info": "UsageLimitExceeded"
			}
		}`
		evsUsage, err := parser.ParseLine([]byte(usageLimitLine))
		if err != nil || len(evsUsage) != 1 {
			t.Fatalf("usageLimitLine parse failed: evs=%v err=%v", evsUsage, err)
		}
		if evsUsage[0].Data.IsRateLimit == nil || *evsUsage[0].Data.IsRateLimit {
			t.Fatalf("expected error event with UsageLimitExceeded to have IsRateLimit = false, got %v", evsUsage[0].Data.IsRateLimit)
		}
		if evsUsage[0].Data.RateLimitResetAt != nil {
			t.Fatalf("expected error event with UsageLimitExceeded to NOT attach RateLimitResetAt, got %v", evsUsage[0].Data.RateLimitResetAt)
		}
		if evsUsage[0].Data.RetryAfterSeconds != nil {
			t.Fatalf("expected error event with UsageLimitExceeded to NOT attach RetryAfterSeconds, got %v", evsUsage[0].Data.RetryAfterSeconds)
		}
	})

	t.Run("turn with positive quota availability restores quota and clears cached rate limit metadata", func(t *testing.T) {
		parser := NewCodexParser()

		// 1. Initial event with exhausted requests sets cached rate limit metadata
		tokenCountLine := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:00.000Z",
			"payload": {
				"type": "token_count",
				"info": {
					"total_tokens": 100
				},
				"rate_limits": {
					"remaining_requests": 0,
					"reset_requests": "60s"
				}
			}
		}`
		evs1, err := parser.ParseLine([]byte(tokenCountLine))
		if err != nil || len(evs1) != 1 {
			t.Fatalf("first token_count parse failed: evs=%v err=%v", evs1, err)
		}
		if evs1[0].Data.RateLimitResetAt == nil {
			t.Fatalf("expected initial token_count to set RateLimitResetAt")
		}

		// 2. Subsequent turn confirming positive quota availability clears cached rate limit
		restoredTurnLine := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:10.000Z",
			"payload": {
				"type": "token_count",
				"info": {
					"total_tokens": 150
				},
				"rate_limits": {
					"remaining_requests": 250,
					"remaining_tokens": 50000
				}
			}
		}`
		evs2, err := parser.ParseLine([]byte(restoredTurnLine))
		if err != nil || len(evs2) != 1 {
			t.Fatalf("restored token_count parse failed: evs=%v err=%v", evs2, err)
		}
		if evs2[0].Data.RateLimitResetAt != nil {
			t.Fatalf("expected restored turn to have nil RateLimitResetAt, got %v", evs2[0].Data.RateLimitResetAt)
		}
		if evs2[0].Data.RetryAfterSeconds != nil {
			t.Fatalf("expected restored turn to have nil RetryAfterSeconds, got %v", evs2[0].Data.RetryAfterSeconds)
		}

		// 3. Generic error occurring before original 60s window should NOT inherit cached rate limit
		genericErrorLine := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:15.000Z",
			"payload": {
				"type": "error",
				"message": "Connection reset by peer"
			}
		}`
		evs3, err := parser.ParseLine([]byte(genericErrorLine))
		if err != nil || len(evs3) != 1 {
			t.Fatalf("generic error parse failed: evs=%v err=%v", evs3, err)
		}
		if evs3[0].Data.IsRateLimit != nil && *evs3[0].Data.IsRateLimit {
			t.Fatalf("expected generic error after restored quota to NOT be classified as rate limit, got %v", evs3[0].Data.IsRateLimit)
		}
		if evs3[0].Data.RateLimitResetAt != nil {
			t.Fatalf("expected generic error after restored quota to have nil RateLimitResetAt, got %v", evs3[0].Data.RateLimitResetAt)
		}
		if evs3[0].Data.RetryAfterSeconds != nil {
			t.Fatalf("expected generic error after restored quota to have nil RetryAfterSeconds, got %v", evs3[0].Data.RetryAfterSeconds)
		}
	})

	t.Run("cached rate limit not wiped when one quota remains exhausted", func(t *testing.T) {
		parser := NewCodexParser()

		// 1. Initial event with exhausted tokens sets cached rate limit metadata
		initialLine := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:00.000Z",
			"payload": {
				"type": "token_count",
				"info": {
					"total_tokens": 100
				},
				"rate_limits": {
					"remaining_tokens": 0,
					"reset_tokens": "60s"
				}
			}
		}`
		evs1, err := parser.ParseLine([]byte(initialLine))
		if err != nil || len(evs1) != 1 {
			t.Fatalf("initial token_count parse failed: evs=%v err=%v", evs1, err)
		}
		if evs1[0].Data.RateLimitResetAt == nil {
			t.Fatalf("expected initial token_count to set RateLimitResetAt")
		}

		// 2. Subsequent turn where remaining_tokens == 0 and remaining_requests == 100
		// must NOT wipe cached rate limit metadata
		turnLine := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:10.000Z",
			"payload": {
				"type": "token_count",
				"info": {
					"total_tokens": 150
				},
				"rate_limits": {
					"remaining_tokens": 0,
					"remaining_requests": 100
				}
			}
		}`
		evs2, err := parser.ParseLine([]byte(turnLine))
		if err != nil || len(evs2) != 1 {
			t.Fatalf("subsequent token_count parse failed: evs=%v err=%v", evs2, err)
		}

		// 3. Subsequent RateLimitExceeded error arrives without rate_limits object
		// It must still inherit the cached RateLimitResetAt because the cache was NOT wiped.
		rlErrorLine := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:15.000Z",
			"payload": {
				"type": "error",
				"message": "Rate limit reached for model tokens",
				"codex_error_info": "RateLimitExceeded"
			}
		}`
		evs3, err := parser.ParseLine([]byte(rlErrorLine))
		if err != nil || len(evs3) != 1 {
			t.Fatalf("RateLimitExceeded parse failed: evs=%v err=%v", evs3, err)
		}
		if evs3[0].Data.RateLimitResetAt == nil {
			t.Fatalf("expected RateLimitExceeded to inherit cached RateLimitResetAt when remaining_tokens == 0 and remaining_requests == 100")
		}
	})

	t.Run("turn_end with expired rate limit reset does not attach expired metadata", func(t *testing.T) {
		parser := NewCodexParser()

		// Token count at 16:08:00 with reset_requests at 16:07:00 (in the past <= ts)
		line := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:00.000Z",
			"payload": {
				"type": "token_count",
				"info": {
					"total_tokens": 100
				},
				"rate_limits": {
					"remaining_requests": 0,
					"reset_requests": "2026-06-12T16:07:00.000Z"
				}
			}
		}`
		evs, err := parser.ParseLine([]byte(line))
		if err != nil || len(evs) != 1 {
			t.Fatalf("token_count parse failed: evs=%v err=%v", evs, err)
		}
		if evs[0].Data.RateLimitResetAt != nil {
			t.Fatalf("expected turn_end to have nil RateLimitResetAt when reset is expired (<= ts), got %v", *evs[0].Data.RateLimitResetAt)
		}
		if evs[0].Data.RetryAfterSeconds != nil {
			t.Fatalf("expected turn_end to have nil RetryAfterSeconds when reset is expired, got %v", *evs[0].Data.RetryAfterSeconds)
		}
	})

	t.Run("event_msg error excludes non-exhausted quotas when selecting reset duration", func(t *testing.T) {
		parser := NewCodexParser()
		line := `{
			"type": "event_msg",
			"timestamp": "2026-06-12T16:08:43.000Z",
			"payload": {
				"type": "error",
				"message": "Rate limit exceeded",
				"codex_error_info": "RateLimitExceeded",
				"rate_limits": {
					"remaining_requests": 0,
					"reset_requests": "15s",
					"remaining_tokens": 100000,
					"reset_tokens": "24h"
				}
			}
		}`
		evs, err := parser.ParseLine([]byte(line))
		if err != nil || len(evs) != 1 {
			t.Fatalf("failed parse: evs=%v err=%v", evs, err)
		}
		if evs[0].Data.RetryAfterSeconds == nil || *evs[0].Data.RetryAfterSeconds != 15 {
			t.Fatalf("expected RetryAfterSeconds == 15, got %v", evs[0].Data.RetryAfterSeconds)
		}
	})

	t.Run("stateful codex parser parse and reset", func(t *testing.T) {
		parser := NewCodexParser()
		line := `{"type": "turn_context", "payload": {"model": "gpt-5.4"}}`
		evs, err := parser.ParseLine([]byte(line))
		if err != nil {
			t.Fatalf("parser.ParseLine failed: %v", err)
		}
		if len(evs) != 1 || evs[0].EventType != "model_change" {
			t.Errorf("expected 1 model_change event for turn_context, got %d (%+v)", len(evs), evs)
		}
		parser.Reset()
	})

	t.Run("extractInt cases", func(t *testing.T) {
		if got := extractInt(float64(429)); got == nil || *got != 429 {
			t.Errorf("extractInt(float64) = %v, want 429", got)
		}
		if got := extractInt(int(500)); got == nil || *got != 500 {
			t.Errorf("extractInt(int) = %v, want 500", got)
		}
		if got := extractInt(int64(503)); got == nil || *got != 503 {
			t.Errorf("extractInt(int64) = %v, want 503", got)
		}
		if got := extractInt("invalid"); got != nil {
			t.Errorf("extractInt(string) = %v, want nil", got)
		}
	})

	t.Run("response_item function_call exec_command with JSON-encoded arguments", func(t *testing.T) {
		line := `{
			"type": "response_item",
			"timestamp": "2026-06-12T16:08:44.000Z",
			"payload": {
				"type": "function_call",
				"name": "exec_command",
				"call_id": "call_exec_001",
				"arguments": "{\"cmd\": \"go test -v ./...\", \"workdir\": \"/project\"}"
			}
		}`
		evs, err := ParseCodexLine([]byte(line))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(evs) != 1 {
			t.Fatalf("expected 1 event, got %d", len(evs))
		}
		if evs[0].EventType != "tool_call" {
			t.Errorf("EventType = %q, want 'tool_call'", evs[0].EventType)
		}
		if *evs[0].Data.ToolName != "exec_command" || *evs[0].Data.ToolCallID != "call_exec_001" {
			t.Errorf("ToolName or ToolCallID mismatch: %+v", evs[0].Data)
		}
		if evs[0].Data.ToolInput["cmd"] != "go test -v ./..." || evs[0].Data.ToolInput["command"] != "go test -v ./..." {
			t.Errorf("ToolInput command mismatch: %+v", evs[0].Data.ToolInput)
		}
	})

	t.Run("response_item function_call spawn_agent", func(t *testing.T) {
		line := `{
			"type": "response_item",
			"timestamp": "2026-06-12T16:08:45.000Z",
			"payload": {
				"type": "function_call",
				"name": "spawn_agent",
				"call_id": "call_spawn_002",
				"arguments": "{\"agent_type\": \"explorer\", \"message\": \"Find all endpoints in router.go\"}"
			}
		}`
		evs, err := ParseCodexLine([]byte(line))
		if err != nil || len(evs) != 1 {
			t.Fatalf("failed spawn_agent parsing: evs=%v err=%v", evs, err)
		}
		if evs[0].EventType != "tool_call" || *evs[0].Data.ToolName != "spawn_agent" {
			t.Errorf("tool_call mismatch: %+v", evs[0])
		}
		if evs[0].Data.ToolInput["prompt"] != "Find all endpoints in router.go" {
			t.Errorf("prompt mapping mismatch: %+v", evs[0].Data.ToolInput)
		}
	})

	t.Run("response_item function_call AskUserQuestion", func(t *testing.T) {
		line := `{
			"type": "response_item",
			"timestamp": "2026-06-12T16:08:46.000Z",
			"payload": {
				"type": "function_call",
				"name": "AskUserQuestion",
				"call_id": "call_ask_003",
				"arguments": "{\"question\": \"Should I apply migration 002?\"}"
			}
		}`
		evs, err := ParseCodexLine([]byte(line))
		if err != nil || len(evs) != 1 {
			t.Fatalf("failed AskUserQuestion parsing: evs=%v err=%v", evs, err)
		}
		if evs[0].EventType != "question_prompt" || *evs[0].Data.Question != "Should I apply migration 002?" {
			t.Errorf("question_prompt mismatch: %+v", evs[0])
		}
	})

	t.Run("response_item function_call_output success and error", func(t *testing.T) {
		successLine := `{
			"type": "response_item",
			"timestamp": "2026-06-12T16:08:47.000Z",
			"payload": {
				"type": "function_call_output",
				"call_id": "call_exec_001",
				"output": "Exit code: 0\nPASS\nok packages/agent-parser"
			}
		}`
		evs1, err := ParseCodexLine([]byte(successLine))
		if err != nil || len(evs1) != 1 {
			t.Fatalf("failed function_call_output parsing: evs=%v err=%v", evs1, err)
		}
		if evs1[0].EventType != "tool_result" || *evs1[0].Data.ToolCallID != "call_exec_001" || *evs1[0].Data.IsError {
			t.Errorf("unexpected success tool_result: %+v", evs1[0])
		}

		errorLine := `{
			"type": "response_item",
			"timestamp": "2026-06-12T16:08:48.000Z",
			"payload": {
				"type": "function_call_output",
				"call_id": "call_exec_002",
				"output": "Exit code: 1\nFAIL\ncommand failed with exit code 1"
			}
		}`
		evs2, err := ParseCodexLine([]byte(errorLine))
		if err != nil || len(evs2) != 1 {
			t.Fatalf("failed function_call_output error parsing: evs=%v err=%v", evs2, err)
		}
		if evs2[0].EventType != "tool_result" || !*evs2[0].Data.IsError {
			t.Errorf("expected isError true for exit code 1, got %+v", evs2[0])
		}
	})

	t.Run("response_item message user and assistant", func(t *testing.T) {
		userLine := `{
			"type": "response_item",
			"timestamp": "2026-06-12T16:08:49.000Z",
			"payload": {
				"type": "message",
				"role": "user",
				"content": [
					{"type": "input_text", "text": "Run the tests"}
				]
			}
		}`
		evs1, err := ParseCodexLine([]byte(userLine))
		if err != nil || len(evs1) != 1 {
			t.Fatalf("failed user response_item message: evs=%v err=%v", evs1, err)
		}
		if evs1[0].EventType != "turn_start" || *evs1[0].Data.Content != "Run the tests" {
			t.Errorf("unexpected turn_start: %+v", evs1[0])
		}

		assistantLine := `{
			"type": "response_item",
			"timestamp": "2026-06-12T16:08:50.000Z",
			"payload": {
				"type": "message",
				"role": "assistant",
				"content": [
					{"type": "output_text", "text": "Tests completed successfully."}
				]
			}
		}`
		evs2, err := ParseCodexLine([]byte(assistantLine))
		if err != nil || len(evs2) != 1 {
			t.Fatalf("failed assistant response_item message: evs=%v err=%v", evs2, err)
		}
		if evs2[0].EventType != "agent_message" || *evs2[0].Data.Content != "Tests completed successfully." {
			t.Errorf("unexpected agent_message: %+v", evs2[0])
		}
	})

	t.Run("response_item tool_search_call and tool_search_output", func(t *testing.T) {
		callLine := `{
			"type": "response_item",
			"timestamp": "2026-06-12T16:08:51.000Z",
			"payload": {
				"type": "tool_search_call",
				"call_id": "ts_001",
				"query": "find files matching *.go"
			}
		}`
		evs1, err := ParseCodexLine([]byte(callLine))
		if err != nil || len(evs1) != 1 {
			t.Fatalf("failed tool_search_call parsing: evs=%v err=%v", evs1, err)
		}
		if evs1[0].EventType != "tool_call" || *evs1[0].Data.ToolName != "tool_search" {
			t.Errorf("unexpected tool_search_call event: %+v", evs1[0])
		}

		outLine := `{
			"type": "response_item",
			"timestamp": "2026-06-12T16:08:52.000Z",
			"payload": {
				"type": "tool_search_output",
				"call_id": "ts_001",
				"output": "Found 12 matching files"
			}
		}`
		evs2, err := ParseCodexLine([]byte(outLine))
		if err != nil || len(evs2) != 1 {
			t.Fatalf("failed tool_search_output parsing: evs=%v err=%v", evs2, err)
		}
		if evs2[0].EventType != "tool_result" || *evs2[0].Data.ToolCallID != "ts_001" {
			t.Errorf("unexpected tool_search_output event: %+v", evs2[0])
		}
	})

	t.Run("session_meta subagent with only nickname", func(t *testing.T) {
		line := []byte(`{"type":"session_meta","timestamp":"2026-06-12T16:08:36.000Z","payload":{"id":"sub-1","agent_nickname":"Scout"}}`)
		evs, err := ParseCodexLine(line)
		if err != nil || len(evs) != 1 {
			t.Fatalf("unexpected result: %v", err)
		}
		if *evs[0].Data.Title != "Codex Subagent: Scout" {
			t.Errorf("got %q, want 'Codex Subagent: Scout'", *evs[0].Data.Title)
		}
	})

	t.Run("session_meta subagent with only parentThreadID", func(t *testing.T) {
		line := []byte(`{"type":"session_meta","timestamp":"2026-06-12T16:08:36.000Z","payload":{"id":"sub-2","parent_thread_id":"root-1"}}`)
		evs, err := ParseCodexLine(line)
		if err != nil || len(evs) != 1 {
			t.Fatalf("unexpected result: %v", err)
		}
		if *evs[0].Data.Title != "Codex Subagent sub-2" {
			t.Errorf("got %q, want 'Codex Subagent sub-2'", *evs[0].Data.Title)
		}
	})

	t.Run("session_meta with string subagent source", func(t *testing.T) {
		line := []byte(`{"type":"session_meta","timestamp":"2026-06-12T16:08:36.000Z","payload":{"id":"sub-3","source":{"subagent":"worker"}}}`)
		evs, err := ParseCodexLine(line)
		if err != nil || len(evs) != 1 {
			t.Fatalf("unexpected result: %v", err)
		}
		if *evs[0].Data.Title != "Codex Subagent: (worker)" {
			t.Errorf("got %q, want 'Codex Subagent: (worker)'", *evs[0].Data.Title)
		}
	})

	t.Run("invalid json payloads in codex line", func(t *testing.T) {
		_, err := ParseCodexLine([]byte(`{"type":"session_meta","payload":"invalid"}`))
		if err == nil {
			t.Error("expected error for invalid session_meta payload")
		}
		_, err = ParseCodexLine([]byte(`{"type":"turn_context","payload":"invalid"}`))
		if err == nil {
			t.Error("expected error for invalid turn_context payload")
		}
		_, err = ParseCodexLine([]byte(`{"type":"event_msg","payload":"invalid"}`))
		if err == nil {
			t.Error("expected error for invalid event_msg payload")
		}
		_, err = ParseCodexLine([]byte(`{"type":"response_item","payload":"invalid"}`))
		if err == nil {
			t.Error("expected error for invalid response_item payload")
		}
	})

	t.Run("session_meta with legacy session_id alias", func(t *testing.T) {
		line := []byte(`{"type":"session_meta","timestamp":"2026-06-12T16:08:36.000Z","payload":{"session_id":"legacy-ses-999","cwd":"/repo"}}`)
		evs, err := ParseCodexLine(line)
		if err != nil || len(evs) != 1 {
			t.Fatalf("unexpected result: evs=%v err=%v", evs, err)
		}
		if evs[0].EventType != "run_started" {
			t.Fatalf("EventType = %q, want 'run_started'", evs[0].EventType)
		}
		if evs[0].Data.Title == nil || *evs[0].Data.Title != "Codex Session legacy-ses-999" {
			t.Errorf("Title = %v, want 'Codex Session legacy-ses-999'", evs[0].Data.Title)
		}
	})

	t.Run("response_item custom_tool_call with arguments", func(t *testing.T) {
		line := []byte(`{
			"type": "response_item",
			"timestamp": "2026-06-12T16:08:44.000Z",
			"payload": {
				"type": "custom_tool_call",
				"name": "mcp_browser_click",
				"call_id": "call_mcp_001",
				"arguments": "{\"element_id\": \"btn-submit\"}"
			}
		}`)
		evs, err := ParseCodexLine(line)
		if err != nil || len(evs) != 1 {
			t.Fatalf("unexpected result: evs=%v err=%v", evs, err)
		}
		if evs[0].EventType != "tool_call" {
			t.Errorf("EventType = %q, want 'tool_call'", evs[0].EventType)
		}
		if *evs[0].Data.ToolName != "mcp_browser_click" || *evs[0].Data.ToolCallID != "call_mcp_001" {
			t.Errorf("ToolName/ToolCallID mismatch: %+v", evs[0].Data)
		}
		if evs[0].Data.ToolInput["element_id"] != "btn-submit" {
			t.Errorf("ToolInput mismatch: %+v", evs[0].Data.ToolInput)
		}
	})

	t.Run("response_item custom_tool_call with input map", func(t *testing.T) {
		line := []byte(`{
			"type": "response_item",
			"timestamp": "2026-06-12T16:08:44.000Z",
			"payload": {
				"type": "custom_tool_call",
				"name": "mcp_search",
				"call_id": "call_mcp_002",
				"input": {"query": "auth handlers"}
			}
		}`)
		evs, err := ParseCodexLine(line)
		if err != nil || len(evs) != 1 {
			t.Fatalf("unexpected result: evs=%v err=%v", evs, err)
		}
		if evs[0].EventType != "tool_call" {
			t.Errorf("EventType = %q, want 'tool_call'", evs[0].EventType)
		}
		if evs[0].Data.ToolInput["query"] != "auth handlers" {
			t.Errorf("ToolInput mismatch: %+v", evs[0].Data.ToolInput)
		}
	})

	t.Run("response_item function_call with input string", func(t *testing.T) {
		line := []byte(`{
			"type": "response_item",
			"timestamp": "2026-06-12T16:08:44.000Z",
			"payload": {
				"type": "function_call",
				"name": "exec_command",
				"call_id": "call_exec_input_str",
				"input": "{\"cmd\": \"git status\"}"
			}
		}`)
		evs, err := ParseCodexLine(line)
		if err != nil || len(evs) != 1 {
			t.Fatalf("unexpected result: evs=%v err=%v", evs, err)
		}
		if evs[0].EventType != "tool_call" {
			t.Errorf("EventType = %q, want 'tool_call'", evs[0].EventType)
		}
		if evs[0].Data.ToolInput["command"] != "git status" {
			t.Errorf("ToolInput command mismatch: %+v", evs[0].Data.ToolInput)
		}
	})

	t.Run("response_item custom_tool_call_output", func(t *testing.T) {
		line := []byte(`{
			"type": "response_item",
			"timestamp": "2026-06-12T16:08:46.000Z",
			"payload": {
				"type": "custom_tool_call_output",
				"call_id": "call_mcp_001",
				"output": "Element clicked successfully"
			}
		}`)
		evs, err := ParseCodexLine(line)
		if err != nil || len(evs) != 1 {
			t.Fatalf("unexpected result: evs=%v err=%v", evs, err)
		}
		if evs[0].EventType != "tool_result" {
			t.Errorf("EventType = %q, want 'tool_result'", evs[0].EventType)
		}
		if *evs[0].Data.ToolCallID != "call_mcp_001" || *evs[0].Data.ToolOutput != "Element clicked successfully" {
			t.Errorf("ToolCallID/ToolOutput mismatch: %+v", evs[0].Data)
		}
		if *evs[0].Data.IsError != false {
			t.Errorf("expected IsError=false, got true")
		}
	})
}

func TestParseCodexRateLimitReset(t *testing.T) {
	nowMs := int64(1700000000000)

	// empty
	resetAt, sec := parseCodexRateLimitReset("", nowMs)
	if resetAt != nil || sec != nil {
		t.Errorf("expected nil for empty")
	}

	// numeric string
	resetAt, sec = parseCodexRateLimitReset("30", nowMs)
	if resetAt == nil || *resetAt != nowMs+30000 || sec == nil || *sec != 30 {
		t.Errorf("numeric string parse failed: %v, %v", resetAt, sec)
	}

	// duration string
	resetAt, sec = parseCodexRateLimitReset("1m", nowMs)
	if resetAt == nil || *resetAt != nowMs+60000 || sec == nil || *sec != 60 {
		t.Errorf("duration string parse failed: %v, %v", resetAt, sec)
	}

	// RFC3339
	resetAt, sec = parseCodexRateLimitReset("2023-11-14T22:14:20Z", nowMs)
	if resetAt == nil || sec == nil {
		t.Errorf("RFC3339 parse failed")
	}

	// RFC3339Nano past
	resetAt, sec = parseCodexRateLimitReset("2020-01-01T00:00:00.000Z", nowMs)
	if resetAt == nil || sec == nil || *sec != 0 {
		t.Errorf("past RFC3339Nano parse failed: %v, %v", resetAt, sec)
	}

	// invalid
	resetAt, sec = parseCodexRateLimitReset("invalid-reset-string", nowMs)
	if resetAt != nil || sec != nil {
		t.Errorf("expected nil for invalid")
	}
}

func TestCodexParser_NormalizedFileModification(t *testing.T) {
	t.Run("apply_patch produces normalized FileModification", func(t *testing.T) {
		patchText := "diff --git a/internal/calc.go b/internal/calc.go\n--- a/internal/calc.go\n+++ b/internal/calc.go\n@@ -1,3 +1,3 @@\n-const X = 1\n+const X = 2\n"
		payload := map[string]interface{}{
			"type": "custom_tool_call",
			"name": "apply_patch",
			"call_id": "call_patch_1",
			"input": map[string]interface{}{
				"patch": patchText,
			},
		}
		raw := map[string]interface{}{
			"type": "response_item",
			"timestamp": "2026-06-12T16:08:40.000Z",
			"payload": payload,
		}
		b, _ := json.Marshal(raw)
		evs, err := ParseCodexLine(b)
		if err != nil {
			t.Fatalf("ParseCodexLine failed: %v", err)
		}
		if len(evs) == 0 {
			t.Fatal("expected events")
		}
		fm := evs[0].Data.FileModification
		if fm == nil {
			t.Fatal("expected non-nil FileModification for Codex apply_patch")
		}
		if fm.Action != "EDIT" {
			t.Errorf("Action = %q, want 'EDIT'", fm.Action)
		}
		if fm.FilePath != "internal/calc.go" {
			t.Errorf("FilePath = %q, want 'internal/calc.go'", fm.FilePath)
		}
		if len(fm.Hunks) != 1 {
			t.Fatalf("expected 1 hunk, got %d", len(fm.Hunks))
		}
		if fm.Hunks[0].Header != "@@ -1,3 +1,3 @@" {
			t.Errorf("Header = %q, want '@@ -1,3 +1,3 @@'", fm.Hunks[0].Header)
		}
	})

	t.Run("write_file produces normalized FileModification", func(t *testing.T) {
		payload := map[string]interface{}{
			"type": "custom_tool_call",
			"name": "write_file",
			"call_id": "call_write_1",
			"input": map[string]interface{}{
				"path": "test.txt",
				"content": "hello world",
			},
		}
		raw := map[string]interface{}{
			"type": "response_item",
			"timestamp": "2026-06-12T16:08:41.000Z",
			"payload": payload,
		}
		b, _ := json.Marshal(raw)
		evs, err := ParseCodexLine(b)
		if err != nil {
			t.Fatalf("ParseCodexLine failed: %v", err)
		}
		if len(evs) == 0 {
			t.Fatal("expected events")
		}
		fm := evs[0].Data.FileModification
		if fm == nil {
			t.Fatal("expected non-nil FileModification for Codex write_file")
		}
		if fm.Action != "WRITE" {
			t.Errorf("Action = %q, want 'WRITE'", fm.Action)
		}
		if fm.FilePath != "test.txt" {
			t.Errorf("FilePath = %q, want 'test.txt'", fm.FilePath)
		}
		if fm.Content == nil || *fm.Content != "hello world" {
			t.Errorf("Content = %v, want 'hello world'", fm.Content)
		}
	})
}
