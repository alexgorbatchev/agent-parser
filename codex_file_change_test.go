package parser

import (
	"encoding/json"
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

func TestCodexFileChangeKeepsHeaderLikeHunkLines(t *testing.T) {
	// Codex's unified_diff has hunks only. A deleted "-- comment" line reads
	// "--- comment" and an added "++ b/x" line reads "+++ b/x"; both are hunk
	// content, not file headers.
	diff := "@@ -1,3 +1,3 @@\n select 1;\n--- drop legacy index\n+++ b/keep this line\n select 2;\n@@ -10 +10 @@\n-a\n+b\n"
	line := `{"timestamp":"2026-11-07T10:00:00Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"FileChange","id":"call_patch","changes":{"/work/q.sql":{"type":"update","unified_diff":` + jsonString(t, diff) + `,"move_path":null}},"status":"completed"}}}`
	evs, err := ParseCodexLine([]byte(line))
	if err != nil || len(evs) != 1 {
		t.Fatalf("ParseCodexLine = %+v, %v", evs, err)
	}
	mod := evs[0].Data.FileModification
	if mod.FilePath != "/work/q.sql" || len(mod.Hunks) != 2 {
		t.Fatalf("modification = %+v; want two hunks on /work/q.sql", mod)
	}
	var got []string
	for _, l := range mod.Hunks[0].Lines {
		got = append(got, fmt.Sprintf("%s old=%s new=%s %q", l.Type, lineNumber(l.OldLineNumber), lineNumber(l.NewLineNumber), l.Content))
	}
	want := []string{
		`ctx old=1 new=1 "select 1;"`,
		`del old=2 new=- "-- drop legacy index"`,
		`add old=- new=2 "++ b/keep this line"`,
		`ctx old=3 new=3 "select 2;"`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("hunk lines:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if l := mod.Hunks[1].Lines; len(l) != 2 || *l[0].OldLineNumber != 10 || *l[1].NewLineNumber != 10 {
		t.Errorf("second hunk = %+v; want the del and add at line 10", l)
	}
}

func lineNumber(n *int) string {
	if n == nil {
		return "-"
	}
	return fmt.Sprint(*n)
}

func jsonString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
