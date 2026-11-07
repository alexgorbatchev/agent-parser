package parser

import (
	"fmt"
	"testing"
	"time"
)

func TestCodexNativeRateLimitSnapshots(t *testing.T) {
	// Shape captured from a token_count rollout; timestamps and usage are fixed
	// so both exhausted and available windows can be exercised deterministically.
	const now = int64(1792709732000)
	const info = `{"total_token_usage":{"input_tokens":100,"output_tokens":20,"total_tokens":120},"last_token_usage":{"input_tokens":100,"output_tokens":20,"total_tokens":120},"model_context_window":475000}`
	tests := []struct {
		name   string
		limits string
		info   string
		reset  int64
	}{
		{"primary exhausted", `{"limit_id":"codex","limit_name":null,"primary":{"used_percent":100,"window_minutes":10080,"resets_at":1792709792},"secondary":null,"credits":{"has_credits":false,"unlimited":false,"balance":"0"},"plan_type":"promax","rate_limit_reached_type":null}`, info, now + 60000},
		{"primary available", `{"primary":{"used_percent":9,"window_minutes":10080,"resets_at":1792709792},"secondary":null,"rate_limit_reached_type":null}`, info, 0},
		{"secondary exhausted", `{"primary":{"used_percent":9,"resets_at":1792719732},"secondary":{"used_percent":101,"resets_at":1792709762}}`, info, now + 30000},
		{"both exhausted", `{"primary":{"used_percent":100,"resets_at":1792709792},"secondary":{"used_percent":100,"resets_at":1792709762}}`, info, now + 60000},
		{"reached marker", `{"primary":{"used_percent":99.5,"resets_at":1792709792},"rate_limit_reached_type":"rate_limit_reached"}`, info, now + 60000},
		{"no reset", `{"primary":{"used_percent":100,"resets_at":null}}`, info, 0},
		{"expired", `{"primary":{"used_percent":100,"resets_at":1792709732}}`, info, 0},
		{"invalid reset overflow", `{"primary":{"used_percent":100,"resets_at":9223372036854775807}}`, info, 0},
		{"no snapshot", `null`, info, 0},
		{"rate limit without usage", `{"primary":{"used_percent":100,"resets_at":1792709792}}`, `null`, now + 60000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line := fmt.Sprintf(`{"timestamp":"2026-10-22T22:55:32Z","type":"event_msg","payload":{"type":"token_count","info":%s,"rate_limits":%s}}`, tt.info, tt.limits)
			events, err := NewCodexParser().ParseLine([]byte(line))
			if tt.info == "null" {
				if err != nil || len(events) != 0 {
					t.Fatalf("quota-only snapshot fabricated events: %v, %v", events, err)
				}
				return
			}
			if err != nil || len(events) != 1 {
				t.Fatalf("ParseLine = %v, %v; want one event", events, err)
			}
			data := events[0].Data
			if tt.reset == 0 {
				if data.RateLimitResetAt != nil || data.RetryAfterSeconds != nil {
					t.Fatalf("unexpected reset metadata: %+v", data)
				}
			} else if data.RateLimitResetAt == nil || *data.RateLimitResetAt != tt.reset || data.RetryAfterSeconds == nil || *data.RetryAfterSeconds != (tt.reset-now)/1000 {
				t.Fatalf("reset = %v, retry = %v; want %d, %d", data.RateLimitResetAt, data.RetryAfterSeconds, tt.reset, (tt.reset-now)/1000)
			}
			if data.Usage == nil || data.Usage.Total != 120 {
				t.Fatalf("usage lost: %+v", data.Usage)
			}
		})
	}
}

