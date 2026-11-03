package parser

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// Harness identifies an agent harness whose sessions this package parses.
type Harness string

// maxTranscriptLineBytes bounds one transcript line read during discovery; tool outputs
// embedded in a line can be tens of megabytes.
const maxTranscriptLineBytes = 256 << 20

// Supported harnesses.
const (
	HarnessClaudeCode Harness = "claude-code"
	HarnessCodex      Harness = "codex"
	HarnessPi         Harness = "pi"
	HarnessOpencode   Harness = "opencode"
)

// SupportedHarnesses returns every supported harness in a stable order.
func SupportedHarnesses() []Harness {
	return []Harness{HarnessClaudeCode, HarnessCodex, HarnessPi, HarnessOpencode}
}

// SessionSource is one session stored on disk. Discovery reads directory entries, OpenCode's
// session table, the first record of each Pi subagent run and fork, Claude Code subagent
// .meta.json files, and, for a Claude Code fork, the parent record holding its spawning tool
// call; it does not parse any other transcript contents.
type SessionSource struct {
	Harness Harness `json:"harness"`
	// SessionID is the harness's session identifier. For a Claude Code subagent it is the
	// subagent transcript's name, such as "agent-a1b2"; for a Pi subagent run it is the ID in
	// the run's session header.
	SessionID string `json:"sessionId"`
	// ParentSessionID is the parent session of a subagent or fork, when the layout records it.
	ParentSessionID string `json:"parentSessionId,omitempty"`
	// Path is the transcript file, or the OpenCode database holding the session. Read
	// transcripts with OpenTranscript, which decompresses .zst files.
	Path string `json:"path"`
	// Subagent marks a Claude Code subagent transcript, a Pi subagent run, or an OpenCode
	// child session.
	Subagent bool `json:"subagent,omitempty"`
	// Fork marks a Claude Code fork subagent or a Pi fork, whose transcript can replay its
	// parent's history before its own entries.
	Fork bool `json:"fork,omitempty"`
	// ForkedAt is when a fork was created: the parent's spawning tool call for a Claude Code
	// fork, and the session header for a Pi fork. Events before it are replayed parent
	// history. It is zero when the session is not a fork or the time could not be read.
	ForkedAt time.Time `json:"forkedAt,omitzero"`
	// Archived marks a Codex rollout moved to archived_sessions.
	Archived bool `json:"archived,omitempty"`
	// ModTime is the transcript's modification time, or OpenCode's session update time.
	ModTime time.Time `json:"modTime"`
}

// DiscoverOptions selects what DiscoverSessions scans.
type DiscoverOptions struct {
	// Harnesses limits discovery; empty means SupportedHarnesses.
	Harnesses []Harness
	// Home replaces the user's home directory as the root of default locations.
	Home string
}

