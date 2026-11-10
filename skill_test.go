package parser

import (
	"reflect"
	"testing"
)

func skillsOf(events []ParsedEvent) []SkillInvocation {
	var skills []SkillInvocation
	for _, event := range events {
		skills = append(skills, event.Data.Skills...)
	}
	return skills
}

func TestSkillsFromShellCommand(t *testing.T) {
	tests := []struct {
		name   string
		script string
		want   []string
	}{
		{"brace group", "cat /x/skills/{programming,golang,programming}/SKILL.md", []string{"programming", "golang"}},
		{"nested braces", "cat /x/skills/{programming,{golang,rust}}/SKILL.md", []string{"programming", "golang", "rust"}},
		{"quoted braces", "cat '/x/skills/{go,rust}/SKILL.md'", []string{"{go,rust}"}},
		{"quoted space", "cat '/some dir/skills/golang/SKILL.md'", []string{"golang"}},
		{"dynamic command", "cat $(pwd)/skills/golang/SKILL.md", nil},
		{"dynamic variable", "cat $ROOT/skills/golang/SKILL.md", nil},
		{"invalid shell", "cat '/x/skills/golang/SKILL.md", nil},
		{"cat one skill", "cat /home/dev/.agents/skills/git-commit/SKILL.md", []string{"git-commit"}},
		{"cat several skills once each", "cat /a/skills/bun/SKILL.md /b/skills/typescript/SKILL.md /a/skills/bun/SKILL.md", []string{"bun", "typescript"}},
		{"sed range read", "sed -n '1,200p' '/home/dev/.agents/skills/docs-writer/SKILL.md'", []string{"docs-writer"}},
		{"read inside a pipeline", "git status --short && head -40 /x/skills/golang/SKILL.md | nl", []string{"golang"}},
		{"sed in-place edit", "sed -i '' 's/a/b/' /x/skills/golang/SKILL.md", nil},
		{"redirected write", "cat notes.md > /x/skills/golang/SKILL.md", nil},
		{"git output mentioning the file", "git diff -- /x/skills/golang/SKILL.md", nil},
		{"registry source outside a skills directory", "cat skills/user/disk-cleanup/SKILL.md", nil},
		{"other markdown", "cat /x/skills/golang/README.md", nil},
		{"first chunk of a paged read", "sed -n '1,200p' /x/skills/golang/SKILL.md", []string{"golang"}},
		{"continuation chunk", "sed -n '201,400p' /x/skills/golang/SKILL.md", nil},
		{"tail reads from the end", "tail -50 /x/skills/golang/SKILL.md", nil},
		{"piped continuation", "nl -ba /x/skills/golang/SKILL.md | sed -n '120,300p'", nil},
		{"piped tail", "cat /x/skills/golang/SKILL.md | tail -40", nil},
		{"piped first chunk", "nl -ba /x/skills/golang/SKILL.md | sed -n '1,120p'", []string{"golang"}},
		{"Codex system skill", "cat /home/dev/.codex/skills/.system/imagegen/SKILL.md", []string{"imagegen"}},
		{"synced skill", "cat /home/dev/.agents/skills/synced/abc123/computer-use/SKILL.md", []string{"computer-use"}},
		{"nested registry agent source", "cat skills/agent/github-issues/SKILL.md", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, skill := range skillsFromShellCommand(tt.script) {
				if skill.Source != SkillSourceModel {
					t.Errorf("skill %q source = %q, want model", skill.Name, skill.Source)
				}
				got = append(got, skill.Name)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("skillsFromShellCommand(%q) = %v, want %v", tt.script, got, tt.want)
			}
		})
	}
}

