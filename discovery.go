package parser

import (
	"context"
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

// AllHarnesses lists every supported harness in a stable order.
var AllHarnesses = []Harness{HarnessClaudeCode, HarnessCodex, HarnessPi, HarnessOpencode}

// SessionSource is one session stored on disk. Discovery reads directory entries and
// OpenCode's session table only; it never parses transcript contents.
type SessionSource struct {
	Harness Harness `json:"harness"`
	// SessionID is the harness's session identifier. For a Claude Code subagent it is the
	// subagent transcript's name, such as "agent-a1b2".
	SessionID string `json:"sessionId"`
	// ParentSessionID is the parent session of a subagent or fork, when the layout records it.
	ParentSessionID string `json:"parentSessionId,omitempty"`
	// Path is the transcript file, or the OpenCode database holding the session. Read
	// transcripts with OpenTranscript, which decompresses .zst files.
	Path string `json:"path"`
	// Subagent marks a Claude Code subagent transcript or an OpenCode child session.
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
	// Harnesses limits discovery; empty means AllHarnesses.
	Harnesses []Harness
	// Home replaces the user's home directory as the root of default locations.
	Home string
}

// DiscoverSessions lists the sessions stored by each selected harness, read-only. It uses
// the same locations and environment overrides as the harnesses themselves:
//
//   - Claude Code: $CLAUDE_CONFIG_DIR/projects and ~/.claude/projects, including subagents.
//   - Codex: $CODEX_SESSIONS_DIR, or $CODEX_HOME/sessions, or ~/.codex/sessions, plus the
//     sibling archived_sessions. Rollouts may be plain .jsonl or zstd-compressed .jsonl.zst.
//   - Pi: $PI_SESSIONS_DIR, or $PI_CODING_AGENT_DIR/sessions, or ~/.pi/agent/sessions.
//   - OpenCode: $OPENCODE_DB_PATH, or $XDG_DATA_HOME/opencode/opencode.db, or
//     ~/.local/share/opencode/opencode.db, with one source per session row.
//
// Missing locations are skipped. Unreadable directories or databases do not stop the scan:
// the sessions found are returned with the joined errors. Results are ordered by harness,
// then path, then session ID.
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
		harnesses = AllHarnesses
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
// suffixes. A missing root yields nothing; unreadable directories are reported and skipped.
func walkTranscripts(ctx context.Context, root string, suffixes []string, visit func(path string, info fs.FileInfo)) error {
	if _, err := os.Stat(root); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	var errs []error
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
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
		visit(path, info)
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
		err := walkTranscripts(ctx, filepath.Join(configDir, "projects"), []string{".jsonl"}, func(path string, info fs.FileInfo) {
			source := SessionSource{
				Harness:   HarnessClaudeCode,
				SessionID: strings.TrimSuffix(info.Name(), ".jsonl"),
				Path:      path,
				ModTime:   info.ModTime(),
			}
			// Subagent transcripts live at projects/<project>/<session>/subagents/<agent>.jsonl.
			if dir := filepath.Dir(path); filepath.Base(dir) == "subagents" {
				source.Subagent = true
				source.ParentSessionID = filepath.Base(filepath.Dir(dir))
			}
			sources = append(sources, source)
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	return sources, errors.Join(errs...)
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
		err := walkTranscripts(ctx, root.dir, []string{".jsonl", ".jsonl.zst"}, func(path string, info fs.FileInfo) {
			if !strings.HasPrefix(info.Name(), "rollout-") {
				return
			}
			rollout, err := ParseCodexRolloutPath(strings.TrimSuffix(path, ".zst"))
			if err != nil {
				return
			}
			sources = append(sources, SessionSource{
				Harness:   HarnessCodex,
				SessionID: rollout.SessionID,
				Path:      path,
				Archived:  root.archived,
				ModTime:   info.ModTime(),
			})
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
	err := walkTranscripts(ctx, sessionsDir, []string{".jsonl"}, func(path string, info fs.FileInfo) {
		// Pi names transcripts <timestamp>_<session-id>.jsonl.
		name := strings.TrimSuffix(info.Name(), ".jsonl")
		sessionID := name
		if _, id, ok := strings.Cut(name, "_"); ok && id != "" {
			sessionID = id
		}
		source := SessionSource{Harness: HarnessPi, SessionID: sessionID, Path: path, ModTime: info.ModTime()}
		// Forks live at <cwd>/<timestamp>_<parent-id>/forks/<timestamp>_<fork-id>.jsonl.
		if dir := filepath.Dir(path); filepath.Base(dir) == "forks" {
			source.Fork = true
			if _, parentID, ok := strings.Cut(filepath.Base(filepath.Dir(dir)), "_"); ok {
				source.ParentSessionID = parentID
			}
		}
		sources = append(sources, source)
	})
	return sources, err
}

func discoverOpencode(ctx context.Context, home string) ([]SessionSource, error) {
	dbPath := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
	if custom := os.Getenv("OPENCODE_DB_PATH"); custom != "" {
		dbPath = custom
	} else if xdgData := os.Getenv("XDG_DATA_HOME"); xdgData != "" {
		dbPath = filepath.Join(xdgData, "opencode", "opencode.db")
	}
	if _, err := os.Stat(dbPath); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
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
