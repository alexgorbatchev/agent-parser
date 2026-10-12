package parser

import (
	"fmt"
	"testing"
)

func TestCodexTaskTimestamps(t *testing.T) {
	for _, event := range []struct {
		kind, field, wantType string
	}{
		{"task_started", "started_at", "turn_start"},
		{"task_complete", "completed_at", "turn_end"},
	} {
		for _, tt := range []struct {
			name, value string
			want        int64
			wantErr     bool
		}{
			{"unix seconds", `1791747503`, 1791747503000, false},
			{"unix epoch", `0`, 0, false},
			{"RFC3339", `"2026-10-11T19:38:23Z"`, 1791747503000, false},
			{"RFC3339 milliseconds", `"2026-10-11T19:38:23.118Z"`, 1791747503118, false},
			{"null uses envelope", `null`, 1791747503999, false},
			{"empty uses envelope", `""`, 1791747503999, false},
			{"invalid boolean", `true`, 0, true},
			{"invalid object", `{}`, 0, true},
			{"invalid string", `"yesterday"`, 0, true},
		} {
			t.Run(event.kind+"/"+tt.name, func(t *testing.T) {
				line := fmt.Sprintf(`{"type":"event_msg","timestamp":"2026-10-11T19:38:23.999Z","payload":{"type":%q,%q:%s}}`, event.kind, event.field, tt.value)
				events, err := NewCodexParser().ParseLine([]byte(line))
				if (err != nil) != tt.wantErr {
					t.Fatalf("ParseLine error = %v, wantErr %v", err, tt.wantErr)
				}
				if tt.wantErr {
					return
				}
				if len(events) != 1 || events[0].EventType != event.wantType || events[0].Timestamp != tt.want {
					t.Fatalf("events = %+v, want %s at %d", events, event.wantType, tt.want)
				}
			})
		}
	}
}
