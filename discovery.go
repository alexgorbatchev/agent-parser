package parser

import (
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

// SessionSource is one session stored on disk. Discovery reads directory entries and
// OpenCode's session table only; it never parses transcript contents.
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
	// Fork marks a Pi fork transcript, which replays its parent's history before its own.
	Fork bool `json:"fork,omitempty"`
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
	slices.SortFunc(sources, func(a, b SessionSource) int {
		return strings.Compare(string(a.Harness)+"\x00"+a.Path+"\x00"+a.SessionID, string(b.Harness)+"\x00"+b.Path+"\x00"+b.SessionID)
	})
	return sources, errors.Join(errs...)
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
		err := walkTranscripts(ctx, filepath.Join(configDir, "projects"), []string{".jsonl"}, func(path string, rel []string, info fs.FileInfo) error {
			if source, ok := claudeCodeSource(path, rel, info); ok {
				sources = append(sources, source)
			}
			return nil
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
		return source, true, nil
	case len(rel) == 5 && strings.HasPrefix(rel[3], "run-") && rel[4] == "session.jsonl":
		// <cwd>/<ts>_<parent>/<run>/run-<n>/session.jsonl: the ID lives in the session header.
		parent, parentOK := piIDFromName(rel[1])
		if !parentOK {
			return SessionSource{}, false, nil
		}
		id, err := piHeaderSessionID(path)
		if err != nil {
			return SessionSource{}, false, err
		}
		source.SessionID, source.ParentSessionID, source.Subagent = id, parent, true
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

// piHeaderSessionID reads the session ID from the first record of a Pi transcript.
func piHeaderSessionID(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("reading Pi session header: %w", err)
	}
	defer func() { _ = file.Close() }()
	var header struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if err := json.NewDecoder(file).Decode(&header); err != nil {
		return "", fmt.Errorf("reading Pi session header %s: %w", path, err)
	}
	if header.Type != "session" || header.ID == "" {
		return "", fmt.Errorf("pi transcript %s does not start with a session header", path)
	}
	return header.ID, nil
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
