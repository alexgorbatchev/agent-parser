package parser

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// ClaudeCodeActivityTimestamp returns a conversational record's envelope time in
// Unix milliseconds. User, assistant, attachment, system, and progress records
// count as activity; session metadata, queue operations, and prompt pointers do not.
// A missing timestamp or nonactivity record returns found=false. Malformed JSON or
// an invalid timestamp on an activity record returns an error. No clock or file
// modification time is substituted. This is not a complete schema validator.
func ClaudeCodeActivityTimestamp(line []byte) (timestamp int64, found bool, err error) {
	if len(bytes.TrimSpace(line)) == 0 {
		return 0, false, nil
	}
	var raw rawJSONLine
	if err := json.Unmarshal(line, &raw); err != nil {
		return 0, false, fmt.Errorf("decoding Claude Code activity: %w", err)
	}
	switch raw.Type {
	case "user", "assistant", "attachment", "system", "progress":
		return activityEnvelopeTimestamp(raw.Timestamp)
	default:
		return 0, false, nil
	}
}

// PiActivityTimestamp returns the Unix-millisecond envelope time of a message,
// custom message, compaction, or branch summary. Header, settings, labels, and
// extension state records do not count as conversational activity. The session
// entry timestamp is authoritative, not the nested message's creation timestamp.
// Missing times and nonactivity records return found=false; malformed JSON and
// invalid activity timestamps return errors. It never substitutes the current time.
func PiActivityTimestamp(line []byte) (timestamp int64, found bool, err error) {
	if len(bytes.TrimSpace(line)) == 0 {
		return 0, false, nil
	}
	var raw rawPiLine
	if err := json.Unmarshal(line, &raw); err != nil {
		return 0, false, fmt.Errorf("decoding Pi activity: %w", err)
	}
	switch raw.Type {
	case "message", "custom_message", "compaction", "branch_summary":
		return activityEnvelopeTimestamp(raw.Timestamp)
	default:
		return 0, false, nil
	}
}

// CodexActivityTimestamp returns the Unix-millisecond envelope time of a known
// event message, response item, or turn context. Session metadata and unknown raw
// types are ignored. Task payload started_at/completed_at describe lifecycle times,
// not when the rollout record was written, so they do not override the envelope.
// Missing times and nonactivity records return found=false; malformed JSON and
// invalid activity timestamps return errors. No numeric unit or timezone is guessed.
func CodexActivityTimestamp(line []byte) (timestamp int64, found bool, err error) {
	if len(bytes.TrimSpace(line)) == 0 {
		return 0, false, nil
	}
	var raw rawCodexLine
	if err := json.Unmarshal(line, &raw); err != nil {
		return 0, false, fmt.Errorf("decoding Codex activity: %w", err)
	}
	switch raw.Type {
	case "turn_context":
		return activityEnvelopeTimestamp(raw.Timestamp)
	case "event_msg":
		var event rawCodexEventMsgPayload
		if err := json.Unmarshal(raw.Payload, &event); err != nil {
			return 0, false, fmt.Errorf("decoding Codex activity event: %w", err)
		}
		switch event.Type {
		case "user_message", "agent_message", "reasoning", "token_count", "task_started", "task_complete", "error", "item_completed":
			return activityEnvelopeTimestamp(raw.Timestamp)
		}
	case "response_item":
		var item rawCodexResponseItemPayload
		if err := json.Unmarshal(raw.Payload, &item); err != nil {
			return 0, false, fmt.Errorf("decoding Codex activity response: %w", err)
		}
		switch item.Type {
		case "message", "function_call", "function_call_output", "custom_tool_call", "custom_tool_call_output", "reasoning", "tool_search_call", "tool_search_output":
			return activityEnvelopeTimestamp(raw.Timestamp)
		}
	}
	return 0, false, nil
}

// All three JSONL envelope schemas require a zoned timestamp string. Keep this
// strict reader separate from event parsing's historical current-time default.
func activityEnvelopeTimestamp(value interface{}) (int64, bool, error) {
	if value == nil || value == "" {
		return 0, false, nil
	}
	text, ok := value.(string)
	if !ok {
		return 0, false, fmt.Errorf("activity timestamp must be an RFC3339 string, got %T", value)
	}
	stamp, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return 0, false, fmt.Errorf("parsing activity timestamp: %w", err)
	}
	return stamp.UnixMilli(), true, nil
}