// DiscoverSessions lists the sessions stored by each selected harness, read-only. It uses
// the same locations and environment overrides as the harnesses themselves:
//
//   - Claude Code: $CLAUDE_CONFIG_DIR/projects and ~/.claude/projects. Sessions are
//     <project>/<session>.jsonl; subagents are agent-*.jsonl anywhere under
//     <project>/<session>/subagents/ (including workflows/<run>/) or, in the legacy layout,
//     directly under <project>/. Other files, such as workflow journal.jsonl, are skipped.
//   - Codex: $CODEX_SESSIONS_DIR, or $CODEX_HOME/sessions, or ~/.codex/sessions, plus the
//     sibling archived_sessions. Rollouts may be plain .jsonl or zstd-compressed .jsonl.zst.
//   - Pi: $PI_SESSIONS_DIR, or $PI_CODING_AGENT_DIR/sessions, or ~/.pi/agent/sessions.
//     Sessions are <cwd>/<ts>_<id>.jsonl, forks <cwd>/<ts>_<parent>/forks/<ts>_<id>.jsonl,
//     and subagent runs <cwd>/<ts>_<parent>/<run>/run-<n>/session.jsonl, whose ID comes from
//     the run's session header. Other files are skipped.
//   - OpenCode: $OPENCODE_DB_PATH; otherwise OpenCode's own $OPENCODE_DB (absolute, or
//     relative to the data directory), or every opencode.db and opencode-<channel>.db in
//     ${XDG_DATA_HOME:-~/.local/share}/opencode, with one source per session row.
//
// Root directories may be symlinks; returned paths keep the configured root. Missing
// locations are skipped. Unreadable directories, transcripts, or databases do not stop the
// scan: the sessions found are returned with the joined errors. Results are ordered by
// harness, then path, then session ID.
func DiscoverSessions(ctx context.Context, opts DiscoverOptions) ([]SessionSource, error) {
	home := opts.Home
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return nil, fmt.Errorf("resolving home directory: %w", err)
		}
	}
	harnesses := opts.Harnesses
	if len(harnesses) == 0 {
		harnesses = SupportedHarnesses()
	}

	var sources []SessionSource
	var errs []error
	for _, harness := range harnesses {
		if err := ctx.Err(); err != nil {
			sortSessionSources(sources)
			return sources, err
		}
		var found []SessionSource
		var err error
		switch harness {
		case HarnessClaudeCode:
			found, err = discoverClaudeCode(ctx, home)
		case HarnessCodex:
			found, err = discoverCodex(ctx, home)
		case HarnessPi:
			found, err = discoverPi(ctx, home)
		case HarnessOpencode:
			found, err = discoverOpencode(ctx, home)
		default:
			err = fmt.Errorf("unknown harness %q", harness)
		}
		sources = append(sources, found...)
		if err != nil {
			errs = append(errs, fmt.Errorf("discovering %s sessions: %w", harness, err))
		}
	}
	sortSessionSources(sources)
	return sources, errors.Join(errs...)
}

func sortSessionSources(sources []SessionSource) {
	slices.SortFunc(sources, func(a, b SessionSource) int {
		return strings.Compare(string(a.Harness)+"\x00"+a.Path+"\x00"+a.SessionID, string(b.Harness)+"\x00"+b.Path+"\x00"+b.SessionID)
	})
}

// OpenTranscript opens a transcript for reading, decompressing zstd-compressed .zst files.
// Closing the returned reader closes the file.
func OpenTranscript(path string) (io.ReadCloser, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening transcript: %w", err)
	}
	if !strings.HasSuffix(path, ".zst") {
		return file, nil
	}
	decoder, err := zstd.NewReader(file)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("opening zstd transcript %s: %w", path, err)
	}
	return &zstdTranscript{decoder: decoder, file: file}, nil
}

type zstdTranscript struct {
	decoder *zstd.Decoder
	file    *os.File
}

func (t *zstdTranscript) Read(p []byte) (int, error) { return t.decoder.Read(p) }

func (t *zstdTranscript) Close() error {
	t.decoder.Close()
	return t.file.Close()
}

// walkTranscripts calls visit for each regular file under root whose name has one of the
// suffixes, with its path under the configured root and its parts relative to the root. A
// symlinked root is followed. A missing root yields nothing; unreadable directories are
// reported and skipped.
func walkTranscripts(ctx context.Context, root string, suffixes []string, visit func(path string, rel []string, info fs.FileInfo) error) error {
	resolved, err := filepath.EvalSymlinks(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	walkErr := filepath.WalkDir(resolved, func(walked string, entry fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			errs = append(errs, err)
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || !hasAnySuffix(entry.Name(), suffixes) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			errs = append(errs, err)
			return nil
		}
		relPath, err := filepath.Rel(resolved, walked)
		if err != nil {
			errs = append(errs, err)
			return nil
		}
		if err := visit(filepath.Join(root, relPath), strings.Split(filepath.ToSlash(relPath), "/"), info); err != nil {
			errs = append(errs, err)
		}
		return nil
	})
	if walkErr != nil {
		errs = append(errs, walkErr)
	}
	return errors.Join(errs...)
}

