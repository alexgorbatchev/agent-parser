package parser

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

// isolateHarnessEnv clears every harness location override so discovery reads only the
// test's fixture home.
func isolateHarnessEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"CLAUDE_CONFIG_DIR", "CODEX_HOME", "CODEX_SESSIONS_DIR", "PI_CODING_AGENT_DIR",
		"PI_SESSIONS_DIR", "OPENCODE_DB_PATH", "OPENCODE_DB", "XDG_DATA_HOME",
	} {
		t.Setenv(key, "")
	}
}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func writeZstdFixture(t *testing.T, path, content string) {
	t.Helper()
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatalf("creating zstd encoder: %v", err)
	}
	defer func() { _ = encoder.Close() }()
	writeFixture(t, path, string(encoder.EncodeAll([]byte(content), nil)))
}

func createOpencodeFixtureDB(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(path), err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("opening sqlite: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(opencodeTestSchema); err != nil {
		t.Fatalf("creating schema: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO session (id, parent_id, directory, time_created, time_updated) VALUES
		('ses_root', NULL, '/repo', 1000, 1762200000000),
		('ses_child', 'ses_root', '/repo', 1100, 1762200001000)`); err != nil {
		t.Fatalf("inserting sessions: %v", err)
	}
}

func buildHarnessHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	writeFixture(t, filepath.Join(home, ".claude/projects/-repo/11111111-aaaa.jsonl"), "{}\n")
	writeFixture(t, filepath.Join(home, ".claude/projects/-repo/11111111-aaaa/subagents/agent-a1.jsonl"), "{}\n")
	writeFixture(t, filepath.Join(home, ".claude/projects/-repo/notes.txt"), "not a transcript\n")
	writeFixture(t, filepath.Join(home, ".codex/sessions/2026/11/03/rollout-2026-11-03T10-00-00-01a1aaaa-0000-7000-8000-000000000001.jsonl"), "{}\n")
	writeZstdFixture(t, filepath.Join(home, ".codex/sessions/2026/10/01/rollout-2026-10-01T10-00-00-01a1aaaa-0000-7000-8000-000000000002.jsonl.zst"), "{}\n")
	writeZstdFixture(t, filepath.Join(home, ".codex/archived_sessions/rollout-2026-09-29T12-43-05-01a0eeb1-34c4-7f92-94c3-b9040da8692d.jsonl.zst"), "{}\n")
	writeFixture(t, filepath.Join(home, ".codex/sessions/2026/11/03/history.jsonl"), "{}\n")
	writeFixture(t, filepath.Join(home, ".pi/agent/sessions/--repo--/2026-09-20T03-02-11-996Z_01a0bcc3-parent.jsonl"), "{}\n")
	writeFixture(t, filepath.Join(home, ".pi/agent/sessions/--repo--/2026-09-20T03-02-11-996Z_01a0bcc3-parent/forks/2026-09-20T03-09-01-836Z_01a0bcc9-fork.jsonl"), "{}\n")
	createOpencodeFixtureDB(t, filepath.Join(home, ".local/share/opencode/opencode.db"))
	return home
}

func TestDiscoverSessionsAllHarnesses(t *testing.T) {
	isolateHarnessEnv(t)
	home := buildHarnessHome(t)

	sources, err := DiscoverSessions(context.Background(), DiscoverOptions{Home: home})
	if err != nil {
		t.Fatalf("DiscoverSessions: %v", err)
	}

	type summary struct {
		harness   Harness
		sessionID string
		parent    string
		subagent  bool
		fork      bool
		archived  bool
	}
	var got []summary
	for _, source := range sources {
		got = append(got, summary{source.Harness, source.SessionID, source.ParentSessionID, source.Subagent, source.Fork, source.Archived})
		if source.ModTime.IsZero() {
			t.Errorf("%s %s has no modification time", source.Harness, source.SessionID)
		}
	}
	want := []summary{
		{HarnessClaudeCode, "11111111-aaaa", "", false, false, false},
		{HarnessClaudeCode, "agent-a1", "11111111-aaaa", true, false, false},
		{HarnessCodex, "01a0eeb1-34c4-7f92-94c3-b9040da8692d", "", false, false, true},
		{HarnessCodex, "01a1aaaa-0000-7000-8000-000000000002", "", false, false, false},
		{HarnessCodex, "01a1aaaa-0000-7000-8000-000000000001", "", false, false, false},
		{HarnessOpencode, "ses_child", "ses_root", true, false, false},
		{HarnessOpencode, "ses_root", "", false, false, false},
		{HarnessPi, "01a0bcc3-parent", "", false, false, false},
		{HarnessPi, "01a0bcc9-fork", "01a0bcc3-parent", false, true, false},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d sources %+v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("source %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	for _, source := range sources {
		if source.Harness == HarnessOpencode && source.SessionID == "ses_root" && !source.ModTime.Equal(time.UnixMilli(1762200000000)) {
			t.Errorf("OpenCode ModTime = %v, want the session's time_updated", source.ModTime)
		}
	}
}

func TestDiscoverSessionsFiltersHarnesses(t *testing.T) {
	isolateHarnessEnv(t)
	home := buildHarnessHome(t)

	sources, err := DiscoverSessions(context.Background(), DiscoverOptions{Home: home, Harnesses: []Harness{HarnessPi}})
	if err != nil {
		t.Fatalf("DiscoverSessions: %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("got %d sources, want the 2 Pi transcripts: %+v", len(sources), sources)
	}
	for _, source := range sources {
		if source.Harness != HarnessPi {
			t.Errorf("unexpected harness %s", source.Harness)
		}
	}
}

func TestDiscoverSessionsHonorsEnvironmentOverrides(t *testing.T) {
	isolateHarnessEnv(t)
	home := t.TempDir()
	custom := t.TempDir()
	writeFixture(t, filepath.Join(custom, "claude/projects/-repo/claude-custom.jsonl"), "{}\n")
	writeFixture(t, filepath.Join(custom, "codex-sessions/rollout-2026-11-03T10-00-00-01a1bbbb-0000-7000-8000-000000000003.jsonl"), "{}\n")
	writeFixture(t, filepath.Join(custom, "codex-home/archived_sessions/rollout-2026-11-03T10-00-00-01a1bbbb-0000-7000-8000-000000000004.jsonl"), "{}\n")
	writeFixture(t, filepath.Join(custom, "pi-sessions/--repo--/2026-11-03T10-00-00-000Z_pi-custom.jsonl"), "{}\n")
	createOpencodeFixtureDB(t, filepath.Join(custom, "xdg/opencode/opencode.db"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(custom, "claude"))
	t.Setenv("CODEX_HOME", filepath.Join(custom, "codex-home"))
	t.Setenv("CODEX_SESSIONS_DIR", filepath.Join(custom, "codex-sessions"))
	t.Setenv("PI_SESSIONS_DIR", filepath.Join(custom, "pi-sessions"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(custom, "xdg"))

	sources, err := DiscoverSessions(context.Background(), DiscoverOptions{Home: home})
	if err != nil {
		t.Fatalf("DiscoverSessions: %v", err)
	}
	ids := map[string]bool{}
	for _, source := range sources {
		ids[source.SessionID] = true
	}
	for _, id := range []string{"claude-custom", "01a1bbbb-0000-7000-8000-000000000003", "01a1bbbb-0000-7000-8000-000000000004", "pi-custom", "ses_root"} {
		if !ids[id] {
			t.Errorf("session %s not discovered from its override location; got %v", id, ids)
		}
	}
}

func TestDiscoverSessionsOverridePrecedence(t *testing.T) {
	isolateHarnessEnv(t)
	home := t.TempDir()
	custom := t.TempDir()
	writeFixture(t, filepath.Join(custom, "pi-agent/sessions/--repo--/2026-11-03T10-00-00-000Z_pi-agent-dir.jsonl"), "{}\n")
	createOpencodeFixtureDB(t, filepath.Join(custom, "explicit.db"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(custom, "pi-agent"))
	t.Setenv("OPENCODE_DB_PATH", filepath.Join(custom, "explicit.db"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(custom, "unused-xdg"))

	sources, err := DiscoverSessions(context.Background(), DiscoverOptions{Home: home, Harnesses: []Harness{HarnessPi, HarnessOpencode}})
	if err != nil {
		t.Fatalf("DiscoverSessions: %v", err)
	}
	var piFound, opencodeFound bool
	for _, source := range sources {
		piFound = piFound || source.SessionID == "pi-agent-dir"
		opencodeFound = opencodeFound || (source.Harness == HarnessOpencode && source.Path == filepath.Join(custom, "explicit.db"))
	}
	if !piFound || !opencodeFound {
		t.Errorf("PI_CODING_AGENT_DIR found=%v, OPENCODE_DB_PATH found=%v: %+v", piFound, opencodeFound, sources)
	}
}

func TestDiscoverSessionsSkipsMissingLocations(t *testing.T) {
	isolateHarnessEnv(t)
	sources, err := DiscoverSessions(context.Background(), DiscoverOptions{Home: t.TempDir()})
	if err != nil || len(sources) != 0 {
		t.Fatalf("DiscoverSessions on an empty home = %+v, %v; want nothing and no error", sources, err)
	}
}

func TestDiscoverSessionsReportsErrorsAndKeepsResults(t *testing.T) {
	isolateHarnessEnv(t)
	home := buildHarnessHome(t)
	// A corrupt OpenCode database fails that harness without hiding the others.
	writeFixture(t, filepath.Join(home, ".local/share/opencode/opencode.db"), "not a database")

	sources, err := DiscoverSessions(context.Background(), DiscoverOptions{Home: home, Harnesses: []Harness{HarnessPi, HarnessOpencode, Harness("unknown")}})
	if err == nil {
		t.Fatal("expected errors for the corrupt database and the unknown harness")
	}
	for _, want := range []string{"discovering opencode sessions", `unknown harness "unknown"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if len(sources) != 2 {
		t.Errorf("got %d sources, want the 2 Pi transcripts despite the errors", len(sources))
	}
}

func TestDiscoverSessionsReportsUnreadableDirectories(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions are not enforced here")
	}
	isolateHarnessEnv(t)
	home := t.TempDir()
	writeFixture(t, filepath.Join(home, ".pi/agent/sessions/--readable--/2026-11-03T10-00-00-000Z_ok.jsonl"), "{}\n")
	locked := filepath.Join(home, ".pi/agent/sessions/--locked--")
	writeFixture(t, filepath.Join(locked, "2026-11-03T10-00-00-000Z_hidden.jsonl"), "{}\n")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	sources, err := DiscoverSessions(context.Background(), DiscoverOptions{Home: home, Harnesses: []Harness{HarnessPi}})
	if err == nil {
		t.Fatal("expected an error for the unreadable directory")
	}
	if len(sources) != 1 || sources[0].SessionID != "ok" {
		t.Errorf("sources = %+v, want only the readable transcript", sources)
	}
}

func TestDiscoverSessionsStopsOnCanceledContext(t *testing.T) {
	isolateHarnessEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := DiscoverSessions(ctx, DiscoverOptions{Home: buildHarnessHome(t)})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestOpenTranscriptDecompressesZstd(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.jsonl")
	compressed := filepath.Join(dir, "rollout.jsonl.zst")
	writeFixture(t, plain, "{\"a\":1}\n")
	writeZstdFixture(t, compressed, "{\"b\":2}\n")

	for path, want := range map[string]string{plain: "{\"a\":1}\n", compressed: "{\"b\":2}\n"} {
		reader, err := OpenTranscript(path)
		if err != nil {
			t.Fatalf("OpenTranscript(%s): %v", path, err)
		}
		got, err := io.ReadAll(reader)
		if closeErr := reader.Close(); closeErr != nil {
			t.Errorf("Close(%s): %v", path, closeErr)
		}
		if err != nil || string(got) != want {
			t.Errorf("OpenTranscript(%s) read %q, %v; want %q", path, got, err, want)
		}
	}
}

func TestOpenTranscriptErrors(t *testing.T) {
	if _, err := OpenTranscript(filepath.Join(t.TempDir(), "missing.jsonl")); err == nil {
		t.Error("expected an error for a missing transcript")
	}
	corrupt := filepath.Join(t.TempDir(), "corrupt.jsonl.zst")
	writeFixture(t, corrupt, "not zstd")
	reader, err := OpenTranscript(corrupt)
	if err != nil {
		return
	}
	defer func() { _ = reader.Close() }()
	if _, err := io.ReadAll(reader); err == nil {
		t.Error("expected an error reading a corrupt zstd transcript")
	}
}

func TestDiscoverClaudeCodeSubagentLayouts(t *testing.T) {
	isolateHarnessEnv(t)
	home := t.TempDir()
	projects := filepath.Join(home, ".claude/projects/-repo")
	writeFixture(t, filepath.Join(projects, "sess-1.jsonl"), "{}\n")
	writeFixture(t, filepath.Join(projects, "sess-1/subagents/workflows/wf_1/agent-w1.jsonl"), "{}\n")
	writeFixture(t, filepath.Join(projects, "sess-1/subagents/workflows/wf_1/journal.jsonl"), `{"type":"launched"}`+"\n")
	writeFixture(t, filepath.Join(projects, "sess-1/tool-results/output.jsonl"), "{}\n")
	writeFixture(t, filepath.Join(projects, "agent-legacy.jsonl"), "{}\n")

	sources, err := DiscoverSessions(context.Background(), DiscoverOptions{Home: home, Harnesses: []Harness{HarnessClaudeCode}})
	if err != nil {
		t.Fatalf("DiscoverSessions: %v", err)
	}
	got := map[string]SessionSource{}
	for _, source := range sources {
		got[source.SessionID] = source
	}
	if len(got) != 3 {
		t.Fatalf("got %+v, want the session, the workflow subagent, and the legacy subagent", sources)
	}
	if s := got["sess-1"]; s.Subagent {
		t.Errorf("session marked as subagent: %+v", s)
	}
	if s := got["agent-w1"]; !s.Subagent || s.ParentSessionID != "sess-1" {
		t.Errorf("workflow subagent = %+v, want a subagent of sess-1", s)
	}
	if s := got["agent-legacy"]; !s.Subagent || s.ParentSessionID != "" {
		t.Errorf("legacy subagent = %+v, want a subagent without a known parent", s)
	}
}

func TestDiscoverPiSubagentRuns(t *testing.T) {
	isolateHarnessEnv(t)
	home := t.TempDir()
	cwd := filepath.Join(home, ".pi/agent/sessions/--repo--")
	writeFixture(t, filepath.Join(cwd, "2026-09-22T17-47-27-324Z_parent-1.jsonl"), "{}\n")
	writeFixture(t, filepath.Join(cwd, "2026-09-22T17-47-27-324Z_parent-1/run-uuid/run-0/session.jsonl"),
		`{"type":"session","version":3,"id":"child-run-1","cwd":"/repo"}`+"\n")
	writeFixture(t, filepath.Join(cwd, "2026-09-22T17-47-27-324Z_parent-1/notes/extra.jsonl"), "{}\n")
	writeFixture(t, filepath.Join(cwd, "untimestamped.jsonl"), "{}\n")

	sources, err := DiscoverSessions(context.Background(), DiscoverOptions{Home: home, Harnesses: []Harness{HarnessPi}})
	if err != nil {
		t.Fatalf("DiscoverSessions: %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("got %+v, want the session and its subagent run", sources)
	}
	run := sources[1]
	if sources[0].SessionID != "parent-1" || run.SessionID != "child-run-1" || run.ParentSessionID != "parent-1" || !run.Subagent {
		t.Errorf("sources = %+v", sources)
	}
}

func TestDiscoverPiSubagentRunWithoutHeaderIsReported(t *testing.T) {
	isolateHarnessEnv(t)
	home := t.TempDir()
	cwd := filepath.Join(home, ".pi/agent/sessions/--repo--")
	writeFixture(t, filepath.Join(cwd, "2026-09-22T17-47-27-324Z_parent-1.jsonl"), "{}\n")
	writeFixture(t, filepath.Join(cwd, "2026-09-22T17-47-27-324Z_parent-1/run-a/run-0/session.jsonl"), `{"type":"message"}`+"\n")
	writeFixture(t, filepath.Join(cwd, "2026-09-22T17-47-27-324Z_parent-1/run-b/run-0/session.jsonl"), "not json")

	sources, err := DiscoverSessions(context.Background(), DiscoverOptions{Home: home, Harnesses: []Harness{HarnessPi}})
	if err == nil || !strings.Contains(err.Error(), "session header") {
		t.Fatalf("err = %v, want session header errors", err)
	}
	if len(sources) != 1 || sources[0].SessionID != "parent-1" {
		t.Errorf("sources = %+v, want only the parent session", sources)
	}
}

func TestDiscoverSessionsFollowsSymlinkedRoot(t *testing.T) {
	isolateHarnessEnv(t)
	home := t.TempDir()
	target := t.TempDir()
	writeFixture(t, filepath.Join(target, "--repo--/2026-11-03T10-00-00-000Z_linked.jsonl"), "{}\n")
	if err := os.MkdirAll(filepath.Join(home, ".pi/agent"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(home, ".pi/agent/sessions")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	sources, err := DiscoverSessions(context.Background(), DiscoverOptions{Home: home, Harnesses: []Harness{HarnessPi}})
	if err != nil {
		t.Fatalf("DiscoverSessions: %v", err)
	}
	want := filepath.Join(home, ".pi/agent/sessions/--repo--/2026-11-03T10-00-00-000Z_linked.jsonl")
	if len(sources) != 1 || sources[0].SessionID != "linked" || sources[0].Path != want {
		t.Errorf("sources = %+v, want one session at %s", sources, want)
	}
}

func TestDiscoverOpencodeDatabaseSelection(t *testing.T) {
	dbPaths := func(t *testing.T, home string) []string {
		t.Helper()
		sources, err := DiscoverSessions(context.Background(), DiscoverOptions{Home: home, Harnesses: []Harness{HarnessOpencode}})
		if err != nil {
			t.Fatalf("DiscoverSessions: %v", err)
		}
		seen := map[string]bool{}
		var paths []string
		for _, source := range sources {
			if !seen[source.Path] {
				seen[source.Path] = true
				paths = append(paths, source.Path)
			}
		}
		return paths
	}
	newHome := func(t *testing.T) (string, string) {
		home := t.TempDir()
		dataDir := filepath.Join(home, ".local/share/opencode")
		createOpencodeFixtureDB(t, filepath.Join(dataDir, "opencode.db"))
		createOpencodeFixtureDB(t, filepath.Join(dataDir, "opencode-local.db"))
		return home, dataDir
	}

	t.Run("default and channel databases", func(t *testing.T) {
		isolateHarnessEnv(t)
		t.Setenv("OPENCODE_DB", "")
		home, dataDir := newHome(t)
		got := dbPaths(t, home)
		want := []string{filepath.Join(dataDir, "opencode-local.db"), filepath.Join(dataDir, "opencode.db")}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("databases = %v, want %v", got, want)
		}
	})
	t.Run("relative OPENCODE_DB resolves in the data directory", func(t *testing.T) {
		isolateHarnessEnv(t)
		home, dataDir := newHome(t)
		createOpencodeFixtureDB(t, filepath.Join(dataDir, "custom.db"))
		t.Setenv("OPENCODE_DB", "custom.db")
		if got := dbPaths(t, home); !reflect.DeepEqual(got, []string{filepath.Join(dataDir, "custom.db")}) {
			t.Errorf("databases = %v", got)
		}
	})
	t.Run("absolute OPENCODE_DB", func(t *testing.T) {
		isolateHarnessEnv(t)
		home, _ := newHome(t)
		elsewhere := filepath.Join(t.TempDir(), "oc.db")
		createOpencodeFixtureDB(t, elsewhere)
		t.Setenv("OPENCODE_DB", elsewhere)
		if got := dbPaths(t, home); !reflect.DeepEqual(got, []string{elsewhere}) {
			t.Errorf("databases = %v", got)
		}
	})
	t.Run("in-memory OPENCODE_DB has nothing on disk", func(t *testing.T) {
		isolateHarnessEnv(t)
		home, _ := newHome(t)
		t.Setenv("OPENCODE_DB", ":memory:")
		if got := dbPaths(t, home); len(got) != 0 {
			t.Errorf("databases = %v, want none", got)
		}
	})
}