func TestCodexNativeRateLimitRecovery(t *testing.T) {
	tests := []struct {
		name   string
		limits string
		cached bool
	}{
		{"available windows clear cache", `{"primary":{"used_percent":5},"secondary":{"used_percent":30}}`, false},
		{"exhausted window preserves cache", `{"primary":{"used_percent":5},"secondary":{"used_percent":100}}`, true},
		{"reached marker preserves cache", `{"primary":{"used_percent":5},"rate_limit_reached_type":"rate_limit_reached"}`, true},
		{"absent windows preserve cache", `{"primary":null,"secondary":null}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewCodexParser()
			lines := []string{
				`{"timestamp":"2026-10-22T22:55:32Z","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"primary":{"used_percent":100,"resets_at":1792709792}}}}`,
				fmt.Sprintf(`{"timestamp":"2026-10-22T22:55:33Z","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":%s}}`, tt.limits),
				`{"timestamp":"2026-10-22T22:55:34Z","type":"event_msg","payload":{"type":"error","codex_error_info":"RateLimitExceeded","message":"limit reached"}}`,
			}
			for i, line := range lines {
				events, err := p.ParseLine([]byte(line))
				if i < 2 {
					if err != nil || len(events) != 0 {
						t.Fatalf("line %d: quota-only snapshot fabricated events: %v, %v", i, events, err)
					}
					continue
				}
				if err != nil || len(events) != 1 {
					t.Fatalf("line %d: ParseLine = %v, %v", i, events, err)
				}
				if i == 2 {
					if tt.cached {
						if events[0].Data.RetryAfterSeconds == nil || *events[0].Data.RetryAfterSeconds != 58 {
							t.Fatalf("expected cached retry of 58s: %+v", events[0].Data)
						}
					} else if events[0].Data.RateLimitResetAt != nil || events[0].Data.RetryAfterSeconds != nil {
						t.Fatalf("recovered quota retained reset: %+v", events[0].Data)
					}
				}
			}
		})
	}
}

func TestCodexNativeRateLimitFractionalRetry(t *testing.T) {
	line := `{"timestamp":"2026-10-22T22:55:32.576Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":270,"cached_input_tokens":0,"output_tokens":20,"reasoning_output_tokens":0,"total_tokens":290},"last_token_usage":{"input_tokens":270,"cached_input_tokens":0,"output_tokens":20,"reasoning_output_tokens":0,"total_tokens":290}},"rate_limits":{"primary":{"used_percent":100,"resets_at":1792709733}}}}`
	events, err := ParseCodexLine([]byte(line))
	if err != nil || len(events) != 1 {
		t.Fatalf("ParseLine = %v, %v", events, err)
	}
	if got := events[0].Data.RetryAfterSeconds; got == nil || *got != 1 {
		t.Fatalf("retry = %v; want 1 second for a future reset", got)
	}
	if got := events[0].Data.RateLimitResetAt; got == nil || *got != time.Date(2026, 10, 22, 22, 55, 33, 0, time.UTC).UnixMilli() {
		t.Fatalf("reset = %v; want Unix milliseconds", got)
	}
}

func TestCodexQuotaOnlySnapshotDoesNotCompleteTurn(t *testing.T) {
	for _, used := range []int{9, 100} {
		t.Run(fmt.Sprintf("used_%d", used), func(t *testing.T) {
			p := NewCodexParser()
			start := `{"timestamp":"2026-10-22T22:55:31Z","type":"event_msg","payload":{"type":"task_started"}}`
			// The turn's start is held until the turn's first event.
			events, err := p.ParseLine([]byte(start))
			if err != nil || len(events) != 0 {
				t.Fatalf("task start = %v, %v", events, err)
			}
			quota := fmt.Sprintf(`{"timestamp":"2026-10-22T22:55:32Z","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"primary":{"used_percent":%d,"resets_at":1792709792}}}}`, used)
			events, err = p.ParseLine([]byte(quota))
			if err != nil || len(events) != 0 {
				t.Fatalf("quota-only update fabricated events: %v, %v", events, err)
			}
			failure := `{"timestamp":"2026-10-22T22:55:34Z","type":"event_msg","payload":{"type":"error","codex_error_info":"RateLimitExceeded","message":"limit reached"}}`
			events, err = p.ParseLine([]byte(failure))
			if err != nil || len(events) != 2 || events[0].EventType != "turn_start" || events[1].EventType != "error" {
				t.Fatalf("error = %v, %v; want the turn start, then the error", events, err)
			}
			events = events[1:]
			if used == 100 {
				if events[0].Data.RetryAfterSeconds == nil || *events[0].Data.RetryAfterSeconds != 58 {
					t.Fatalf("quota update was not cached: %+v", events[0].Data)
				}
			} else if events[0].Data.RateLimitResetAt != nil || events[0].Data.RetryAfterSeconds != nil {
				t.Fatalf("available quota cached a reset: %+v", events[0].Data)
			}
		})
	}
}