func hasAnySuffix(name string, suffixes []string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// uniqueDirs drops repeated directories, comparing them after resolving symlinks.
func uniqueDirs(dirs []string) []string {
	seen := map[string]bool{}
	var unique []string
	for _, dir := range dirs {
		key := dir
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			key = resolved
		}
		if !seen[key] {
			seen[key] = true
			unique = append(unique, dir)
		}
	}
	return unique
}

func discoverClaudeCode(ctx context.Context, home string) ([]SessionSource, error) {
	var configDirs []string
	if custom := os.Getenv("CLAUDE_CONFIG_DIR"); custom != "" {
		configDirs = append(configDirs, custom)
	}
	configDirs = append(configDirs, filepath.Join(home, ".claude"))

	var sources []SessionSource
	var errs []error
	for _, configDir := range uniqueDirs(configDirs) {
		projects := filepath.Join(configDir, "projects")
		err := walkTranscripts(ctx, projects, []string{".jsonl"}, func(path string, rel []string, info fs.FileInfo) error {
			source, ok := claudeCodeSource(path, rel, info)
			if !ok {
				return nil
			}
			var err error
			if source.Subagent && len(rel) >= 4 {
				err = markClaudeCodeFork(&source, filepath.Join(projects, rel[0], rel[1]+".jsonl"))
			}
			sources = append(sources, source)
			return err
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	return sources, errors.Join(errs...)
}

// claudeCodeSource classifies a .jsonl file by its position under the projects directory.
// rel is [project, ...].
func claudeCodeSource(path string, rel []string, info fs.FileInfo) (SessionSource, bool) {
	name := strings.TrimSuffix(info.Name(), ".jsonl")
	source := SessionSource{Harness: HarnessClaudeCode, SessionID: name, Path: path, ModTime: info.ModTime()}
	isAgent := strings.HasPrefix(name, "agent-")
	switch {
	case len(rel) == 2:
		// <project>/<session>.jsonl, or a legacy <project>/agent-<id>.jsonl subagent.
		source.Subagent = isAgent
		return source, true
	case len(rel) >= 4 && rel[2] == "subagents" && isAgent:
		// <project>/<session>/subagents/[workflows/<run>/]agent-<id>.jsonl
		source.Subagent = true
		source.ParentSessionID = rel[1]
		return source, true
	default:
		return SessionSource{}, false
	}
}

// claudeCodeForkMeta is the part of a subagent's agent-<id>.meta.json that identifies a fork.
type claudeCodeForkMeta struct {
	IsFork        bool   `json:"isFork"`
	ToolUseID     string `json:"toolUseId"`
	ParentAgentID string `json:"parentAgentId"`
}

// markClaudeCodeFork reads the subagent's meta file and, for a fork, sets Fork and
// ForkedAt from the parent's tool call that spawned it. The parent is the subagent named
// by parentAgentId in the same directory, or the session transcript. A subagent without a
// meta file is not a fork.
func markClaudeCodeFork(source *SessionSource, sessionTranscript string) error {
	metaPath := strings.TrimSuffix(source.Path, ".jsonl") + ".meta.json"
	data, err := os.ReadFile(metaPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading Claude Code subagent meta: %w", err)
	}
	var meta claudeCodeForkMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return fmt.Errorf("reading Claude Code subagent meta %s: %w", metaPath, err)
	}
	if !meta.IsFork {
		return nil
	}
	source.Fork = true
	parent := sessionTranscript
	if meta.ParentAgentID != "" {
		parent = filepath.Join(filepath.Dir(source.Path), "agent-"+meta.ParentAgentID+".jsonl")
	}
	forkedAt, err := toolCallTime(parent, meta.ToolUseID)
	if err != nil {
		return fmt.Errorf("finding when Claude Code fork %s was spawned: %w", source.Path, err)
	}
	source.ForkedAt = forkedAt
	return nil
}

// toolCallTime returns the timestamp of the transcript record holding the tool call with
// the given ID.
func toolCallTime(transcript, toolUseID string) (time.Time, error) {
	if toolUseID == "" {
		return time.Time{}, errors.New("the fork names no spawning tool call")
	}
	file, err := os.Open(transcript)
	if err != nil {
		return time.Time{}, err
	}
	defer func() { _ = file.Close() }()
	needle := []byte(`"id":"` + toolUseID + `"`)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<20), maxTranscriptLineBytes)
	for scanner.Scan() {
		if !bytes.Contains(scanner.Bytes(), needle) {
			continue
		}
		var record struct {
			Timestamp string `json:"timestamp"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return time.Time{}, fmt.Errorf("reading %s: %w", transcript, err)
		}
		return time.Parse(time.RFC3339Nano, record.Timestamp)
	}
	if err := scanner.Err(); err != nil {
		return time.Time{}, fmt.Errorf("reading %s: %w", transcript, err)
	}
	return time.Time{}, fmt.Errorf("tool call %s not found in %s", toolUseID, transcript)
}

func discoverCodex(ctx context.Context, home string) ([]SessionSource, error) {
	codexHome := filepath.Join(home, ".codex")
	if custom := os.Getenv("CODEX_HOME"); custom != "" {
		codexHome = custom
	}
	sessionsDir := filepath.Join(codexHome, "sessions")
	if custom := os.Getenv("CODEX_SESSIONS_DIR"); custom != "" {
		sessionsDir = custom
	}

	var sources []SessionSource
	var errs []error
	for _, root := range []struct {
		dir      string
		archived bool
	}{{sessionsDir, false}, {filepath.Join(codexHome, "archived_sessions"), true}} {
		err := walkTranscripts(ctx, root.dir, []string{".jsonl", ".jsonl.zst"}, func(path string, _ []string, info fs.FileInfo) error {
			if !strings.HasPrefix(info.Name(), "rollout-") {
				return nil
			}
			rollout, err := ParseCodexRolloutPath(strings.TrimSuffix(path, ".zst"))
			if err != nil {
				return nil
			}
			sources = append(sources, SessionSource{
				Harness:   HarnessCodex,
				SessionID: rollout.SessionID,
				Path:      path,
				Archived:  root.archived,
				ModTime:   info.ModTime(),
			})
			return nil
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	return sources, errors.Join(errs...)
}

func discoverPi(ctx context.Context, home string) ([]SessionSource, error) {
	sessionsDir := filepath.Join(home, ".pi", "agent", "sessions")
	if custom := os.Getenv("PI_CODING_AGENT_DIR"); custom != "" {
		sessionsDir = filepath.Join(custom, "sessions")
	}
	if custom := os.Getenv("PI_SESSIONS_DIR"); custom != "" {
		sessionsDir = custom
	}

	var sources []SessionSource
	err := walkTranscripts(ctx, sessionsDir, []string{".jsonl"}, func(path string, rel []string, info fs.FileInfo) error {
		source, ok, err := piSource(path, rel, info)
		if ok {
			sources = append(sources, source)
		}
		return err
	})
	return sources, err
}

// piSource classifies a .jsonl file by its position under the sessions directory.
// rel is [cwd, ...]; Pi names transcripts <timestamp>_<session-id>.jsonl.
func piSource(path string, rel []string, info fs.FileInfo) (SessionSource, bool, error) {
	source := SessionSource{Harness: HarnessPi, Path: path, ModTime: info.ModTime()}
	switch {
	case len(rel) == 2:
		// <cwd>/<ts>_<id>.jsonl
		id, ok := piIDFromName(rel[1])
		source.SessionID = id
		return source, ok, nil
	case len(rel) == 4 && rel[2] == "forks":
		// <cwd>/<ts>_<parent>/forks/<ts>_<id>.jsonl
		id, ok := piIDFromName(rel[3])
		parent, parentOK := piIDFromName(rel[1])
		if !ok || !parentOK {
			return SessionSource{}, false, nil
		}
		source.SessionID, source.ParentSessionID, source.Fork = id, parent, true
		header, err := readPiHeader(path)
		if err != nil {
			return source, true, err
		}
		source.ForkedAt = header.Timestamp
		return source, true, nil
	case len(rel) == 5 && strings.HasPrefix(rel[3], "run-") && rel[4] == "session.jsonl":
		// <cwd>/<ts>_<parent>/<run>/run-<n>/session.jsonl: the ID lives in the session header.
		parent, parentOK := piIDFromName(rel[1])
		if !parentOK {
			return SessionSource{}, false, nil
		}
		header, err := readPiHeader(path)
		if err != nil {
			return SessionSource{}, false, err
		}
		source.SessionID, source.ParentSessionID, source.Subagent = header.ID, parent, true
		return source, true, nil
	default:
		return SessionSource{}, false, nil
	}
}

// piIDFromName returns the session ID from a <timestamp>_<session-id>[.jsonl] name.
func piIDFromName(name string) (string, bool) {
	_, id, ok := strings.Cut(strings.TrimSuffix(name, ".jsonl"), "_")
	return id, ok && id != ""
}

// readPiHeader reads the first record of a Pi transcript: its session ID and creation time.
func readPiHeader(path string) (piHeader, error) {
	file, err := os.Open(path)
	if err != nil {
		return piHeader{}, fmt.Errorf("reading Pi session header: %w", err)
	}
	defer func() { _ = file.Close() }()
	var header piHeader
	if err := json.NewDecoder(file).Decode(&header); err != nil {
		return piHeader{}, fmt.Errorf("reading Pi session header %s: %w", path, err)
	}
	if header.Type != "session" || header.ID == "" {
		return piHeader{}, fmt.Errorf("pi transcript %s does not start with a session header", path)
	}
	return header, nil
}

// piHeader is the first record of a Pi transcript.
type piHeader struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
}

func discoverOpencode(ctx context.Context, home string) ([]SessionSource, error) {
	paths, err := opencodeDBPaths(home)
	if err != nil {
		return nil, err
	}
	var sources []SessionSource
	var errs []error
	for _, dbPath := range paths {
		found, err := opencodeDBSources(ctx, dbPath)
		sources = append(sources, found...)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", dbPath, err))
		}
	}
	return sources, errors.Join(errs...)
}

// opencodeDBPaths returns the OpenCode databases to read. OPENCODE_DB_PATH (agent-watcher's
// override) wins. Otherwise OpenCode's own OPENCODE_DB names the database, resolved against
// the data directory the way OpenCode resolves it; without it, every opencode.db and
// channel opencode-<channel>.db in the data directory is read.
func opencodeDBPaths(home string) ([]string, error) {
	if custom := os.Getenv("OPENCODE_DB_PATH"); custom != "" {
		return existingFiles([]string{custom}), nil
	}
	dataDir := filepath.Join(home, ".local", "share", "opencode")
	if xdgData := os.Getenv("XDG_DATA_HOME"); xdgData != "" {
		dataDir = filepath.Join(xdgData, "opencode")
	}
	if name := os.Getenv("OPENCODE_DB"); name != "" {
		if name == ":memory:" {
			return nil, nil
		}
		if !filepath.IsAbs(name) {
			name = filepath.Join(dataDir, name)
		}
		return existingFiles([]string{name}), nil
	}
	channels, err := filepath.Glob(filepath.Join(dataDir, "opencode-*.db"))
	if err != nil {
		return nil, err
	}
	return existingFiles(append([]string{filepath.Join(dataDir, "opencode.db")}, channels...)), nil
}

func existingFiles(paths []string) []string {
	var existing []string
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			existing = append(existing, path)
		}
	}
	return existing
}

func opencodeDBSources(ctx context.Context, dbPath string) ([]SessionSource, error) {
	db, err := OpenOpencodeDB(dbPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	sessions, err := db.GetSessions(ctx)
	if err != nil {
		return nil, err
	}
	sources := make([]SessionSource, 0, len(sessions))
	for _, session := range sessions {
		source := SessionSource{
			Harness:   HarnessOpencode,
			SessionID: session.ID,
			Path:      dbPath,
			Subagent:  session.IsSubagent,
			ModTime:   time.UnixMilli(session.TimeUpdated),
		}
		if session.ParentID != nil {
			source.ParentSessionID = *session.ParentID
		}
		sources = append(sources, source)
	}
	return sources, nil
}
