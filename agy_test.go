package parser

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLiveAgyStream(t *testing.T) {
	lines := []string{
		`{"event":"init","conversation_id":"conv-1","init":{"cwd":"/repo","model":"gemini-4-neon-high","tools":["read_file","run_command"],"permission_mode":"request-review"}}`,
		`{"event":"step_update","step_update":{"conversation_id":"conv-1","step_index":0,"state":"DONE","step_type":"user_input"}}`,
		`{"event":"step_update","step_update":{"conversation_id":"conv-1","step_index":1,"state":"ACTIVE","step_type":"tool","tool_name":"run_command","tool_info":{"name":"run_command","parameters":{"CommandLine":"echo hi"}}}}`,
		`{"event":"step_update","step_update":{"conversation_id":"conv-1","step_index":1,"state":"ACTIVE","step_type":"tool","tool_name":"run_command"}}`,
		`{"event":"step_update","step_update":{"conversation_id":"conv-1","step_index":1,"state":"DONE","step_type":"tool","tool_name":"run_command","tool_info":{"name":"run_command","output":"hi\n"}}}`,
		`{"event":"step_update","step_update":{"conversation_id":"conv-1","step_index":2,"state":"DONE","step_type":"tool","tool_info":{"name":"write_file","parameters":{"Path":"/repo/a.txt","Content":"hello"},"error":{"type":"denied","message":"permission denied"}}}}`,
		`{"event":"step_update","step_update":{"conversation_id":"conv-1","step_index":3,"state":"DONE","step_type":"tool","subagent_info":{"subagents":[{"role":"research"}]}}}`,
		`{"event":"step_update","step_update":{"conversation_id":"conv-1","step_index":4,"state":"ACTIVE","step_type":"agent_response","thinking_delta":"plan","text_delta":"Hel"}}`,
		`{"event":"step_update","step_update":{"conversation_id":"conv-1","step_index":4,"state":"DONE","step_type":"agent_response","text_delta":"lo"}}`,
		`{"event":"step_update","step_update":{"conversation_id":"conv-1","step_index":5,"state":"DONE","step_type":"checkpoint"}}`,
		`{"event":"result","result":{"conversation_id":"conv-1","status":"SUCCESS","response":"Hello","structured_output":{"ok":true},"usage":{"input_tokens":100,"output_tokens":20,"thinking_tokens":8,"cache_read_tokens":40,"total_tokens":160}}}`,
	}

	for _, harness := range []string{"agy", "antigravity"} {
		t.Run(harness, func(t *testing.T) {
			p, err := NewLiveParser(harness)
			if err != nil {
				t.Fatal(err)
			}
			var gotTypes []string
			for _, line := range lines {
				events, err := p.ParseLine([]byte(line))
				if err != nil {
					t.Fatalf("ParseLine(%s): %v", line, err)
				}
				for _, ev := range events {
					gotTypes = append(gotTypes, ev.EventType)
					if ev.EventType == "tool_call" && ev.Data.ToolName != nil && *ev.Data.ToolName == "write_file" {
						if ev.Data.FileModification == nil || ev.Data.FileModification.Action != "WRITE" || ev.Data.FileModification.FilePath != "/repo/a.txt" {
							t.Fatalf("unexpected FileModification: %+v", ev.Data.FileModification)
						}
					}
					if ev.EventType == "tool_result" && ev.Data.ToolCallID != nil && *ev.Data.ToolCallID == "step-2" {
						if ev.Data.IsError == nil || !*ev.Data.IsError || ev.Data.ToolOutput == nil || *ev.Data.ToolOutput != "permission denied" {
							t.Fatalf("unexpected error tool_result: %+v", ev.Data)
						}
					}
				}
			}
			wantTypes := []string{
				"run_started", "turn_start",
				"tool_call", "tool_update", "tool_result",
				"tool_call", "tool_result",
				"tool_call", "tool_result",
				"thinking_delta", "agent_message_delta", "agent_message_delta",
				"run_result",
			}
			if len(gotTypes) != len(wantTypes) {
				t.Fatalf("event types = %v, want %v", gotTypes, wantTypes)
			}
			for i := range wantTypes {
				if gotTypes[i] != wantTypes[i] {
					t.Fatalf("event[%d] = %s, want %s (all: %v)", i, gotTypes[i], wantTypes[i], gotTypes)
				}
			}
			res, done := p.Result()
			if !done || res.IsError || !res.HasText || res.Text != "Hello" || res.Model != "gemini-4-neon-high" || res.SessionID != "conv-1" || string(res.StructuredOutput) != `{"ok":true}` {
				t.Fatalf("unexpected LiveResult: %+v done=%v", res, done)
			}
			if res.Usage == nil || res.Usage.Input != 100 || res.Usage.Output != 20 || res.Usage.Reasoning != 8 || res.Usage.CacheRead != 40 || res.Usage.Total != 160 {
				t.Fatalf("unexpected Usage: %+v", res.Usage)
			}
		})
	}
}

