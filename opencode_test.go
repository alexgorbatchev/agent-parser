package parser

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
)

func TestParseOpencodeModel(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantID      string
		wantHasConf bool
	}{
		{
			name:        "empty string",
			input:       "",
			wantID:      "unknown",
			wantHasConf: false,
		},
		{
			name:        "plain string model ID",
			input:       "claude-3-7-sonnet",
			wantID:      "claude-3-7-sonnet",
			wantHasConf: false,
		},
		{
			name:        "json object model config",
			input:       `{"id":"gpt-5.5","providerID":"openai","variant":"high"}`,
			wantID:      "gpt-5.5",
			wantHasConf: true,
		},
		{
			name:        "invalid json fallback",
			input:       `{"id":`,
			wantID:      `{"id":`,
			wantHasConf: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, cfg := ParseOpencodeModel(tt.input)
			if id != tt.wantID {
				t.Errorf("ParseOpencodeModel(%q) id = %q, want %q", tt.input, id, tt.wantID)
			}
			if (cfg != nil) != tt.wantHasConf {
				t.Errorf("ParseOpencodeModel(%q) config presence = %v, want %v", tt.input, cfg != nil, tt.wantHasConf)
			}
		})
	}
}

func TestParseOpencodeMessageData(t *testing.T) {
	rawUser := `{
		"role": "user",
		"agent": "build",
		"mode": "code",
		"path": {
			"cwd": "/workspace/project",
			"root": "/workspace"
		}
	}`

	msg, err := ParseOpencodeMessageData(rawUser)
	if err != nil {
		t.Fatalf("ParseOpencodeMessageData user error = %v", err)
	}
	if msg.Role != "user" {
		t.Errorf("expected role user, got %q", msg.Role)
	}
	if msg.Path == nil || msg.Path.CWD != "/workspace/project" {
		t.Errorf("expected cwd /workspace/project, got %+v", msg.Path)
	}

	rawAssistant := `{
		"role": "assistant",
		"modelID": "claude-3-7-sonnet",
		"providerID": "anthropic",
		"cost": 0.0152,
		"tokens": {
			"total": 1250,
			"input": 1000,
			"output": 250,
			"reasoning": 100,
			"cache": {
				"read": 500,
				"write": 0
			}
		},
		"time": {
			"created": 1700000000000,
			"completed": 1700000005000
		},
		"finish": "tool_use"
	}`

	asstMsg, err := ParseOpencodeMessageData(rawAssistant)
	if err != nil {
		t.Fatalf("ParseOpencodeMessageData assistant error = %v", err)
	}
	if asstMsg.Role != "assistant" || asstMsg.ModelID != "claude-3-7-sonnet" {
		t.Errorf("unexpected role or model: %+v", asstMsg)
	}
	if asstMsg.Cost == nil || *asstMsg.Cost != 0.0152 {
		t.Errorf("expected cost 0.0152, got %v", asstMsg.Cost)
	}
	if asstMsg.Tokens == nil || asstMsg.Tokens.Total != 1250 || asstMsg.Tokens.Reasoning != 100 {
		t.Errorf("unexpected tokens: %+v", asstMsg.Tokens)
	}
	if asstMsg.Finish == nil || *asstMsg.Finish != "tool_use" {
		t.Errorf("expected finish tool_use, got %v", asstMsg.Finish)
	}
}

func TestParseOpencodePartData(t *testing.T) {
	rawTool := `{
		"type": "tool",
		"tool": "edit",
		"callID": "call_12345",
		"state": {
			"status": "completed",
			"title": "src/main.ts",
			"input": {
				"path": "src/main.ts",
				"command": "update"
			},
			"output": "File updated successfully",
			"time": {
				"start": 1700000001000,
				"end": 1700000002000
			}
		}
	}`

	part, err := ParseOpencodePartData(rawTool)
	if err != nil {
		t.Fatalf("ParseOpencodePartData error = %v", err)
	}
	if part.Type != "tool" || part.Tool != "edit" || part.CallID != "call_12345" {
		t.Errorf("unexpected part data: %+v", part)
	}
	if part.State == nil || part.State.Status != "completed" || part.State.Output != "File updated successfully" {
		t.Errorf("unexpected state: %+v", part.State)
	}

	rawReasoning := `{
		"type": "reasoning",
		"text": "Analyzing the build structure to identify root cause"
	}`
	reasoningPart, err := ParseOpencodePartData(rawReasoning)
	if err != nil {
		t.Fatalf("ParseOpencodePartData reasoning error = %v", err)
	}
	if reasoningPart.Type != "reasoning" || reasoningPart.Text != "Analyzing the build structure to identify root cause" {
		t.Errorf("unexpected reasoning part: %+v", reasoningPart)
	}
}

func setupTestOpencodeDB(t *testing.T) *OpencodeDB {
	db, err := sql.Open("sqlite3", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("opening in-memory sqlite: %v", err)
	}

	schema := `
		CREATE TABLE project (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			worktree TEXT,
			vcs TEXT
		);

		CREATE TABLE session (
			id TEXT PRIMARY KEY,
			project_id TEXT,
			parent_id TEXT,
			slug TEXT,
			directory TEXT NOT NULL,
			title TEXT,
			version TEXT,
			agent TEXT,
			model TEXT,
			cost REAL,
			tokens_input INTEGER NOT NULL DEFAULT 0,
			tokens_output INTEGER NOT NULL DEFAULT 0,
			tokens_reasoning INTEGER NOT NULL DEFAULT 0,
			tokens_cache_read INTEGER NOT NULL DEFAULT 0,
			tokens_cache_write INTEGER NOT NULL DEFAULT 0,
			time_created INTEGER NOT NULL,
			time_updated INTEGER NOT NULL,
			time_archived INTEGER,
			metadata TEXT
		);

		CREATE TABLE message (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			time_created INTEGER NOT NULL,
			data TEXT NOT NULL
		);

		CREATE TABLE part (
			id TEXT PRIMARY KEY,
			message_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			time_created INTEGER NOT NULL,
			data TEXT NOT NULL
		);

		CREATE TABLE permission (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			type TEXT NOT NULL,
			data TEXT NOT NULL,
			time_created INTEGER NOT NULL
		);
	`
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		t.Fatalf("creating test schema: %v", err)
	}

	return NewOpencodeDB(db)
}