func TestCodexNativeRateLimitMarkedWindows(t *testing.T) {
	const now = int64(1792709732000)
	tests := []struct {
		name    string
		windows string
		reset   int64
	}{
		{"exhausted primary before available weekly", `"primary":{"used_percent":100,"window_minutes":300,"resets_at":1792727732},"secondary":{"used_percent":20,"window_minutes":10080,"resets_at":1793314532}`, now + 5*60*60*1000},
		{"exhausted secondary before available primary", `"primary":{"used_percent":20,"resets_at":1793314532},"secondary":{"used_percent":101,"resets_at":1792727732}`, now + 5*60*60*1000},
		{"both exhausted use later reset", `"primary":{"used_percent":100,"resets_at":1792727732},"secondary":{"used_percent":100,"resets_at":1793314532}`, now + 7*24*60*60*1000},
		{"marker only uses later reset", `"primary":{"used_percent":99.5,"resets_at":1792727732},"secondary":{"used_percent":20,"resets_at":1793314532}`, now + 7*24*60*60*1000},
		{"marker only with absent primary", `"primary":null,"secondary":{"used_percent":99.5,"resets_at":1792727732}`, now + 5*60*60*1000},
		{"exhausted reset missing", `"primary":{"used_percent":100,"resets_at":null},"secondary":{"used_percent":20,"resets_at":1793314532}`, 0},
		{"exhausted reset expired", `"primary":{"used_percent":100,"resets_at":1792709732},"secondary":{"used_percent":20,"resets_at":1793314532}`, 0},
		{"exhausted reset invalid", `"primary":{"used_percent":100,"resets_at":-1},"secondary":{"used_percent":20,"resets_at":1793314532}`, 0},
		{"exhausted reset overflows", `"primary":{"used_percent":100,"resets_at":9223372036854775807},"secondary":{"used_percent":20,"resets_at":1793314532}`, 0},
		{"marker only without windows", `"primary":null,"secondary":null`, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limits := `{` + tt.windows + `,"rate_limit_reached_type":"rate_limit_reached"}`
			for _, source := range []string{"usage snapshot", "quota-only cache", "error snapshot"} {
				t.Run(source, func(t *testing.T) {
					p := NewCodexParser()
					payload := `"type":"token_count","info":{"total_token_usage":{"input_tokens":280,"cached_input_tokens":0,"output_tokens":20,"reasoning_output_tokens":0,"total_tokens":300},"last_token_usage":{"input_tokens":280,"cached_input_tokens":0,"output_tokens":20,"reasoning_output_tokens":0,"total_tokens":300}},"rate_limits":` + limits
					switch source {
					case "error snapshot":
						payload = `"type":"error","codex_error_info":"RateLimitExceeded","message":"limit reached","rate_limits":` + limits
					case "quota-only cache":
						payload = `"type":"token_count","info":null,"rate_limits":` + limits
					}
					line := `{"timestamp":"2026-10-22T22:55:32Z","type":"event_msg","payload":{` + payload + `}}`
					events, err := p.ParseLine([]byte(line))
					eventTime := now
					if source == "quota-only cache" {
						if err != nil || len(events) != 0 {
							t.Fatalf("quota-only snapshot = %v, %v; want no event", events, err)
						}
						events, err = p.ParseLine([]byte(`{"timestamp":"2026-10-22T22:55:34Z","type":"event_msg","payload":{"type":"error","codex_error_info":"RateLimitExceeded","message":"limit reached"}}`))
						eventTime += 2000
					}
					if err != nil || len(events) != 1 {
						t.Fatalf("ParseLine = %v, %v; want one event", events, err)
					}
					data := events[0].Data
					if tt.reset == 0 {
						if data.RateLimitResetAt != nil || data.RetryAfterSeconds != nil {
							t.Fatalf("unexpected reset metadata: %+v", data)
						}
						return
					}
					if data.RateLimitResetAt == nil || data.RetryAfterSeconds == nil {
						t.Fatalf("missing reset metadata: %+v", data)
					}
					if *data.RateLimitResetAt != tt.reset || *data.RetryAfterSeconds != (tt.reset-eventTime)/1000 {
						t.Fatalf("reset = %d, retry = %d; want %d, %d", *data.RateLimitResetAt, *data.RetryAfterSeconds, tt.reset, (tt.reset-eventTime)/1000)
					}
				})
			}
		})
	}
}