func TestLiveAgyErrorsAndEdgeCases(t *testing.T) {
	p, err := NewLiveParser("agy")
	if err != nil {
		t.Fatal(err)
	}
	if evs, err := p.ParseLine([]byte(`{"event":"result","result":{"status":"ERROR","response":"","error":"bad model","usage":{"input_tokens":10,"output_tokens":5,"cache_read_tokens":2}}}`)); err != nil {
		t.Fatal(err)
	} else if len(evs) != 1 || evs[0].EventType != "run_result" {
		t.Fatalf("unexpected events: %+v", evs)
	}
	res, done := p.Result()
	if !done || !res.IsError || res.HasText || res.ErrorMessage != "bad model" || res.NativeSubtype != "ERROR" {
		t.Fatalf("unexpected error LiveResult: %+v", res)
	}
	if res.Usage == nil || res.Usage.Total != 17 {
		t.Fatalf("unexpected fallback total usage: %+v", res.Usage)
	}

	cases := []struct {
		line    string
		wantErr bool
	}{
		{"", false},
		{"not json", true},
		{"{}", true},
		{`{"event":"future"}`, false},
		{`{"event":"init","init":42}`, true},
		{`{"event":"step_update"}`, false},
		{`{"event":"step_update","step_update":42}`, true},
		{`{"event":"result"}`, true},
		{`{"event":"result","result":42}`, true},
	}
	for _, tt := range cases {
		_, err := p.ParseLine([]byte(tt.line))
		if (err != nil) != tt.wantErr {
			t.Fatalf("ParseLine(%q) err=%v wantErr=%v", tt.line, err, tt.wantErr)
		}
	}
}