func TestClaudeCodeSkillInvocations(t *testing.T) {
	lines := []string{
		// Model loads a skill through the Skill tool.
		`{"type":"assistant","sessionId":"s1","timestamp":"2026-11-03T10:00:00Z","message":{"id":"m1","role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_1","name":"Skill","input":{"skill":"github-issues"}}]}}`,
		// Its skill content carries sourceToolUseID and must not be counted again.
		`{"type":"user","sessionId":"s1","timestamp":"2026-11-03T10:00:01Z","isMeta":true,"sourceToolUseID":"toolu_1","message":{"role":"user","content":[{"type":"text","text":"Base directory for this skill: /home/dev/.claude/skills/github-issues\n\nChoose the mode."}]}}`,
		// A built-in slash command has no skill content and is not a skill.
		`{"type":"user","sessionId":"s1","timestamp":"2026-11-03T10:01:00Z","message":{"role":"user","content":"<command-message>clear</command-message>\n<command-name>/clear</command-name>"}}`,
		`{"type":"user","sessionId":"s1","timestamp":"2026-11-03T10:01:01Z","message":{"role":"user","content":"what next?"}}`,
		// A user-invoked skill: the command, then its isMeta skill content.
		`{"type":"user","sessionId":"s1","timestamp":"2026-11-03T10:02:00Z","message":{"role":"user","content":"<command-message>workbench:deploy</command-message>\n<command-name>/workbench:deploy</command-name>"}}`,
		`{"type":"user","sessionId":"s1","timestamp":"2026-11-03T10:02:01Z","isMeta":true,"message":{"role":"user","content":[{"type":"text","text":"Base directory for this skill: /home/dev/.claude/plugins/workbench/skills/deploy\n\nCreate a new app."}]}}`,
	}
	p := NewClaudeCodeParser()
	var skills []SkillInvocation
	for _, line := range lines {
		events, err := p.ParseLine([]byte(line))
		if err != nil {
			t.Fatalf("ParseLine: %v", err)
		}
		skills = append(skills, skillsOf(events)...)
	}
	want := []SkillInvocation{
		{Name: "github-issues", Source: SkillSourceModel},
		{Name: "workbench:deploy", Source: SkillSourceUser, Path: "/home/dev/.claude/plugins/workbench/skills/deploy"},
	}
	if !reflect.DeepEqual(skills, want) {
		t.Errorf("skills = %+v, want %+v", skills, want)
	}
}

func TestClaudeCodeSkillContentWithoutCommandUsesDirectoryName(t *testing.T) {
	p := NewClaudeCodeParser()
	events, err := p.ParseLine([]byte(`{"type":"user","sessionId":"s1","timestamp":"2026-11-03T10:00:00Z","isMeta":true,"message":{"role":"user","content":[{"type":"text","text":"Base directory for this skill: /home/dev/.claude/skills/docs-writer"}]}}`))
	if err != nil {
		t.Fatalf("ParseLine: %v", err)
	}
	want := []SkillInvocation{{Name: "docs-writer", Source: SkillSourceUser, Path: "/home/dev/.claude/skills/docs-writer"}}
	if got := skillsOf(events); !reflect.DeepEqual(got, want) {
		t.Errorf("skills = %+v, want %+v", got, want)
	}
}

func TestClaudeCodeResetClearsPendingCommand(t *testing.T) {
	p := NewClaudeCodeParser()
	if _, err := p.ParseLine([]byte(`{"type":"user","sessionId":"s1","timestamp":"2026-11-03T10:00:00Z","message":{"role":"user","content":"<command-name>/review</command-name>"}}`)); err != nil {
		t.Fatalf("ParseLine: %v", err)
	}
	p.Reset()
	events, err := p.ParseLine([]byte(`{"type":"user","sessionId":"s1","timestamp":"2026-11-03T10:00:01Z","isMeta":true,"message":{"role":"user","content":[{"type":"text","text":"Base directory for this skill: /x/skills/code-review"}]}}`))
	if err != nil {
		t.Fatalf("ParseLine: %v", err)
	}
	if got := skillsOf(events); len(got) != 1 || got[0].Name != "code-review" {
		t.Errorf("skills after Reset = %+v, want the directory name", got)
	}
}

