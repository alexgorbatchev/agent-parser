package parser

import (
	"encoding/json"
	"fmt"
	"sort"
)

// rawCodexFileChange is a patch application: a completed FileChange item in
// paginated history (protocol/src/items.rs FileChangeItem) or a
// patch_apply_end event in legacy history (protocol.rs PatchApplyEndEvent).
// Changes maps each path to Codex's FileChange.
type rawCodexFileChange struct {
	ID      string                            `json:"id"`
	CallID  string                            `json:"call_id"`
	Status  string                            `json:"status"`
	Success *bool                             `json:"success"`
	Changes map[string]rawCodexFileChangeKind `json:"changes"`
}

type rawCodexFileChangeKind struct {
	Type        string  `json:"type"` // "add" | "delete" | "update"
	Content     string  `json:"content"`
	UnifiedDiff string  `json:"unified_diff"`
	MovePath    *string `json:"move_path"`
}

// parseCodexFileChange emits one file_modification event per file a patch
// changed, correlated with the patch's tool call by ToolCallID. Patches that
// failed or were declined changed nothing and emit no events.
func parseCodexFileChange(raw json.RawMessage, ts int64) ([]ParsedEvent, error) {
	var change rawCodexFileChange
	if err := json.Unmarshal(raw, &change); err != nil {
		return nil, fmt.Errorf("unmarshaling codex file change: %w", err)
	}
	if !codexPatchApplied(change) {
		return nil, nil
	}
	callID := change.CallID
	if callID == "" {
		callID = change.ID
	}

	paths := make([]string, 0, len(change.Changes))
	for path := range change.Changes {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	var events []ParsedEvent
	for _, path := range paths {
		mod := codexFileModification(path, change.Changes[path])
		if mod == nil {
			continue
		}
		events = append(events, ParsedEvent{
			EventType: "file_modification",
			Timestamp: ts,
			Data: EventData{
				Role:             stringPtr("assistant"),
				ToolName:         stringPtr("apply_patch"),
				ToolCallID:       optionalString(callID),
				FileModification: mod,
			},
		})
	}
	return events, nil
}

// codexPatchApplied reports whether a patch application completed. Items
// written before Codex recorded a status count as applied.
func codexPatchApplied(change rawCodexFileChange) bool {
	switch {
	case change.Status != "":
		return change.Status == "completed"
	case change.Success != nil:
		return *change.Success
	default:
		return true
	}
}

func codexFileModification(path string, kind rawCodexFileChangeKind) *FileModification {
	switch kind.Type {
	case "add":
		content := kind.Content
		return &FileModification{Action: "WRITE", FilePath: path, Content: &content}
	case "delete":
		content := kind.Content
		return &FileModification{Action: "DELETE", FilePath: path, Content: &content}
	case "update":
		mod := parseUnifiedPatch(kind.UnifiedDiff, path)
		if mod == nil {
			mod = &FileModification{Action: "EDIT", FilePath: path}
		}
		mod.FilePath = path
		if kind.MovePath != nil && *kind.MovePath != "" {
			mod.FilePath = *kind.MovePath
			mod.PreviousPath = path
		}
		return mod
	}
	return nil
}