func TestAgyTranscriptParser(t *testing.T) {
	p := NewAgyParser()
	lines := []string{
		`{"step_index":0,"type":"USER_INPUT","source":"USER_EXPLICIT","status":"DONE","created_at":"2026-11-22T14:38:26Z","content":"Fix the bug"}`,
		`{"step_index":1,"type":"PLANNER_RESPONSE","source":"MODEL","status":"DONE","created_at":"2026-11-22T14:38:27Z","model":"gemini-4-neon-high","thinking":"Inspect skill and edit file","input_tokens":500,"output_tokens":50,"cache_read_tokens":200,"thinking_tokens":20,"tool_calls":[{"name":"read_file","args":{"Path":"/home/u/.gemini/config/skills/programming/SKILL.md"}},{"name":"read_file","args":{"Path":"/home/u/.gemini/config/skills/skipped/SKILL.md","StartLine":50}},{"name":"run_command","args":{"CommandLine":"cat /home/u/.gemini/config/skills/git-commit/SKILL.md"}},{"name":"edit_file","args":{"Path":"/repo/main.go","Target":"old","Replacement":"new"}},{"name":"multi_replace_file_content","args":{"TargetFile":"/repo/app.go","ReplacementChunks":"[{\"TargetContent\":\"a\",\"ReplacementContent\":\"b\"},{\"TargetContent\":\"c\",\"ReplacementContent\":\"d\"}]"}}]}`,
		`{"step_index":2,"type":"GENERIC","source":"MODEL","status":"DONE","created_at":"2026-11-22T14:38:28Z","content":"skill contents"}`,
		`{"step_index":3,"type":"GENERIC","source":"MODEL","status":"ERROR","created_at":"2026-11-22T14:38:29Z","error":"file does not exist"}`,
		`{"step_index":4,"type":"SYSTEM_MESSAGE","source":"SYSTEM","status":"DONE","created_at":"2026-11-22T14:38:30Z","content":"notice"}`,
		`{"step_index":5,"type":"SYSTEM_MESSAGE","source":"SYSTEM","status":"ERROR","created_at":"2026-11-22T14:38:30Z","error":"quota exceeded"}`,
		`{"step_index":6,"type":"PLANNER_RESPONSE","source":"MODEL","status":"DONE","created_at":"2026-11-22T14:38:31Z","content":"Done fixing.","error":"warning"}`,
		`{"step_index":7,"type":"UNKNOWN_TYPE","created_at":"2026-11-22T14:38:32Z"}`,
	}

	var all []ParsedEvent
	for _, line := range lines {
		evs, err := p.ParseLine([]byte(line))
		if err != nil {
			t.Fatalf("ParseLine(%s): %v", line, err)
		}
		all = append(all, evs...)
	}

	var skills []string
	var fileMods []*FileModification
	var toolResults []ParsedEvent
	var hasTurnStart, hasTurnEnd, hasThinking, hasUsage, hasAgentMsg bool
	var errorCount int
	for _, ev := range all {
		switch ev.EventType {
		case "turn_start":
			hasTurnStart = true
		case "turn_end":
			hasTurnEnd = true
		case "thinking":
			hasThinking = true
		case "agent_message":
			hasAgentMsg = true
		case "usage":
			hasUsage = true
			if ev.Data.Usage.Input != 500 || ev.Data.Usage.Output != 50 || ev.Data.Usage.CacheRead != 200 || ev.Data.Usage.Reasoning != 20 || ev.Data.Usage.Total != 750 {
				t.Fatalf("unexpected usage: %+v", ev.Data.Usage)
			}
		case "tool_call":
			for _, sk := range ev.Data.Skills {
				skills = append(skills, sk.Name)
			}
			if ev.Data.FileModification != nil {
				fileMods = append(fileMods, ev.Data.FileModification)
			}
		case "tool_result":
			toolResults = append(toolResults, ev)
		case "error":
			errorCount++
		}
	}
	if !hasTurnStart || !hasTurnEnd || !hasThinking || !hasUsage || !hasAgentMsg || errorCount != 2 {
		t.Fatalf("missing expected events: start=%v end=%v think=%v usage=%v msg=%v errs=%d", hasTurnStart, hasTurnEnd, hasThinking, hasUsage, hasAgentMsg, errorCount)
	}
	if len(skills) != 2 || skills[0] != "programming" || skills[1] != "git-commit" {
		t.Fatalf("skills = %v, want [programming git-commit]", skills)
	}
	if len(fileMods) != 2 || fileMods[0].FilePath != "/repo/main.go" || len(fileMods[1].Hunks) != 2 {
		t.Fatalf("unexpected fileMods: %+v", fileMods)
	}
	if len(toolResults) != 2 || *toolResults[0].Data.ToolName != "read_file" || *toolResults[0].Data.IsError || !*toolResults[1].Data.IsError || *toolResults[1].Data.ToolOutput != "file does not exist" {
		t.Fatalf("unexpected toolResults: %+v", toolResults)
	}

	// Stateless helper, orphan GENERIC, Reset, blank and invalid lines.
	p.Reset()
	if evs, err := ParseAgyLine([]byte(`{"step_index":9,"type":"GENERIC","status":"DONE","content":"orphan"}`)); err != nil || len(evs) != 1 || *evs[0].Data.ToolCallID != "step-9" {
		t.Fatalf("unexpected stateless orphan result: %+v err=%v", evs, err)
	}
	if evs, err := p.ParseLine([]byte("   ")); err != nil || len(evs) != 0 {
		t.Fatalf("blank line: %+v %v", evs, err)
	}
	if _, err := p.ParseLine([]byte("{bad")); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestAgyActivityTimestamp(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		wantTS  int64
		wantOK  bool
		wantErr bool
	}{
		{"blank", "  ", 0, false, false},
		{"malformed", "{bad", 0, false, true},
		{"user_input", `{"type":"USER_INPUT","created_at":"2026-11-22T14:38:26Z"}`, 1795358306000, true, false},
		{"planner", `{"type":"PLANNER_RESPONSE","created_at":"2026-11-22T14:38:26.500Z"}`, 1795358306500, true, false},
		{"generic", `{"type":"GENERIC","created_at":"2026-11-22T14:38:26Z"}`, 1795358306000, true, false},
		{"system_ignored", `{"type":"SYSTEM_MESSAGE","created_at":"2026-11-22T14:38:26Z"}`, 0, false, false},
		{"missing_ts", `{"type":"USER_INPUT"}`, 0, false, false},
		{"bad_ts", `{"type":"USER_INPUT","created_at":"not-a-time"}`, 0, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, ok, err := AgyActivityTimestamp([]byte(tt.line))
			if (err != nil) != tt.wantErr || ok != tt.wantOK || ts != tt.wantTS {
				t.Fatalf("AgyActivityTimestamp() = (%d, %v, %v), want (%d, %v, err=%v)", ts, ok, err, tt.wantTS, tt.wantOK, tt.wantErr)
			}
		})
	}
}

