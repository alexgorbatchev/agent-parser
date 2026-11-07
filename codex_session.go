package parser

import (
	"encoding/json"
	"fmt"
)

// parseCodexSessionMeta turns a session_meta record into run_started.
func parseCodexSessionMeta(payload json.RawMessage, ts int64, p *CodexParser) ([]ParsedEvent, error) {
	var meta rawCodexSessionMetaPayload
	if err := json.Unmarshal(payload, &meta); err != nil {
		return nil, fmt.Errorf("unmarshaling session_meta payload: %w", err)
	}

	p.recordHistoryStart(meta.SubagentHistoryStartOrdinal, meta.HistoryMode == "paginated")

	if meta.Timestamp != "" {
		ts = parseTimestamp(meta.Timestamp)
	}

	var gitBranch string
	if meta.Git != nil {
		gitBranch = meta.Git.Branch
	}

	parentThreadID, nickname, role := codexSubagentIdentity(meta)

	sessionID := meta.ID
	if sessionID == "" {
		sessionID = meta.SessionID
	}

	return []ParsedEvent{{
		EventType: "run_started",
		Timestamp: ts,
		CWD:       meta.CWD,
		GitBranch: gitBranch,
		Data: EventData{
			// SessionMeta names no model; turn_context and
			// thread_settings_applied report it.
			Title: stringPtr(codexSessionTitle(sessionID, parentThreadID, nickname, role)),
		},
	}}, nil
}

// codexSubagentIdentity resolves the parent thread, nickname and role from the
// top-level fields, overridden by a thread_spawn subagent source.
func codexSubagentIdentity(meta rawCodexSessionMetaPayload) (parentThreadID, nickname, role string) {
	parentThreadID = meta.ParentThreadID
	nickname = meta.AgentNickname
	role = meta.AgentRole

	sm, ok := meta.Source.(map[string]interface{})
	if !ok {
		return parentThreadID, nickname, role
	}
	subVal, exists := sm["subagent"]
	if !exists || subVal == nil {
		return parentThreadID, nickname, role
	}
	if subStr, ok := subVal.(string); ok {
		if subStr != "" && role == "" {
			role = subStr
		}
		return parentThreadID, nickname, role
	}
	subMap, ok := subVal.(map[string]interface{})
	if !ok {
		return parentThreadID, nickname, role
	}
	spawnVal, ok := subMap["thread_spawn"]
	if !ok || spawnVal == nil {
		return parentThreadID, nickname, role
	}
	var spawn rawCodexThreadSpawn
	data, err := json.Marshal(spawnVal)
	if err != nil || json.Unmarshal(data, &spawn) != nil {
		return parentThreadID, nickname, role
	}
	if spawn.ParentThreadID != "" {
		parentThreadID = spawn.ParentThreadID
	}
	if spawn.AgentNickname != "" {
		nickname = spawn.AgentNickname
	}
	if spawn.AgentRole != "" {
		role = spawn.AgentRole
	}
	return parentThreadID, nickname, role
}

func codexSessionTitle(sessionID, parentThreadID, nickname, role string) string {
	isSubagent := parentThreadID != "" || nickname != "" || role != ""
	switch {
	case isSubagent && nickname != "" && role != "":
		return fmt.Sprintf("Codex Subagent: %s (%s)", nickname, role)
	case isSubagent && nickname != "":
		return fmt.Sprintf("Codex Subagent: %s", nickname)
	case isSubagent && role != "":
		return fmt.Sprintf("Codex Subagent: (%s)", role)
	case isSubagent:
		return fmt.Sprintf("Codex Subagent %s", sessionID)
	case sessionID != "":
		return fmt.Sprintf("Codex Session %s", sessionID)
	default:
		return "Codex Session"
	}
}

// parseCodexTurnContext turns a turn_context record that names a model into
// model_change.
func parseCodexTurnContext(payload json.RawMessage, ts int64, p *CodexParser) ([]ParsedEvent, error) {
	var ctx rawCodexTurnContextPayload
	if err := json.Unmarshal(payload, &ctx); err != nil {
		return nil, fmt.Errorf("unmarshaling turn_context payload: %w", err)
	}
	if ctx.Model == "" {
		return nil, nil
	}
	p.setModel(ctx.Model)
	return []ParsedEvent{{
		EventType: "model_change",
		Timestamp: ts,
		CWD:       ctx.CWD,
		Data: EventData{
			Model: stringPtr(ctx.Model),
		},
	}}, nil
}

// rawCodexThreadSettings is the thread_settings of a thread_settings_applied
// event (ThreadSettingsSnapshot).
type rawCodexThreadSettings struct {
	Model string `json:"model"`
	CWD   string `json:"cwd"`
}

// codexThreadSettingsEvents turns the model a thread_settings_applied event
// names into model_change.
func codexThreadSettingsEvents(settings *rawCodexThreadSettings, ts int64, p *CodexParser) []ParsedEvent {
	if settings == nil || settings.Model == "" {
		return nil
	}
	p.setModel(settings.Model)
	return []ParsedEvent{{
		EventType: "model_change",
		Timestamp: ts,
		CWD:       settings.CWD,
		Data:      EventData{Model: stringPtr(settings.Model)},
	}}
}