func TestCodexSkillInvocations(t *testing.T) {
	lines := []string{
		// $name mention: the completed UserMessage lists the skill.
		`{"timestamp":"2026-11-03T10:00:00Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u1","content":[{"type":"text","text":"$disk-cleanup"},{"type":"skill","name":"disk-cleanup","path":"/home/dev/.agents/skills/disk-cleanup/SKILL.md"}]}}}`,
		// The injected <skill> message is replayed into forked rollouts, so it is not counted.
		`{"timestamp":"2026-11-03T10:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<skill>\n<name>disk-cleanup</name>\n<path>/home/dev/.agents/skills/disk-cleanup/SKILL.md</path>\nSkill body"}]}}`,
		// Plain user messages are not skills.
		`{"timestamp":"2026-11-03T10:00:00Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u2","content":[{"type":"text","text":"hello"}]}}}`,
		// Model reads two skills in one successful command.
		`{"timestamp":"2026-11-03T10:00:01Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","command":["/bin/zsh","-lc","cat /home/dev/.agents/skills/bun/SKILL.md /home/dev/.agents/skills/typescript/SKILL.md"],"exit_code":0}}}`,
		// Failed reads and non-shell argv forms.
		`{"timestamp":"2026-11-03T10:00:02Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","command":["/bin/zsh","-lc","cat /home/dev/.agents/skills/missing/SKILL.md"],"exit_code":1}}}`,
		`{"timestamp":"2026-11-03T10:00:03Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","command":["head","/home/dev/.agents/skills/golang/SKILL.md"],"exit_code":0}}}`,
		`{"timestamp":"2026-11-03T10:00:04Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","command":["/bin/zsh","-lc","git status --short"],"exit_code":0}}}`,
		// A command the user ran with ! is not the model loading a skill.
		`{"timestamp":"2026-11-03T10:00:05Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","command":["/bin/zsh","-lc","cat /home/dev/.agents/skills/rust/SKILL.md"],"exit_code":0,"source":"user_shell"}}}`,
	}
	p := NewCodexParser()
	var skills []SkillInvocation
	var skillEvents int
	for _, line := range lines {
		events, err := p.ParseLine([]byte(line))
		if err != nil {
			t.Fatalf("ParseLine: %v", err)
		}
		for _, event := range events {
			if event.EventType == "skill_invoked" {
				skillEvents++
			}
		}
		skills = append(skills, skillsOf(events)...)
	}
	want := []SkillInvocation{
		{Name: "disk-cleanup", Source: SkillSourceUser, Path: "/home/dev/.agents/skills/disk-cleanup/SKILL.md"},
		{Name: "bun", Source: SkillSourceModel, Path: "/home/dev/.agents/skills/bun/SKILL.md"},
		{Name: "typescript", Source: SkillSourceModel, Path: "/home/dev/.agents/skills/typescript/SKILL.md"},
		{Name: "golang", Source: SkillSourceModel, Path: "/home/dev/.agents/skills/golang/SKILL.md"},
	}
	if !reflect.DeepEqual(skills, want) {
		t.Errorf("skills = %+v, want %+v", skills, want)
	}
	if skillEvents != 3 {
		t.Errorf("skill_invoked events = %d, want 3", skillEvents)
	}
}

func TestCodexForkedRolloutDoesNotRecountReplayedSkillMention(t *testing.T) {
	// A forked child replays the parent's response_item history but not its
	// item_completed records, so the parent's $name mention yields nothing here.
	replayed := []string{
		`{"timestamp":"2026-11-03T10:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"$disk-cleanup"}]}}`,
		`{"timestamp":"2026-11-03T10:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<skill>\n<name>disk-cleanup</name>\n<path>/home/dev/.agents/skills/disk-cleanup/SKILL.md</path>\nSkill body"}]}}`,
	}
	p := NewCodexParser()
	for _, line := range replayed {
		events, err := p.ParseLine([]byte(line))
		if err != nil {
			t.Fatalf("ParseLine: %v", err)
		}
		if skills := skillsOf(events); len(skills) != 0 {
			t.Errorf("replayed fork history produced skills %+v", skills)
		}
	}
}