func TestOpencodeDB_QueriesAndReconstruction(t *testing.T) {
	odb := setupTestOpencodeDB(t)
	defer func() {
		_ = odb.Close()
	}()

	ctx := context.Background()

	// 1. Insert project
	_, err := odb.DB().Exec(`
		INSERT INTO project (id, name, worktree, vcs)
		VALUES ('proj_1', 'agent-status', '/workspace/agent-status', 'git');
	`)
	if err != nil {
		t.Fatalf("inserting project: %v", err)
	}

	// 2. Insert parent session
	_, err = odb.DB().Exec(`
		INSERT INTO session (
			id, project_id, parent_id, slug, directory, title,
			version, agent, model, cost, tokens_input, tokens_output,
			tokens_reasoning, tokens_cache_read, tokens_cache_write,
			time_created, time_updated, time_archived, metadata
		) VALUES (
			'ses_parent', 'proj_1', NULL, 'fix-session-parser', '/workspace/agent-status', 'Fix Session Parser',
			'0.4.1', 'build', '{"id":"claude-3-7-sonnet","providerID":"anthropic"}', 0.045, 2000, 500,
			200, 1000, 0, 1700000000000, 1700000010000, NULL, '{"priority":"high"}'
		);
	`)
	if err != nil {
		t.Fatalf("inserting parent session: %v", err)
	}

	// 3. Insert child subagent session
	_, err = odb.DB().Exec(`
		INSERT INTO session (
			id, project_id, parent_id, slug, directory, title,
			version, agent, model, cost, tokens_input, tokens_output,
			tokens_reasoning, tokens_cache_read, tokens_cache_write,
			time_created, time_updated, time_archived, metadata
		) VALUES (
			'ses_child', 'proj_1', 'ses_parent', 'subagent-task', '/workspace/agent-status', 'Subagent Task',
			'0.4.1', 'default', 'gpt-5.5', 0.012, 800, 200,
			0, 0, 0, 1700000005000, 1700000008000, NULL, NULL
		);
	`)
	if err != nil {
		t.Fatalf("inserting child session: %v", err)
	}

	// 4. Insert messages for parent session
	userMsgData := `{"role":"user","mode":"code","path":{"cwd":"/workspace/agent-status","root":"/workspace"}}`
	_, err = odb.DB().Exec(`
		INSERT INTO message (id, session_id, time_created, data)
		VALUES ('msg_u1', 'ses_parent', 1700000001000, ?);
	`, userMsgData)
	if err != nil {
		t.Fatalf("inserting user message: %v", err)
	}

	asstMsgData := `{
		"role":"assistant",
		"modelID":"claude-3-7-sonnet",
		"cost":0.045,
		"tokens":{"total":2700,"input":2000,"output":500,"reasoning":200,"cache":{"read":1000,"write":0}},
		"time":{"created":1700000002000,"completed":1700000008000},
		"finish":"stop"
	}`
	_, err = odb.DB().Exec(`
		INSERT INTO message (id, session_id, time_created, data)
		VALUES ('msg_a1', 'ses_parent', 1700000002000, ?);
	`, asstMsgData)
	if err != nil {
		t.Fatalf("inserting assistant message: %v", err)
	}

	// 5. Insert parts for user and assistant messages
	userPartData := `{"type":"text","text":"Please fix the parser tests"}`
	_, err = odb.DB().Exec(`
		INSERT INTO part (id, message_id, session_id, time_created, data)
		VALUES ('part_u1', 'msg_u1', 'ses_parent', 1700000001050, ?);
	`, userPartData)
	if err != nil {
		t.Fatalf("inserting user text part: %v", err)
	}

	reasoningPartData := `{"type":"reasoning","text":"Let me read the test failure logs first."}`
	_, err = odb.DB().Exec(`
		INSERT INTO part (id, message_id, session_id, time_created, data)
		VALUES ('part_r1', 'msg_a1', 'ses_parent', 1700000002100, ?);
	`, reasoningPartData)
	if err != nil {
		t.Fatalf("inserting reasoning part: %v", err)
	}

	toolPartData := `{
		"type":"tool",
		"tool":"read",
		"callID":"call_read_1",
		"state":{
			"status":"completed",
			"title":"packages/agent-parser/pi_test.go",
			"input":{"path":"packages/agent-parser/pi_test.go"},
			"output":"package parser\n\nfunc TestPi(t *testing.T) {}",
			"time":{"start":1700000003000,"end":1700000004000}
		}
	}`
	_, err = odb.DB().Exec(`
		INSERT INTO part (id, message_id, session_id, time_created, data)
		VALUES ('part_t1', 'msg_a1', 'ses_parent', 1700000003000, ?);
	`, toolPartData)
	if err != nil {
		t.Fatalf("inserting tool part: %v", err)
	}

	asTextPartData := `{"type":"text","text":"I have reviewed the test file and identified the issue."}`
	_, err = odb.DB().Exec(`
		INSERT INTO part (id, message_id, session_id, time_created, data)
		VALUES ('part_text1', 'msg_a1', 'ses_parent', 1700000005000, ?);
	`, asTextPartData)
	if err != nil {
		t.Fatalf("inserting assistant text part: %v", err)
	}

	// 6. Insert pending permission
	_, err = odb.DB().Exec(`
		INSERT INTO permission (id, session_id, type, data, time_created)
		VALUES ('perm_1', 'ses_parent', 'tool_execution', '{"tool":"bash","command":"rm -rf /tmp/junk"}', 1700000009000);
	`)
	if err != nil {
		t.Fatalf("inserting permission: %v", err)
	}

	// Verify Projects Query
	projects, err := odb.GetProjects(ctx)
	if err != nil {
		t.Fatalf("GetProjects error = %v", err)
	}
	if len(projects) != 1 || projects[0].Name != "agent-status" {
		t.Errorf("unexpected projects: %+v", projects)
	}

	// Verify Sessions Query
	sessions, err := odb.GetSessions(ctx)
	if err != nil {
		t.Fatalf("GetSessions error = %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}

	// Verify Session By ID
	parentSess, err := odb.GetSessionByID(ctx, "ses_parent")
	if err != nil {
		t.Fatalf("GetSessionByID(ses_parent) error = %v", err)
	}
	if parentSess.Title != "Fix Session Parser" || parentSess.Model != "claude-3-7-sonnet" || parentSess.IsSubagent {
		t.Errorf("unexpected parent session: %+v", parentSess)
	}
	if parentSess.Cost != 0.045 || parentSess.Tokens.Total != 3500 { // 2000 input + 500 output + 1000 cacheRead + 0 cacheWrite = 3500
		t.Errorf("unexpected tokens/cost: tokens=%+v, cost=%f", parentSess.Tokens, parentSess.Cost)
	}

	// Verify Child Sessions Query
	children, err := odb.GetChildSessions(ctx, "ses_parent")
	if err != nil {
		t.Fatalf("GetChildSessions error = %v", err)
	}
	if len(children) != 1 || children[0].ID != "ses_child" || !children[0].IsSubagent || children[0].ParentID == nil || *children[0].ParentID != "ses_parent" {
		t.Errorf("unexpected child sessions: %+v", children)
	}

	// Verify Messages Query
	messages, err := odb.GetMessages(ctx, "ses_parent")
	if err != nil {
		t.Fatalf("GetMessages error = %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}

	// Verify Parts Query
	parts, err := odb.GetParts(ctx, "ses_parent")
	if err != nil {
		t.Fatalf("GetParts error = %v", err)
	}
	if len(parts) != 4 {
		t.Fatalf("expected 4 parts, got %d", len(parts))
	}

	// Verify Pending Permissions Query
	perms, err := odb.GetPendingPermissions(ctx, "ses_parent")
	if err != nil {
		t.Fatalf("GetPendingPermissions error = %v", err)
	}
	if len(perms) != 1 || perms[0].Type != "tool_execution" {
		t.Errorf("unexpected permissions: %+v", perms)
	}

	// Verify Event Reconstruction
	events, err := odb.ReconstructSessionEvents(ctx, "ses_parent")
	if err != nil {
		t.Fatalf("ReconstructSessionEvents error = %v", err)
	}

	expectedEventTypes := []string{
		"run_started",
		"user_prompt",
		"thinking",
		"tool_use",
		"tool_result",
		"text",
		"turn_end",
	}

	if len(events) != len(expectedEventTypes) {
		t.Fatalf("expected %d events, got %d:\n%+v", len(expectedEventTypes), len(events), events)
	}

	for i, expectedType := range expectedEventTypes {
		if events[i].EventType != expectedType {
			t.Errorf("event[%d] type = %q, want %q", i, events[i].EventType, expectedType)
		}
	}

	// Inspect specific reconstructed event data
	runStarted := events[0]
	if runStarted.Data.Title == nil || *runStarted.Data.Title != "Fix Session Parser" {
		t.Errorf("run_started title = %v, want 'Fix Session Parser'", runStarted.Data.Title)
	}
	if runStarted.Data.Model == nil || *runStarted.Data.Model != "claude-3-7-sonnet" {
		t.Errorf("run_started model = %v, want 'claude-3-7-sonnet'", runStarted.Data.Model)
	}

	userPrompt := events[1]
	if userPrompt.Data.Content == nil || *userPrompt.Data.Content != "Please fix the parser tests" {
		t.Errorf("user_prompt content = %v", userPrompt.Data.Content)
	}

	thinking := events[2]
	if thinking.Data.Content == nil || *thinking.Data.Content != "Let me read the test failure logs first." {
		t.Errorf("thinking content = %v", thinking.Data.Content)
	}

	toolUse := events[3]
	if toolUse.Data.ToolName == nil || *toolUse.Data.ToolName != "read" || toolUse.Data.ToolCallID == nil || *toolUse.Data.ToolCallID != "call_read_1" {
		t.Errorf("tool_use data = %+v", toolUse.Data)
	}

	toolResult := events[4]
	if toolResult.Data.ToolCallID == nil || *toolResult.Data.ToolCallID != "call_read_1" || toolResult.Data.ToolOutput == nil || *toolResult.Data.ToolOutput != "package parser\n\nfunc TestPi(t *testing.T) {}" {
		t.Errorf("tool_result data = %+v", toolResult.Data)
	}
	if toolResult.Data.IsError == nil || *toolResult.Data.IsError {
		t.Errorf("tool_result isError = %v, want false", toolResult.Data.IsError)
	}

	textEvent := events[5]
	if textEvent.Data.Content == nil || *textEvent.Data.Content != "I have reviewed the test file and identified the issue." {
		t.Errorf("text event content = %v", textEvent.Data.Content)
	}

	turnEnd := events[6]
	if turnEnd.Data.Usage == nil || turnEnd.Data.Usage.Total != 2700 {
		t.Errorf("turn_end usage = %+v", turnEnd.Data.Usage)
	}
	if turnEnd.Data.RawHarnessCost == nil || *turnEnd.Data.RawHarnessCost != 0.045 {
		t.Errorf("turn_end cost = %v", turnEnd.Data.RawHarnessCost)
	}
	if turnEnd.Data.StopReason == nil || *turnEnd.Data.StopReason != "stop" {
		t.Errorf("turn_end stopReason = %v", turnEnd.Data.StopReason)
	}

	// Test ReconstructSessionEvents with non-existent session
	_, err = odb.ReconstructSessionEvents(ctx, "ses_non_existent")
	if err == nil {
		t.Errorf("expected error querying non-existent session, got nil")
	}

	// Test ReconstructSessionEvents with nil session
	nilEvents := ReconstructSessionEvents(nil, nil, nil)
	if len(nilEvents) != 0 {
		t.Errorf("expected 0 events for nil session, got %d", len(nilEvents))
	}
}

func TestOpencodeDB_ToolResultErrorStatus(t *testing.T) {
	session := &OpencodeSession{
		ID:          "ses_err",
		Directory:   "/test",
		Title:       "Error Test",
		Model:       "gpt-5.5",
		TimeCreated: time.Now().UnixMilli(),
	}

	messages := []OpencodeMessage{
		{
			ID:          "msg_1",
			SessionID:   "ses_err",
			TimeCreated: time.Now().UnixMilli(),
			Data: OpencodeMessageData{
				Role: "assistant",
			},
		},
	}

	errOutput := "command not found: foobar"
	parts := []OpencodePart{
		{
			ID:          "part_err",
			MessageID:   "msg_1",
			SessionID:   "ses_err",
			TimeCreated: time.Now().UnixMilli(),
			Data: OpencodePartData{
				Type:   "tool",
				Tool:   "bash",
				CallID: "call_bash_err",
				State: &OpencodeToolPartState{
					Status: "error",
					Output: errOutput,
				},
			},
		},
	}

	events := ReconstructSessionEvents(session, messages, parts)
	var foundResult *ParsedEvent
	for i := range events {
		if events[i].EventType == "tool_result" {
			foundResult = &events[i]
			break
		}
	}

	if foundResult == nil {
		t.Fatalf("expected tool_result event")
	}
	if foundResult.Data.IsError == nil || !*foundResult.Data.IsError {
		t.Errorf("expected isError = true for failed tool execution, got %v", foundResult.Data.IsError)
	}
}

func TestOpencodeDB_ParseDataErrors(t *testing.T) {
	if _, err := ParseOpencodeMessageData("{invalid"); err == nil {
		t.Error("expected error for invalid message data")
	}
	if _, err := ParseOpencodePartData("{invalid"); err == nil {
		t.Error("expected error for invalid part data")
	}
	m, err := ParseOpencodeModel("plain-model-name")
	if err != nil || m != "plain-model-name" {
		t.Errorf("expected plain string model, got %q, err %v", m, err)
	}
	if m, err := ParseOpencodeModel(""); err != nil || m != "unknown" {
		t.Errorf("expected 'unknown' model for empty string, got %q, err %v", m, err)
	}
}

func TestOpencodeDB_OpenAndGetPartsByMessage(t *testing.T) {
	odb, err := OpenOpencodeDB(":memory:")
	if err != nil {
		t.Fatalf("OpenOpencodeDB(:memory:) failed: %v", err)
	}
	defer func() { _ = odb.Close() }()

	_, err = odb.DB().Exec(`
		CREATE TABLE IF NOT EXISTS part (
			id TEXT PRIMARY KEY,
			message_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			time_created INTEGER NOT NULL,
			data TEXT NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("creating part table: %v", err)
	}

	_, err = odb.DB().Exec(`
		INSERT INTO part (id, message_id, session_id, time_created, data)
		VALUES ('part_1', 'msg_100', 'ses_1', 1700000000000, '{"type":"text","text":"hello world"}');
	`)
	if err != nil {
		t.Fatalf("inserting part: %v", err)
	}

	parts, err := odb.GetPartsByMessage(context.Background(), "msg_100")
	if err != nil {
		t.Fatalf("GetPartsByMessage failed: %v", err)
	}
	if len(parts) != 1 || parts[0].Data.Text != "hello world" {
		t.Errorf("unexpected parts: %+v", parts)
	}
}

func TestOpencodeParser_NormalizedFileModification(t *testing.T) {
	session := &OpencodeSession{
		ID:          "ses_oc_norm",
		Directory:   "/workspace",
		Title:       "OpenCode Test",
		TimeCreated: 1700000000000,
	}
	messages := []OpencodeMessage{
		{
			ID:          "msg_1",
			SessionID:   "ses_oc_norm",
			TimeCreated: 1700000000000,
			Data: OpencodeMessageData{
				Role: "assistant",
			},
		},
	}

	t.Run("edit part produces normalized FileModification", func(t *testing.T) {
		parts := []OpencodePart{
			{
				ID:          "part_edit_1",
				MessageID:   "msg_1",
				SessionID:   "ses_oc_norm",
				TimeCreated: 1700000001000,
				Data: OpencodePartData{
					Type:   "tool",
					Tool:   "edit",
					CallID: "call_oc_1",
					State: &OpencodeToolPartState{
						Status: "running",
						Input: map[string]interface{}{
							"filePath": "src/server.go",
							"oldText":  "port := 8080",
							"newText":  "port := 9090",
						},
					},
				},
			},
		}
		evs := ReconstructSessionEvents(session, messages, parts)
		var toolCall *ParsedEvent
		for i := range evs {
			if evs[i].EventType == "tool_use" {
				toolCall = &evs[i]
				break
			}
		}
		if toolCall == nil {
			t.Fatal("expected tool_use event")
		}
		fm := toolCall.Data.FileModification
		if fm == nil {
			t.Fatal("expected non-nil FileModification for OpenCode edit tool")
		}
		if fm.Action != "EDIT" {
			t.Errorf("Action = %q, want 'EDIT'", fm.Action)
		}
		if fm.FilePath != "src/server.go" {
			t.Errorf("FilePath = %q, want 'src/server.go'", fm.FilePath)
		}
		if len(fm.Hunks) != 1 {
			t.Fatalf("expected 1 hunk, got %d", len(fm.Hunks))
		}
	})

	t.Run("write part produces normalized FileModification", func(t *testing.T) {
		parts := []OpencodePart{
			{
				ID:          "part_write_1",
				MessageID:   "msg_1",
				SessionID:   "ses_oc_norm",
				TimeCreated: 1700000002000,
				Data: OpencodePartData{
					Type:   "tool",
					Tool:   "write",
					CallID: "call_oc_2",
					State: &OpencodeToolPartState{
						Status: "running",
						Input: map[string]interface{}{
							"filePath": "notes.txt",
							"content":  "some notes\n",
						},
					},
				},
			},
		}
		evs := ReconstructSessionEvents(session, messages, parts)
		var toolCall *ParsedEvent
		for i := range evs {
			if evs[i].EventType == "tool_use" {
				toolCall = &evs[i]
				break
			}
		}
		if toolCall == nil {
			t.Fatal("expected tool_use event")
		}
		fm := toolCall.Data.FileModification
		if fm == nil {
			t.Fatal("expected non-nil FileModification for OpenCode write tool")
		}
		if fm.Action != "WRITE" {
			t.Errorf("Action = %q, want 'WRITE'", fm.Action)
		}
		if fm.FilePath != "notes.txt" {
			t.Errorf("FilePath = %q, want 'notes.txt'", fm.FilePath)
		}
		if fm.Content == nil || *fm.Content != "some notes\n" {
			t.Errorf("Content = %v, want 'some notes\\n'", fm.Content)
		}
	})
}

func TestOpencode_ErrorEventEmission(t *testing.T) {
	session := &OpencodeSession{
		ID:          "ses_err_test",
		Directory:   "/workspace/project",
		Title:       "Error Emission Test",
		Model:       "claude-3-7-sonnet",
		TimeCreated: 1700000000000,
	}

	finishErr := "error"
	finishStop := "stop"

	tests := []struct {
		name        string
		message     OpencodeMessage
		parts       []OpencodePart
		wantError   bool
		wantContent string
		wantTurnEnd bool
	}{
		{
			name: "finish_error_without_payload",
			message: OpencodeMessage{
				ID:          "msg_err_1",
				SessionID:   session.ID,
				TimeCreated: 1700000001000,
				Data: OpencodeMessageData{
					Role:   "assistant",
					Finish: &finishErr,
				},
			},
			wantError:   true,
			wantContent: "error",
			wantTurnEnd: true,
		},
		{
			name: "finish_error_with_string_payload",
			message: OpencodeMessage{
				ID:          "msg_err_2",
				SessionID:   session.ID,
				TimeCreated: 1700000002000,
				Data: OpencodeMessageData{
					Role:   "assistant",
					Finish: &finishErr,
					Error:  "Rate limit exceeded: 429 too many requests",
				},
			},
			wantError:   true,
			wantContent: "Rate limit exceeded: 429 too many requests",
			wantTurnEnd: true,
		},
		{
			name: "structured_provider_auth_error",
			message: OpencodeMessage{
				ID:          "msg_err_3",
				SessionID:   session.ID,
				TimeCreated: 1700000003000,
				Data: OpencodeMessageData{
					Role:   "assistant",
					Finish: &finishErr,
					Error: map[string]interface{}{
						"name": "ProviderAuthError",
						"data": map[string]interface{}{
							"message":    "Invalid API key provided for anthropic",
							"providerID": "anthropic",
						},
					},
				},
			},
			wantError:   true,
			wantContent: "Invalid API key provided for anthropic",
			wantTurnEnd: true,
		},
		{
			name: "structured_api_error",
			message: OpencodeMessage{
				ID:          "msg_err_4",
				SessionID:   session.ID,
				TimeCreated: 1700000004000,
				Data: OpencodeMessageData{
					Role:   "assistant",
					Finish: &finishErr,
					Error: map[string]interface{}{
						"name":    "APIError",
						"message": "Upstream service timeout",
					},
				},
			},
			wantError:   true,
			wantContent: "Upstream service timeout",
			wantTurnEnd: true,
		},
		{
			name: "error_payload_without_finish_error",
			message: OpencodeMessage{
				ID:          "msg_err_5",
				SessionID:   session.ID,
				TimeCreated: 1700000005000,
				Data: OpencodeMessageData{
					Role:   "assistant",
					Finish: &finishStop,
					Error: map[string]interface{}{
						"message": "Stream interrupted before completion",
					},
				},
			},
			wantError:   true,
			wantContent: "Stream interrupted before completion",
			wantTurnEnd: true,
		},
		{
			name: "typed_unknown_error",
			message: OpencodeMessage{
				ID:          "msg_err_unknown",
				SessionID:   session.ID,
				TimeCreated: 1700000005500,
				Data: OpencodeMessageData{
					Role:   "assistant",
					Finish: &finishErr,
					Error: map[string]interface{}{
						"name": "UnknownError",
						"data": map[string]interface{}{
							"message": "Internal gateway failure",
						},
					},
				},
			},
			wantError:   true,
			wantContent: "Internal gateway failure",
			wantTurnEnd: true,
		},
		{
			name: "typed_message_output_length_error",
			message: OpencodeMessage{
				ID:          "msg_err_length",
				SessionID:   session.ID,
				TimeCreated: 1700000005600,
				Data: OpencodeMessageData{
					Role:   "assistant",
					Finish: &finishErr,
					Error: map[string]interface{}{
						"name": "MessageOutputLengthError",
						"data": map[string]interface{}{
							"message": "Maximum output token length exceeded",
						},
					},
				},
			},
			wantError:   true,
			wantContent: "Maximum output token length exceeded",
			wantTurnEnd: true,
		},
		{
			name: "message_aborted_error_does_not_emit_error",
			message: OpencodeMessage{
				ID:          "msg_err_abort",
				SessionID:   session.ID,
				TimeCreated: 1700000005700,
				Data: OpencodeMessageData{
					Role:   "assistant",
					Finish: &finishStop,
					Error: map[string]interface{}{
						"name": "MessageAbortedError",
						"data": map[string]interface{}{
							"message": "Message aborted by user",
						},
					},
				},
			},
			wantError:   false,
			wantTurnEnd: true,
		},
		{
			name: "retry_part_with_successful_finish_does_not_emit_error",
			message: OpencodeMessage{
				ID:          "msg_retry_success",
				SessionID:   session.ID,
				TimeCreated: 1700000005800,
				Data: OpencodeMessageData{
					Role:   "assistant",
					Finish: &finishStop,
				},
			},
			parts: []OpencodePart{
				{
					ID:          "part_retry_success",
					MessageID:   "msg_retry_success",
					SessionID:   session.ID,
					TimeCreated: 1700000005900,
					Data: OpencodePartData{
						Type:    "retry",
						Attempt: intPtr(1),
						Error: map[string]interface{}{
							"name": "APIError",
							"data": map[string]interface{}{
								"message":    "Service Unavailable: 503",
								"statusCode": 503,
							},
						},
					},
				},
			},
			wantError:   false,
			wantTurnEnd: true,
		},
		{
			name: "retry_part_with_api_error",
			message: OpencodeMessage{
				ID:          "msg_err_retry",
				SessionID:   session.ID,
				TimeCreated: 1700000006000,
				Data: OpencodeMessageData{
					Role:   "assistant",
					Finish: &finishErr,
				},
			},
			parts: []OpencodePart{
				{
					ID:          "part_err_retry",
					MessageID:   "msg_err_retry",
					SessionID:   session.ID,
					TimeCreated: 1700000006500,
					Data: OpencodePartData{
						Type:    "retry",
						Attempt: intPtr(1),
						Error: map[string]interface{}{
							"name": "APIError",
							"data": map[string]interface{}{
								"message":    "Rate limit exceeded: 429",
								"statusCode": 429,
							},
						},
					},
				},
			},
			wantError:   true,
			wantContent: "Rate limit exceeded: 429",
			wantTurnEnd: true,
		},
		{
			name: "retry_part_with_nil_finish_does_not_emit_error",
			message: OpencodeMessage{
				ID:          "msg_retry_nil_finish",
				SessionID:   session.ID,
				TimeCreated: 1700000006600,
				Data: OpencodeMessageData{
					Role: "assistant",
				},
			},
			parts: []OpencodePart{
				{
					ID:          "part_retry_nil_finish",
					MessageID:   "msg_retry_nil_finish",
					SessionID:   session.ID,
					TimeCreated: 1700000006700,
					Data: OpencodePartData{
						Type:    "retry",
						Attempt: intPtr(1),
						Error: map[string]interface{}{
							"name": "APIError",
							"data": map[string]interface{}{
								"message":    "Service Unavailable: 503",
								"statusCode": 503,
							},
						},
					},
				},
			},
			wantError:   false,
			wantTurnEnd: false,
		},
		{
			name: "error_payload_with_nil_finish",
			message: OpencodeMessage{
				ID:          "msg_err_nil_finish",
				SessionID:   session.ID,
				TimeCreated: 1700000006800,
				Data: OpencodeMessageData{
					Role: "assistant",
					Error: map[string]interface{}{
						"message": "Stream interrupted before completion",
					},
				},
			},
			wantError:   true,
			wantContent: "Stream interrupted before completion",
			wantTurnEnd: false,
		},
		{
			name: "unhandled_exception_payload",
			message: OpencodeMessage{
				ID:          "msg_err_8",
				SessionID:   session.ID,
				TimeCreated: 1700000008000,
				Data: OpencodeMessageData{
					Role:   "assistant",
					Finish: &finishErr,
					Error: map[string]interface{}{
						"name": "UnhandledException",
						"data": map[string]interface{}{
							"exception": "panic: runtime error: nil pointer dereference",
						},
					},
				},
			},
			wantError:   true,
			wantContent: "panic: runtime error: nil pointer dereference",
			wantTurnEnd: true,
		},
		{
			name: "tool_use_with_turn_error",
			message: OpencodeMessage{
				ID:          "msg_err_9",
				SessionID:   session.ID,
				TimeCreated: 1700000009000,
				Data: OpencodeMessageData{
					Role:   "assistant",
					Finish: &finishErr,
					Error:  "Tool execution aborted by host",
				},
			},
			parts: []OpencodePart{
				{
					ID:          "part_tool_9",
					MessageID:   "msg_err_9",
					SessionID:   session.ID,
					TimeCreated: 1700000009100,
					Data: OpencodePartData{
						Type:   "tool",
						Tool:   "bash",
						CallID: "call_bash_9",
						State: &OpencodeToolPartState{
							Status: "error",
							Output: "aborted",
						},
					},
				},
			},
			wantError:   true,
			wantContent: "Tool execution aborted by host",
			wantTurnEnd: true,
		},
		{
			name: "tool_part_state_error",
			message: OpencodeMessage{
				ID:          "msg_err_14",
				SessionID:   session.ID,
				TimeCreated: 1700000014000,
				Data: OpencodeMessageData{
					Role:   "assistant",
					Finish: &finishErr,
				},
			},
			parts: []OpencodePart{
				{
					ID:          "part_err_14",
					MessageID:   "msg_err_14",
					SessionID:   session.ID,
					TimeCreated: 1700000014500,
					Data: OpencodePartData{
						Type:   "tool",
						Tool:   "execute",
						CallID: "call_14",
						State: &OpencodeToolPartState{
							Status: "error",
							Output: "",
							Error:  "failed to spawn binary",
						},
					},
				},
			},
			wantError:   true,
			wantContent: "failed to spawn binary",
			wantTurnEnd: true,
		},
		{
			name: "tool_part_error_with_successful_finish_does_not_emit_error",
			message: OpencodeMessage{
				ID:          "msg_tool_err_success",
				SessionID:   session.ID,
				TimeCreated: 1700000014600,
				Data: OpencodeMessageData{
					Role:   "assistant",
					Finish: &finishStop,
				},
			},
			parts: []OpencodePart{
				{
					ID:          "part_tool_err_success",
					MessageID:   "msg_tool_err_success",
					SessionID:   session.ID,
					TimeCreated: 1700000014700,
					Data: OpencodePartData{
						Type:   "tool",
						Tool:   "execute",
						CallID: "call_14_success",
						State: &OpencodeToolPartState{
							Status: "error",
							Output: "",
							Error:  "failed to spawn binary",
						},
					},
				},
			},
			wantError:   false,
			wantTurnEnd: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evs := ReconstructSessionEvents(session, []OpencodeMessage{tt.message}, tt.parts)

			var errorEv *ParsedEvent
			var turnEndEv *ParsedEvent
			turnEndIdx := -1
			errorIdx := -1

			for i := range evs {
				switch evs[i].EventType {
				case "error":
					errorEv = &evs[i]
					errorIdx = i
				case "turn_end":
					turnEndEv = &evs[i]
					turnEndIdx = i
				}
			}

			if tt.wantError {
				if errorEv == nil {
					t.Fatalf("expected EventType 'error' event, but none was emitted. Events: %+v", evs)
				}
				if errorEv.Data.IsError == nil || !*errorEv.Data.IsError {
					t.Errorf("error event IsError = %v, want true", errorEv.Data.IsError)
				}
				if errorEv.Data.Content == nil || *errorEv.Data.Content != tt.wantContent {
					got := "<nil>"
					if errorEv.Data.Content != nil {
						got = *errorEv.Data.Content
					}
					t.Errorf("error event Content = %q, want %q", got, tt.wantContent)
				}
				if errorEv.Data.Role == nil || *errorEv.Data.Role != "assistant" {
					t.Errorf("error event Role = %v, want 'assistant'", errorEv.Data.Role)
				}
			} else {
				if errorEv != nil {
					t.Errorf("did not expect error event, but got: %+v", errorEv)
				}
			}

			if tt.wantTurnEnd {
				if turnEndEv == nil {
					t.Errorf("expected turn_end event, but none found")
				}
				// Error event must follow turn_end event so classifier transitions to ActivityError
				if tt.wantError && errorIdx < turnEndIdx {
					t.Errorf("error event index (%d) must follow turn_end index (%d)", errorIdx, turnEndIdx)
				}
			} else {
				if turnEndEv != nil {
					t.Errorf("did not expect turn_end event, but got: %+v", turnEndEv)
				}
			}
		})
	}
}

func TestParseOpencodeMessageData_WithError(t *testing.T) {
	raw := `{
		"role": "assistant",
		"finish": "error",
		"error": {
			"name": "ProviderAuthError",
			"data": {
				"message": "API key rejected"
			}
		}
	}`
	msg, err := ParseOpencodeMessageData(raw)
	if err != nil {
		t.Fatalf("ParseOpencodeMessageData failed: %v", err)
	}
	if msg.Finish == nil || *msg.Finish != "error" {
		t.Errorf("Finish = %v, want 'error'", msg.Finish)
	}
	if msg.Error == nil {
		t.Fatalf("Error is nil, expected map")
	}
	errMap, ok := msg.Error.(map[string]interface{})
	if !ok {
		t.Fatalf("Error is %T, want map[string]interface{}", msg.Error)
	}
	if errMap["name"] != "ProviderAuthError" {
		t.Errorf("Error.name = %v, want 'ProviderAuthError'", errMap["name"])
	}
}

func TestParseOpencodePartData_RetryPart(t *testing.T) {
	raw := `{
		"type": "retry",
		"attempt": 2,
		"error": {
			"name": "APIError",
			"data": {
				"message": "Rate limit exceeded: 429",
				"statusCode": 429,
				"isRetryable": true
			}
		}
	}`
	part, err := ParseOpencodePartData(raw)
	if err != nil {
		t.Fatalf("ParseOpencodePartData failed: %v", err)
	}
	if part.Type != "retry" {
		t.Errorf("part.Type = %q, want 'retry'", part.Type)
	}
	if part.Attempt == nil || *part.Attempt != 2 {
		t.Errorf("part.Attempt = %v, want 2", part.Attempt)
	}
	if part.Error == nil {
		t.Fatalf("part.Error is nil, expected map")
	}
}

func TestOpencode_RateLimitParsing(t *testing.T) {
	session := &OpencodeSession{
		ID:          "ses_rl_test",
		Directory:   "/workspace/project",
		Title:       "Rate Limit Test",
		Model:       "claude-3-7-sonnet",
		TimeCreated: 1700000000000,
	}

	finishErr := "error"
	finishStop := "stop"

	t.Run("APIError with statusCode 429 and retry-after header", func(t *testing.T) {
		msgs := []OpencodeMessage{
			{
				ID:          "msg_rl_1",
				SessionID:   session.ID,
				TimeCreated: 1700000005000,
				Data: OpencodeMessageData{
					Role:   "assistant",
					Finish: &finishErr,
					Error: map[string]interface{}{
						"name": "APIError",
						"data": map[string]interface{}{
							"message":    "Rate limit exceeded",
							"statusCode": 429,
							"responseHeaders": map[string]interface{}{
								"retry-after": "60",
							},
						},
					},
				},
			},
		}

		events := ReconstructSessionEvents(session, msgs, nil)
		var errEvent *ParsedEvent
		for i := range events {
			if events[i].EventType == "error" {
				errEvent = &events[i]
				break
			}
		}
		if errEvent == nil {
			t.Fatalf("expected error event emitted, got %d events", len(events))
		}
		if errEvent.Data.IsRateLimit == nil || !*errEvent.Data.IsRateLimit {
			t.Errorf("expected IsRateLimit = true for statusCode 429")
		}
		if errEvent.Data.ExitCode != nil {
			t.Errorf("expected ExitCode = nil (HTTP status codes must not be assigned to ExitCode), got %v", errEvent.Data.ExitCode)
		}
		if errEvent.Data.RetryAfterSeconds == nil || *errEvent.Data.RetryAfterSeconds != 60 {
			t.Errorf("expected RetryAfterSeconds = 60, got %v", errEvent.Data.RetryAfterSeconds)
		}
		if errEvent.Data.RateLimitResetAt == nil || *errEvent.Data.RateLimitResetAt != 1700000065000 {
			t.Errorf("expected RateLimitResetAt = 1700000065000, got %v", errEvent.Data.RateLimitResetAt)
		}
	})

	t.Run("APIError with statusCode 429 and RFC3339 retry-after header", func(t *testing.T) {
		msgs := []OpencodeMessage{
			{
				ID:          "msg_rl_rfc3339",
				SessionID:   session.ID,
				TimeCreated: 1781280493000, // 2026-06-12T16:08:13Z
				Data: OpencodeMessageData{
					Role:   "assistant",
					Finish: &finishErr,
					Error: map[string]interface{}{
						"name": "APIError",
						"data": map[string]interface{}{
							"message":    "Rate limit exceeded",
							"statusCode": 429,
							"responseHeaders": map[string]interface{}{
								"retry-after": "2026-06-12T16:08:43Z",
							},
						},
					},
				},
			},
		}

		events := ReconstructSessionEvents(session, msgs, nil)
		var errEvent *ParsedEvent
		for i := range events {
			if events[i].EventType == "error" {
				errEvent = &events[i]
				break
			}
		}
		if errEvent == nil {
			t.Fatalf("expected error event emitted, got %d events", len(events))
		}
		if errEvent.Data.IsRateLimit == nil || !*errEvent.Data.IsRateLimit {
			t.Errorf("expected IsRateLimit = true")
		}
		if errEvent.Data.RetryAfterSeconds == nil || *errEvent.Data.RetryAfterSeconds != 30 {
			t.Errorf("expected RetryAfterSeconds = 30, got %v (must not scan 2026 as numeric seconds)", errEvent.Data.RetryAfterSeconds)
		}
		if errEvent.Data.RateLimitResetAt == nil || *errEvent.Data.RateLimitResetAt != 1781280523000 {
			t.Errorf("expected RateLimitResetAt = 1781280523000, got %v", errEvent.Data.RateLimitResetAt)
		}
	})

	t.Run("RetryPart with statusCode 429 and retry-after header", func(t *testing.T) {
		msgs := []OpencodeMessage{
			{
				ID:          "msg_rl_2",
				SessionID:   session.ID,
				TimeCreated: 1700000010000,
				Data: OpencodeMessageData{
					Role:   "assistant",
					Finish: &finishErr,
				},
			},
		}
		parts := []OpencodePart{
			{
				ID:          "part_retry_429",
				MessageID:   "msg_rl_2",
				SessionID:   session.ID,
				TimeCreated: 1700000010500,
				Data: OpencodePartData{
					Type:    "retry",
					Attempt: intPtr(1),
					Error: map[string]interface{}{
						"name": "APIError",
						"data": map[string]interface{}{
							"message":    "Rate limit exceeded: 429",
							"statusCode": 429,
							"responseHeaders": map[string]interface{}{
								"retry-after": "30",
							},
						},
					},
				},
			},
		}

		events := ReconstructSessionEvents(session, msgs, parts)
		var errEvent *ParsedEvent
		for i := range events {
			if events[i].EventType == "error" {
				errEvent = &events[i]
				break
			}
		}
		if errEvent == nil {
			t.Fatalf("expected error event emitted, got %d events", len(events))
		}
		if errEvent.Data.IsRateLimit == nil || !*errEvent.Data.IsRateLimit {
			t.Errorf("expected IsRateLimit = true for RetryPart 429")
		}
		if errEvent.Data.ExitCode != nil {
			t.Errorf("expected ExitCode = nil (HTTP status codes must not be assigned to ExitCode), got %v", errEvent.Data.ExitCode)
		}
		if errEvent.Data.RetryAfterSeconds == nil || *errEvent.Data.RetryAfterSeconds != 30 {
			t.Errorf("expected RetryAfterSeconds = 30, got %v", errEvent.Data.RetryAfterSeconds)
		}
	})

	t.Run("In-flight RetryPart with statusCode 429 and nil finish emits error event", func(t *testing.T) {
		msgs := []OpencodeMessage{
			{
				ID:          "msg_inflight_rl",
				SessionID:   session.ID,
				TimeCreated: 1700000015000,
				Data: OpencodeMessageData{
					Role: "assistant",
				},
			},
		}
		parts := []OpencodePart{
			{
				ID:          "part_inflight_429",
				MessageID:   "msg_inflight_rl",
				SessionID:   session.ID,
				TimeCreated: 1700000015500,
				Data: OpencodePartData{
					Type:    "retry",
					Attempt: intPtr(1),
					Error: map[string]interface{}{
						"name": "APIError",
						"data": map[string]interface{}{
							"message":    "Too many requests: 429",
							"statusCode": 429,
							"responseHeaders": map[string]interface{}{
								"retry-after": "45",
							},
						},
					},
				},
			},
		}

		events := ReconstructSessionEvents(session, msgs, parts)
		var errEvent *ParsedEvent
		for i := range events {
			if events[i].EventType == "error" {
				errEvent = &events[i]
				break
			}
		}
		if errEvent == nil {
			t.Fatalf("expected error event emitted for in-flight retry 429, got %d events", len(events))
		}
		if errEvent.Data.IsRateLimit == nil || !*errEvent.Data.IsRateLimit {
			t.Errorf("expected IsRateLimit = true for in-flight RetryPart 429")
		}
		if errEvent.Data.ExitCode != nil {
			t.Errorf("expected ExitCode = nil (HTTP status codes must not be assigned to ExitCode), got %v", errEvent.Data.ExitCode)
		}
		if errEvent.Data.RetryAfterSeconds == nil || *errEvent.Data.RetryAfterSeconds != 45 {
			t.Errorf("expected RetryAfterSeconds = 45, got %v", errEvent.Data.RetryAfterSeconds)
		}
		if errEvent.Data.RateLimitResetAt == nil || *errEvent.Data.RateLimitResetAt != 1700000015500+45000 {
			t.Errorf("expected RateLimitResetAt = %d, got %v", 1700000015500+45000, errEvent.Data.RateLimitResetAt)
		}
	})

	t.Run("In-flight RetryPart with statusCode 429 and stop finish emits error event", func(t *testing.T) {
		msgs := []OpencodeMessage{
			{
				ID:          "msg_stop_rl",
				SessionID:   session.ID,
				TimeCreated: 1700000016000,
				Data: OpencodeMessageData{
					Role:   "assistant",
					Finish: &finishStop,
				},
			},
		}
		parts := []OpencodePart{
			{
				ID:          "part_stop_429",
				MessageID:   "msg_stop_rl",
				SessionID:   session.ID,
				TimeCreated: 1700000016500,
				Data: OpencodePartData{
					Type:    "retry",
					Attempt: intPtr(1),
					Error: map[string]interface{}{
						"name": "APIError",
						"data": map[string]interface{}{
							"message":    "Rate limit exceeded: 429",
							"statusCode": 429,
							"responseHeaders": map[string]interface{}{
								"retry-after": "20",
							},
						},
					},
				},
			},
		}

		events := ReconstructSessionEvents(session, msgs, parts)
		var errEvent *ParsedEvent
		for i := range events {
			if events[i].EventType == "error" {
				errEvent = &events[i]
				break
			}
		}
		if errEvent == nil {
			t.Fatalf("expected error event emitted for retry 429 with stop finish, got %d events", len(events))
		}
		if errEvent.Data.IsRateLimit == nil || !*errEvent.Data.IsRateLimit {
			t.Errorf("expected IsRateLimit = true for RetryPart 429 with stop finish")
		}
		if errEvent.Data.ExitCode != nil {
			t.Errorf("expected ExitCode = nil (HTTP status codes must not be assigned to ExitCode), got %v", errEvent.Data.ExitCode)
		}
		if errEvent.Data.RetryAfterSeconds == nil || *errEvent.Data.RetryAfterSeconds != 20 {
			t.Errorf("expected RetryAfterSeconds = 20, got %v", errEvent.Data.RetryAfterSeconds)
		}
	})

	t.Run("APIError with statusCode 500 is not rate limited", func(t *testing.T) {
		msgs := []OpencodeMessage{
			{
				ID:          "msg_err_500",
				SessionID:   session.ID,
				TimeCreated: 1700000020000,
				Data: OpencodeMessageData{
					Role:   "assistant",
					Finish: &finishErr,
					Error: map[string]interface{}{
						"name": "APIError",
						"data": map[string]interface{}{
							"message":    "Internal server error",
							"statusCode": 500,
						},
					},
				},
			},
		}

		events := ReconstructSessionEvents(session, msgs, nil)
		var errEvent *ParsedEvent
		for i := range events {
			if events[i].EventType == "error" {
				errEvent = &events[i]
				break
			}
		}
		if errEvent == nil {
			t.Fatalf("expected error event emitted, got %d events", len(events))
		}
		if errEvent.Data.IsRateLimit != nil && *errEvent.Data.IsRateLimit {
			t.Errorf("expected IsRateLimit = false or nil for statusCode 500, got true")
		}
	})

	t.Run("terminal non-rate-limit error takes strict precedence over intermediate retry rate limit", func(t *testing.T) {
		session := &OpencodeSession{
			ID:        "ses-terminal-precedence",
			Directory: "/repo",
		}
		finishErr := "error"
		msgs := []OpencodeMessage{
			{
				ID:          "msg-retry-then-500",
				SessionID:   session.ID,
				TimeCreated: 1700000000000,
				Data: OpencodeMessageData{
					Role:   "assistant",
					Finish: &finishErr,
					Error: map[string]interface{}{
						"name": "APIError",
						"data": map[string]interface{}{
							"message":    "Internal server error",
							"statusCode": 500,
						},
					},
				},
			},
		}
		parts := []OpencodePart{
			{
				ID:          "prt-retry-429",
				MessageID:   "msg-retry-then-500",
				TimeCreated: 1700000001000,
				Data: OpencodePartData{
					Type: "retry",
					Error: map[string]interface{}{
						"name": "RateLimitError",
						"data": map[string]interface{}{
							"statusCode": 429,
							"headers": map[string]interface{}{
								"retry-after": "45",
							},
						},
					},
				},
			},
		}

		events := ReconstructSessionEvents(session, msgs, parts)
		var terminalErrEvent *ParsedEvent
		for i := len(events) - 1; i >= 0; i-- {
			if events[i].EventType == "error" {
				terminalErrEvent = &events[i]
				break
			}
		}
		if terminalErrEvent == nil {
			t.Fatalf("expected terminal error event emitted, got %d events", len(events))
		}
		if terminalErrEvent.Data.IsRateLimit == nil || *terminalErrEvent.Data.IsRateLimit {
			t.Errorf("expected IsRateLimit = false for terminal 500 failure, got %v", terminalErrEvent.Data.IsRateLimit)
		}
		if terminalErrEvent.Data.RetryAfterSeconds != nil {
			t.Errorf("expected RetryAfterSeconds = nil for terminal 500 failure, got %v", terminalErrEvent.Data.RetryAfterSeconds)
		}
		if terminalErrEvent.Data.RateLimitResetAt != nil {
			t.Errorf("expected RateLimitResetAt = nil for terminal 500 failure, got %v", terminalErrEvent.Data.RateLimitResetAt)
		}
	})
}

func TestParseOpencodeApiErrorInfo_Branches(t *testing.T) {
	nowMs := int64(1700000000000)

	// nil
	res := parseOpencodeApiErrorInfo(nil, nowMs)
	if res.statusCode != nil || res.isRateLimit {
		t.Errorf("expected empty for nil")
	}

	// non-map
	res = parseOpencodeApiErrorInfo("simple error string", nowMs)
	if res.message != "simple error string" {
		t.Errorf("expected string message, got %q", res.message)
	}

	// status in root map
	res = parseOpencodeApiErrorInfo(map[string]interface{}{
		"status":  429,
		"message": "Too many requests",
		"responseHeaders": map[string]interface{}{
			"retry-after": "Wed, 21 Oct 2026 07:28:00 GMT",
		},
	}, nowMs)
	if !res.isRateLimit || res.statusCode == nil || *res.statusCode != 429 {
		t.Errorf("expected rate limit for root status 429")
	}
	if res.rateLimitResetAt == nil {
		t.Errorf("expected rateLimitResetAt for RFC1123 header")
	}

	// past RFC1123
	res = parseOpencodeApiErrorInfo(map[string]interface{}{
		"status": 429,
		"responseHeaders": map[string]interface{}{
			"retry-after": "Wed, 21 Oct 2020 07:28:00 GMT",
		},
	}, nowMs)
	if res.retryAfterSeconds == nil || *res.retryAfterSeconds != 0 {
		t.Errorf("expected retryAfterSeconds = 0 for past RFC1123")
	}

	// statusCode omitted, but retry-after header present (numeric)
	res = parseOpencodeApiErrorInfo(map[string]interface{}{
		"message": "Quota exceeded",
		"responseHeaders": map[string]interface{}{
			"retry-after": "60",
		},
	}, nowMs)
	if !res.isRateLimit {
		t.Errorf("expected isRateLimit = true when retry-after header is present even without statusCode")
	}
	if res.retryAfterSeconds == nil || *res.retryAfterSeconds != 60 {
		t.Errorf("expected retryAfterSeconds = 60, got %v", res.retryAfterSeconds)
	}
	if res.statusCode != nil {
		t.Errorf("expected nil statusCode, got %v", res.statusCode)
	}

	// statusCode omitted, but retry-after header present (RFC1123)
	res = parseOpencodeApiErrorInfo(map[string]interface{}{
		"message": "Slow down",
		"responseHeaders": map[string]interface{}{
			"retry-after": "Wed, 21 Oct 2026 07:28:00 GMT",
		},
	}, nowMs)
	if !res.isRateLimit {
		t.Errorf("expected isRateLimit = true for RFC1123 retry-after header even without statusCode")
	}
	if res.rateLimitResetAt == nil {
		t.Errorf("expected rateLimitResetAt for RFC1123 header without statusCode")
	}

	// ISO8601/RFC3339 retry-after header correctly falls through to date parser
	// rather than scanning 2026 as numeric seconds
	isoDateStr := "2026-06-12T16:08:43Z"
	parsedIsoTime, err := time.Parse(time.RFC3339, isoDateStr)
	if err != nil {
		t.Fatalf("failed to parse test RFC3339 date: %v", err)
	}
	expectedResetAt := parsedIsoTime.UnixMilli()
	isoNowMs := expectedResetAt - 30000 // 30 seconds before reset

	res = parseOpencodeApiErrorInfo(map[string]interface{}{
		"status":  429,
		"message": "Too many requests",
		"responseHeaders": map[string]interface{}{
			"retry-after": isoDateStr,
		},
	}, isoNowMs)
	if !res.isRateLimit {
		t.Errorf("expected isRateLimit = true for ISO8601/RFC3339 retry-after")
	}
	if res.rateLimitResetAt == nil || *res.rateLimitResetAt != expectedResetAt {
		t.Errorf("expected rateLimitResetAt = %d, got %v", expectedResetAt, res.rateLimitResetAt)
	}
	if res.retryAfterSeconds == nil || *res.retryAfterSeconds != 30 {
		t.Errorf("expected retryAfterSeconds = 30, got %v (must not scan 2026 as numeric seconds)", res.retryAfterSeconds)
	}

	// retry-after in the past (RFC3339)
	res = parseOpencodeApiErrorInfo(map[string]interface{}{
		"status": 429,
		"responseHeaders": map[string]interface{}{
			"retry-after": "2020-01-01T00:00:00Z",
		},
	}, nowMs)
	if res.retryAfterSeconds == nil || *res.retryAfterSeconds != 0 {
		t.Errorf("expected retryAfterSeconds = 0 for past RFC3339")
	}

	// headers fallback in root map m
	res = parseOpencodeApiErrorInfo(map[string]interface{}{
		"message": "Too many requests",
		"headers": map[string]interface{}{
			"retry-after": "60",
		},
	}, nowMs)
	if !res.isRateLimit {
		t.Errorf("expected isRateLimit = true for headers fallback in root map")
	}
	if res.retryAfterSeconds == nil || *res.retryAfterSeconds != 60 {
		t.Errorf("expected retryAfterSeconds = 60, got %v", res.retryAfterSeconds)
	}

	// headers fallback in nested dataMap
	res = parseOpencodeApiErrorInfo(map[string]interface{}{
		"data": map[string]interface{}{
			"message": "Too many requests",
			"headers": map[string]interface{}{
				"retry-after": "120",
			},
		},
	}, nowMs)
	if !res.isRateLimit {
		t.Errorf("expected isRateLimit = true for headers fallback in dataMap")
	}
	if res.retryAfterSeconds == nil || *res.retryAfterSeconds != 120 {
		t.Errorf("expected retryAfterSeconds = 120, got %v", res.retryAfterSeconds)
	}

	// statusCode in root map m while dataMap exists
	res = parseOpencodeApiErrorInfo(map[string]interface{}{
		"statusCode": 429,
		"data": map[string]interface{}{
			"message": "Rate limited from upstream",
		},
	}, nowMs)
	if !res.isRateLimit {
		t.Errorf("expected isRateLimit = true when statusCode is in root map m and data map exists")
	}
	if res.statusCode == nil || *res.statusCode != 429 {
		t.Errorf("expected statusCode = 429 from root map m, got %v", res.statusCode)
	}

	// status in root map m while dataMap exists
	res = parseOpencodeApiErrorInfo(map[string]interface{}{
		"status": 429,
		"data": map[string]interface{}{
			"message": "Rate limited from upstream",
		},
	}, nowMs)
	if !res.isRateLimit {
		t.Errorf("expected isRateLimit = true when status is in root map m and data map exists")
	}
	if res.statusCode == nil || *res.statusCode != 429 {
		t.Errorf("expected statusCode = 429 from root map m, got %v", res.statusCode)
	}
}

func TestExtractOpencodeError(t *testing.T) {
	tests := []struct {
		name  string
		input interface{}
		want  string
	}{
		{"nil", nil, ""},
		{"empty_map", map[string]interface{}{}, ""},
		{"empty_nested_data", map[string]interface{}{"data": map[string]interface{}{}}, ""},
		{"string", "   network timeout   ", "network timeout"},
		{"error_interface", errors.New("underlying socket error"), "underlying socket error"},
		{
			"data_message",
			map[string]interface{}{
				"data": map[string]interface{}{
					"message": "authorization token expired",
				},
			},
			"authorization token expired",
		},
		{
			"data_exception",
			map[string]interface{}{
				"data": map[string]interface{}{
					"exception": "fatal: out of memory",
				},
			},
			"fatal: out of memory",
		},
		{
			"data_error",
			map[string]interface{}{
				"data": map[string]interface{}{
					"error": "nested inner error",
				},
			},
			"nested inner error",
		},
		{
			"data_string",
			map[string]interface{}{
				"data": "raw data string failure",
			},
			"raw data string failure",
		},
		{
			"message_field",
			map[string]interface{}{
				"message": "context deadline exceeded",
			},
			"context deadline exceeded",
		},
		{
			"error_field_recursive",
			map[string]interface{}{
				"error": map[string]interface{}{
					"message": "deep error message",
				},
			},
			"deep error message",
		},
		{
			"exception_field_recursive",
			map[string]interface{}{
				"exception": map[string]interface{}{
					"message": "exception payload text",
				},
			},
			"exception payload text",
		},
		{
			"text_field",
			map[string]interface{}{
				"text": "error block text content",
			},
			"error block text content",
		},
		{
			"details_field",
			map[string]interface{}{
				"details": "detailed stack trace information",
			},
			"detailed stack trace information",
		},
		{
			"name_only",
			map[string]interface{}{
				"name": "MessageOutputLengthError",
			},
			"MessageOutputLengthError",
		},
		{
			"json_marshal_fallback",
			map[string]interface{}{
				"code":    float64(503),
				"unknown": true,
			},
			`{"code":503,"unknown":true}`,
		},
		{
			"slice_of_errors",
			[]interface{}{
				"first failure",
				map[string]interface{}{"message": "second failure"},
			},
			"first failure\nsecond failure",
		},
		{
			"int_fallback",
			12345,
			"12345",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractOpencodeError(tt.input)
			if got != tt.want {
				t.Errorf("extractOpencodeError(%v) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsOpencodeMessageAborted(t *testing.T) {
	tests := []struct {
		name  string
		input interface{}
		want  bool
	}{
		{"nil", nil, false},
		{"string_match", "MessageAbortedError", true},
		{"string_contains", "Error occurred: MessageAbortedError", true},
		{"string_mismatch", "APIError", false},
		{"error_match", errors.New("MessageAbortedError: user cancelled"), true},
		{"error_mismatch", errors.New("APIError: something failed"), false},
		{
			"map_name_match",
			map[string]interface{}{
				"name": "MessageAbortedError",
				"data": map[string]interface{}{"message": "aborted by user"},
			},
			true,
		},
		{
			"map_message_match",
			map[string]interface{}{
				"message": "Operation cancelled: MessageAbortedError",
			},
			true,
		},
		{
			"nested_error_match",
			map[string]interface{}{
				"error": map[string]interface{}{
					"name": "MessageAbortedError",
				},
			},
			true,
		},
		{
			"nested_data_match",
			map[string]interface{}{
				"data": map[string]interface{}{
					"name": "MessageAbortedError",
				},
			},
			true,
		},
		{
			"unrelated_map",
			map[string]interface{}{
				"name": "APIError",
				"data": map[string]interface{}{"message": "rate limit"},
			},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isOpencodeMessageAborted(tt.input)
			if got != tt.want {
				t.Errorf("isOpencodeMessageAborted(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}