func TestDiscoverAgyAndMetadata(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AGY_SESSIONS_DIR", "")
	t.Setenv("GEMINI_HOME", "")

	appDir := filepath.Join(home, ".gemini", "antigravity-cli")
	parentID := "11111111-1111-4111-8111-111111111111"
	childID := "22222222-2222-4222-8222-222222222222"
	dbChildID := "33333333-3333-4333-8333-333333333333"

	// Parent has both transcript_full.jsonl and transcript.jsonl; discovery prefers transcript_full.jsonl.
	parentLogs := filepath.Join(appDir, "brain", parentID, ".system_generated", "logs")
	if err := os.MkdirAll(parentLogs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parentLogs, "transcript_full.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parentLogs, "transcript.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Detached subagent metadata links childID -> parentID.
	subDir := filepath.Join(appDir, "brain", parentID, ".system_generated", "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, childID+".json"), []byte(`{"conversationId":"`+childID+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Child has only transcript.jsonl.
	childLogs := filepath.Join(appDir, "brain", childID, ".system_generated", "logs")
	if err := os.MkdirAll(childLogs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childLogs, "transcript.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// dbChildID parent is recorded via conversation_summaries.db.
	dbChildLogs := filepath.Join(appDir, "brain", dbChildID, ".system_generated", "logs")
	if err := os.MkdirAll(dbChildLogs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dbChildLogs, "transcript_full.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Create conversation_summaries.db.
	summariesDB := filepath.Join(appDir, "conversation_summaries.db")
	db, err := sql.Open("sqlite", summariesDB)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE conversation_summaries (
			conversation_id TEXT PRIMARY KEY,
			title TEXT NOT NULL DEFAULT '',
			workspace_uris TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			parent_conversation_id TEXT NOT NULL DEFAULT '',
			last_modified_time DATETIME NOT NULL DEFAULT '',
			not_fully_idle NUMERIC NOT NULL DEFAULT 0,
			killed NUMERIC NOT NULL DEFAULT 0
		);
		CREATE TABLE conversation_parents (
			conversation_id TEXT PRIMARY KEY,
			parent_id TEXT NOT NULL
		);
		INSERT INTO conversation_summaries VALUES ('` + parentID + `', 'Main Task', '["file:///repo/main"]', 'IDLE', '', '2026-11-22T14:38:26Z', 1, 0);
		INSERT INTO conversation_summaries VALUES ('` + dbChildID + `', 'Worker', 'file:///repo/sub', 'RUNNING', '', '2026-11-22 14:39:00', 'true', 'false');
		INSERT INTO conversation_parents VALUES ('` + dbChildID + `', '` + parentID + `');
	`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	sources, err := DiscoverSessions(context.Background(), DiscoverOptions{
		Harnesses: []Harness{HarnessAgy},
		Home:      home,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 3 {
		t.Fatalf("got %d sources, want 3: %+v", len(sources), sources)
	}
	byID := map[string]SessionSource{}
	for _, s := range sources {
		byID[s.SessionID] = s
	}
	if filepath.Base(byID[parentID].Path) != "transcript_full.jsonl" || byID[parentID].Subagent {
		t.Fatalf("parent source unexpected: %+v", byID[parentID])
	}
	if filepath.Base(byID[childID].Path) != "transcript.jsonl" || !byID[childID].Subagent || byID[childID].ParentSessionID != parentID {
		t.Fatalf("child source unexpected: %+v", byID[childID])
	}
	if !byID[dbChildID].Subagent || byID[dbChildID].ParentSessionID != parentID {
		t.Fatalf("dbChild source unexpected: %+v", byID[dbChildID])
	}

	summaries, err := ReadAgySummaries(context.Background(), summariesDB)
	if err != nil {
		t.Fatal(err)
	}
	if s := summaries[parentID]; s.Title != "Main Task" || !s.NotFullyIdle || len(s.WorkspacePaths) != 1 || s.WorkspacePaths[0] != "/repo/main" {
		t.Fatalf("unexpected parent summary: %+v", s)
	}
	if s := summaries[dbChildID]; s.ParentConversationID != parentID || !s.NotFullyIdle || s.Killed || len(s.WorkspacePaths) != 1 || s.WorkspacePaths[0] != "/repo/sub" {
		t.Fatalf("unexpected dbChild summary: %+v", s)
	}

	// Test ReadAgyConversationWorkspaces with both .meta sidecar and SQLite protobuf blob.
	convDir := filepath.Join(appDir, "conversations")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	metaBytes, _ := json.Marshal(map[string]string{"cwd": "/legacy/dir"})
	if err := os.WriteFile(filepath.Join(convDir, "legacy.meta"), metaBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	paths, err := ReadAgyConversationWorkspaces(context.Background(), convDir, "legacy")
	if err != nil || len(paths) != 1 || paths[0] != "/legacy/dir" {
		t.Fatalf("legacy meta paths = %v, err = %v", paths, err)
	}

	// Build TrajectoryMetadata protobuf: field 1 -> nested field 1 ("file:///proto/ws1"),
	// field 7 ("file:///proto/ws2"), plus varint/fixed fields skipped cleanly.
	protoBlob := append([]byte{}, encodeProtoBytes(1, encodeProtoBytes(1, []byte("file:///proto/ws1")))...)
	protoBlob = append(protoBlob, 0x10, 0x05)                                          // field 2 varint 5
	protoBlob = append(protoBlob, 0x19, 1, 2, 3, 4, 5, 6, 7, 8)                        // field 3 fixed64
	protoBlob = append(protoBlob, 0x25, 1, 2, 3, 4)                                    // field 4 fixed32
	protoBlob = append(protoBlob, encodeProtoBytes(7, []byte("file:///proto/ws2"))...) // field 7 URI
	convDBPath := filepath.Join(convDir, parentID+".db")
	convDB, err := sql.Open("sqlite", convDBPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := convDB.Exec(`CREATE TABLE trajectory_metadata_blob (id TEXT PRIMARY KEY, data BLOB)`); err != nil {
		t.Fatal(err)
	}
	if _, err := convDB.Exec(`INSERT INTO trajectory_metadata_blob VALUES ('main', ?)`, protoBlob); err != nil {
		t.Fatal(err)
	}
	_ = convDB.Close()

	protoPaths, err := ReadAgyConversationWorkspaces(context.Background(), convDir, parentID)
	if err != nil || len(protoPaths) != 2 || protoPaths[0] != "/proto/ws1" || protoPaths[1] != "/proto/ws2" {
		t.Fatalf("proto paths = %v, err = %v", protoPaths, err)
	}

	// AGY_SESSIONS_DIR override.
	t.Setenv("AGY_SESSIONS_DIR", filepath.Join(appDir, "brain"))
	overrideSources, err := DiscoverSessions(context.Background(), DiscoverOptions{Harnesses: []Harness{HarnessAgy}, Home: t.TempDir()})
	if err != nil || len(overrideSources) != 3 {
		t.Fatalf("override sources = %d, err = %v", len(overrideSources), err)
	}
}

func encodeProtoBytes(fieldNum byte, payload []byte) []byte {
	tag := (fieldNum << 3) | 2
	out := []byte{tag, byte(len(payload))}
	return append(out, payload...)
}