func TestCodexUserMessageRejectsMalformedItem(t *testing.T) {
	_, err := ParseCodexLine([]byte(`{"timestamp":"2026-11-03T10:00:00Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","content":"not-an-array"}}}`))
	if err == nil {
		t.Fatal("expected an error for a malformed UserMessage item")
	}
}

func TestCodexCommandExecutionRejectsMalformedItem(t *testing.T) {
	_, err := ParseCodexLine([]byte(`{"timestamp":"2026-11-03T10:00:00Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","command":"not-an-array"}}}`))
	if err == nil {
		t.Fatal("expected an error for a malformed CommandExecution item")
	}
}

func TestPiSkillInvocations(t *testing.T) {
	lines := []string{
		`{"type":"message","timestamp":"2026-11-03T10:00:00Z","message":{"role":"user","content":[{"type":"text","text":"<skill name=\"github-issue\" location=\"/home/dev/.pi/agent/skills/github-issue/SKILL.md\">\nbody\n</skill>"}]}}`,
		`{"type":"message","timestamp":"2026-11-03T10:00:01Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"read","arguments":{"path":"/home/dev/.pi/agent/skills/ask-user/SKILL.md"}}]}}`,
		`{"type":"message","timestamp":"2026-11-03T10:00:02Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c2","name":"read","arguments":{"path":"/repo/README.md"}}]}}`,
		`{"type":"message","timestamp":"2026-11-03T10:00:03Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c3","name":"edit","arguments":{"path":"/home/dev/.pi/agent/skills/ask-user/SKILL.md"}}]}}`,
		// Paging past line 1 continues the load already counted.
		`{"type":"message","timestamp":"2026-11-03T10:00:04Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c4","name":"read","arguments":{"path":"/home/dev/.pi/agent/skills/ask-user/SKILL.md","offset":201,"limit":200}}]}}`,
		`{"type":"message","timestamp":"2026-11-03T10:00:05Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c5","name":"read","arguments":{"path":"/home/dev/.pi/agent/skills/golang/SKILL.md","offset":1}}]}}`,
	}
	var skills []SkillInvocation
	for _, line := range lines {
		events, err := ParsePiLine([]byte(line), "/repo")
		if err != nil {
			t.Fatalf("ParsePiLine: %v", err)
		}
		skills = append(skills, skillsOf(events)...)
	}
	want := []SkillInvocation{
		{Name: "github-issue", Source: SkillSourceUser, Path: "/home/dev/.pi/agent/skills/github-issue/SKILL.md"},
		{Name: "ask-user", Source: SkillSourceModel, Path: "/home/dev/.pi/agent/skills/ask-user/SKILL.md"},
		{Name: "golang", Source: SkillSourceModel, Path: "/home/dev/.pi/agent/skills/golang/SKILL.md"},
	}
	if !reflect.DeepEqual(skills, want) {
		t.Errorf("skills = %+v, want %+v", skills, want)
	}
}

func TestOpencodeSkillInvocations(t *testing.T) {
	session := &OpencodeSession{ID: "ses_1", Directory: "/repo"}
	messages := []OpencodeMessage{{ID: "msg_1", SessionID: "ses_1", TimeCreated: 1000, Data: OpencodeMessageData{Role: "assistant"}}}
	parts := []OpencodePart{
		{ID: "p1", MessageID: "msg_1", SessionID: "ses_1", TimeCreated: 1001, Data: OpencodePartData{Type: "tool", Tool: "skill", CallID: "call_1", State: &OpencodeToolPartState{Status: "completed", Input: map[string]interface{}{"name": "git-release"}}}},
		{ID: "p2", MessageID: "msg_1", SessionID: "ses_1", TimeCreated: 1002, Data: OpencodePartData{Type: "tool", Tool: "read", CallID: "call_2", State: &OpencodeToolPartState{Status: "completed", Input: map[string]interface{}{"filePath": "/repo/README.md"}}}},
	}
	want := []SkillInvocation{{Name: "git-release", Source: SkillSourceModel}}
	if got := skillsOf(ReconstructSessionEvents(session, messages, parts)); !reflect.DeepEqual(got, want) {
		t.Errorf("skills = %+v, want %+v", got, want)
	}
}
