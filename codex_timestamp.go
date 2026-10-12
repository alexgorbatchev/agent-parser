package parser

import (
	"bytes"
	"encoding/json"
	"time"
)

func parseCodexTaskTimestamp(raw json.RawMessage, fallback int64) (int64, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte(`""`)) {
		return fallback, nil
	}
	if raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return 0, err
		}
		t, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			return 0, err
		}
		return t.UnixMilli(), nil
	}
	// Task lifecycle fields use Unix seconds; envelope timestamps use RFC3339.
	var seconds int64
	if err := json.Unmarshal(raw, &seconds); err != nil {
		return 0, err
	}
	return time.Unix(seconds, 0).UnixMilli(), nil
}
