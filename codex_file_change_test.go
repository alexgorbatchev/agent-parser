package parser

import (
	"fmt"
	"strings"
	"testing"
)

func TestCodexFileChanges(t *testing.T) {
	changes := `{` +
		`"/work/new.go":{"type":"add","content":"package work\n"},` +
		`"/work/old.go":{"type":"delete","content":"package old\n"},` +
		`"/work/calc.go":{"type":"update","unified_diff":"@@ -1,2 +1,2 @@\n package work\n-const X = 1\n+const X = 2\n","move_path":null},` +
		`"/work/a.go":{"type":"update","unified_diff":"@@ -3 +3 @@\n-old\n+new\n","move_path":"/work/b.go"}` +
		`}`
	want := []string{
		"EDIT /work/b.go (from /work/a.go) hunks=1",
		"EDIT /work/calc.go hunks=1",
		"WRITE /work/new.go package work\n",
		"DELETE /work/old.go package old\n",
	}
	tests := []struct {
		name, line string
		want       []string
	}{
		{"completed item", `{"timestamp":"2026-11-07T10:00:00Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"FileChange","id":"call_patch","changes":` + changes + `,"status":"completed","stdout":"Success."}}}`, want},
		{"failed item", `{"timestamp":"2026-11-07T10:00:00Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"FileChange","id":"call_patch","changes":` + changes + `,"status":"failed","stderr":"no match"}}}`, nil},
		{"declined item", `{"timestamp":"2026-11-07T10:00:00Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"FileChange","id":"call_patch","changes":` + changes + `,"status":"declined"}}}`, nil},
		{"item without a status", `{"timestamp":"2026-11-07T10:00:00Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"FileChange","id":"call_patch","changes":` + changes + `}}}`, want},
		{"legacy patch_apply_end", `{"timestamp":"2026-11-07T10:00:00Z","type":"event_msg","payload":{"type":"patch_apply_end","call_id":"call_patch","turn_id":"t1","stdout":"Success.","stderr":"","success":true,"changes":` + changes + `,"status":"completed"}}`, want},
		{"legacy failed patch", `{"timestamp":"2026-11-07T10:00:00Z","type":"event_msg","payload":{"type":"patch_apply_end","call_id":"call_patch","stdout":"","stderr":"bad","success":false,"changes":` + changes + `}}`, nil},
		{"unknown change type", `{"timestamp":"2026-11-07T10:00:00Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"FileChange","id":"call_patch","changes":{"/work/x":{"type":"rename"}},"status":"completed"}}}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evs, err := ParseCodexLine([]byte(tt.line))
			if err != nil {
				t.Fatalf("ParseCodexLine: %v", err)
			}
			var got []string
			for _, ev := range evs {
				if ev.EventType != "file_modification" || ev.Data.ToolCallID == nil || *ev.Data.ToolCallID != "call_patch" {
					t.Fatalf("event = %+v; want file_modification for call_patch", ev)
				}
				got = append(got, describeFileModification(ev.Data.FileModification))
			}
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("modifications = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCodexFileChangeMalformed(t *testing.T) {
	line := `{"timestamp":"2026-11-07T10:00:00Z","type":"event_msg","payload":{"type":"patch_apply_end","changes":"not a map"}}`
	if _, err := ParseCodexLine([]byte(line)); err == nil {
		t.Error("expected an error for malformed changes")
	}
}

func TestCodexApplyPatchCallCarriesNoModification(t *testing.T) {
	// Codex writes apply_patch input in its own patch grammar; the applied
	// result arrives as a FileChange item or patch_apply_end.
	line := `{"timestamp":"2026-11-07T10:00:00Z","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","call_id":"call_patch","input":"*** Begin Patch\n*** Update File: calc.go\n@@\n-const X = 1\n+const X = 2\n*** End Patch\n"}}`
	evs, err := ParseCodexLine([]byte(line))
	if err != nil || len(evs) != 1 || evs[0].EventType != "tool_call" || evs[0].Data.FileModification != nil {
		t.Errorf("events = %+v, %v; want one tool_call without a FileModification", evs, err)
	}
}

func describeFileModification(mod *FileModification) string {
	desc := mod.Action + " " + mod.FilePath
	if mod.PreviousPath != "" {
		desc += " (from " + mod.PreviousPath + ")"
	}
	if mod.Content != nil {
		return desc + " " + *mod.Content
	}
	return desc + fmt.Sprintf(" hunks=%d", len(mod.Hunks))
}
