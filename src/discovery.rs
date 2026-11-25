use crate::{Error, activity::activity_time, common::s};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::{
    collections::HashSet,
    ffi::OsString,
    fmt,
    fs::{self, File, Metadata},
    io::{self, BufRead, BufReader, Read},
    path::{Path, PathBuf},
    str::FromStr,
};
use walkdir::WalkDir;

#[path = "discovery_layouts.rs"]
mod layouts;

const MAX_LINE_BYTES: u64 = 256 << 20;

/// A harness whose stored sessions this crate can discover.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum Harness {
    Agy,
    ClaudeCode,
    Codex,
    Opencode,
    Pi,
}

impl Harness {
    pub const ALL: [Self; 5] = [
        Self::ClaudeCode,
        Self::Codex,
        Self::Pi,
        Self::Opencode,
        Self::Agy,
    ];

    pub const fn as_str(self) -> &'static str {
        match self {
            Self::Agy => "agy",
            Self::ClaudeCode => "claude-code",
            Self::Codex => "codex",
            Self::Opencode => "opencode",
            Self::Pi => "pi",
        }
    }
}

impl fmt::Display for Harness {
    fn fmt(&self, output: &mut fmt::Formatter<'_>) -> fmt::Result {
        output.write_str(self.as_str())
    }
}

impl FromStr for Harness {
    type Err = Error;

    fn from_str(name: &str) -> Result<Self, Self::Err> {
        Self::ALL
            .into_iter()
            .find(|harness| harness.as_str() == name)
            .ok_or_else(|| Error::Invalid(format!("unknown harness {name:?}")))
    }
}

/// A transcript, or an OpenCode session within a shared SQLite database.
/// Timestamps are in Unix milliseconds.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct SessionSource {
    pub harness: Harness,
    pub session_id: String,
    pub parent_session_id: String,
    pub path: PathBuf,
    pub cwd: PathBuf,
    pub git_branch: String,
    pub subagent: bool,
    pub is_fork: bool,
    /// Parent history at or before this timestamp may be replayed in the fork.
    pub forked_at: Option<i64>,
    pub archived: bool,
    pub modified_at: i64,
}

impl SessionSource {
    fn new(harness: Harness, session_id: &str, path: &Path, modified_at: i64) -> Self {
        Self {
            harness,
            session_id: session_id.into(),
            parent_session_id: String::new(),
            path: path.into(),
            cwd: PathBuf::new(),
            git_branch: String::new(),
            subagent: false,
            is_fork: false,
            forked_at: None,
            archived: false,
            modified_at,
        }
    }
}

#[derive(Clone, Debug, Default)]
pub struct DiscoverOptions {
    /// Empty selects the current user's home directory for standard locations.
    pub home: PathBuf,
    /// Empty scans all harnesses.
    pub harnesses: Vec<Harness>,
}

/// Readable sessions and independent discovery errors. Errors retain partial results.
#[derive(Debug, Default)]
pub struct Discovery {
    pub sources: Vec<SessionSource>,
    pub errors: Vec<Error>,
}

/// Discover stored sessions without modifying their files or databases.
pub fn discover_sessions(options: &DiscoverOptions) -> Discovery {
    discover_sessions_until(options, || false)
}

/// Discover stored sessions, returning sorted partial results when cancellation is requested.
pub fn discover_sessions_until(
    options: &DiscoverOptions,
    is_canceled: impl Fn() -> bool,
) -> Discovery {
    discover(options, |key| std::env::var_os(key), &is_canceled)
}

fn discover(
    options: &DiscoverOptions,
    env: impl Fn(&str) -> Option<OsString>,
    is_canceled: &dyn Fn() -> bool,
) -> Discovery {
    let mut found = Discovery::default();
    let home = if options.home.as_os_str().is_empty() {
        match std::env::home_dir() {
            Some(path) => path,
            None => {
                found
                    .errors
                    .push(Error::Invalid("could not resolve home directory".into()));
                return found;
            }
        }
    } else {
        options.home.clone()
    };

    let configured = |name| env(name).filter(|v| !v.is_empty()).map(PathBuf::from);
    let harnesses = if options.harnesses.is_empty() {
        &Harness::ALL[..]
    } else {
        &options.harnesses
    };
    for &harness in harnesses {
        if is_canceled() {
            break;
        }
        match harness {
            Harness::ClaudeCode => {
                for root in
                    unique_dirs([configured("CLAUDE_CONFIG_DIR"), Some(home.join(".claude"))])
                {
                    let root = root.join("projects");
                    walk(
                        &root,
                        &[".jsonl"],
                        is_canceled,
                        &mut found,
                        layouts::claude_source,
                    );
                }
            }
            Harness::Codex => {
                let root = configured("CODEX_HOME").unwrap_or_else(|| home.join(".codex"));
                let current =
                    configured("CODEX_SESSIONS_DIR").unwrap_or_else(|| root.join("sessions"));
                for (root, archived) in [(current, false), (root.join("archived_sessions"), true)] {
                    walk(
                        &root,
                        &[".jsonl", ".jsonl.zst"],
                        is_canceled,
                        &mut found,
                        |path, parts, modified, found| {
                            layouts::codex_source(path, parts, modified, found, archived);
                        },
                    );
                }
            }
            Harness::Pi => {
                let path = configured("PI_SESSIONS_DIR").unwrap_or_else(|| {
                    configured("PI_CODING_AGENT_DIR")
                        .unwrap_or_else(|| home.join(".pi/agent"))
                        .join("sessions")
                });
                walk(
                    &path,
                    &[".jsonl"],
                    is_canceled,
                    &mut found,
                    layouts::pi_source,
                );
            }
            Harness::Opencode => {
                let data = configured("XDG_DATA_HOME")
                    .unwrap_or_else(|| home.join(".local/share"))
                    .join("opencode");
                let paths = layouts::opencode_paths(
                    configured("OPENCODE_DB_PATH"),
                    configured("OPENCODE_DB"),
                    &data,
                    &mut found,
                );
                for path in paths.into_iter().take_while(|_| !is_canceled()) {
                    layouts::opencode_sources(&path, &mut found);
                }
            }
            Harness::Agy => {
                let roots = if let Some(path) = configured("AGY_SESSIONS_DIR") {
                    vec![path]
                } else {
                    unique_dirs([
                        configured("GEMINI_HOME").map(|path| path.join("antigravity-cli/brain")),
                        Some(home.join(".gemini/antigravity-cli/brain")),
                    ])
                };
                for root in roots.into_iter().take_while(|_| !is_canceled()) {
                    layouts::agy_sources(&root, is_canceled, &mut found);
                }
            }
        }
    }
    found.sources.sort_by(|a, b| {
        (a.harness, &a.path, &a.session_id).cmp(&(b.harness, &b.path, &b.session_id))
    });
    if is_canceled() {
        found.errors.push(Error::Io(io::Error::new(
            io::ErrorKind::Interrupted,
            "session discovery canceled",
        )));
    }
    found
}

/// Open a plain or zstd-compressed transcript with buffered, streaming reads.
pub fn open_transcript(path: &Path) -> Result<Box<dyn BufRead>, Error> {
    let file = BufReader::new(File::open(path)?);
    if path.extension().is_some_and(|ext| ext == "zst") {
        let mut decoder = zstd::stream::read::Decoder::with_buffer(file)?;
        decoder.window_log_max(28)?;
        Ok(Box::new(BufReader::new(decoder)))
    } else {
        Ok(Box::new(file))
    }
}

fn unique_dirs(paths: [Option<PathBuf>; 2]) -> Vec<PathBuf> {
    let mut seen = HashSet::new();
    paths
        .into_iter()
        .flatten()
        .filter(|path| seen.insert(fs::canonicalize(path).unwrap_or_else(|_| path.clone())))
        .collect()
}

fn walk(
    root: &Path,
    suffixes: &[&str],
    is_canceled: &dyn Fn() -> bool,
    found: &mut Discovery,
    mut visit: impl FnMut(&Path, &[&str], i64, &mut Discovery),
) {
    let resolved = match fs::canonicalize(root) {
        Ok(path) => path,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return,
        Err(error) => {
            found.report(root, error);
            return;
        }
    };
    for entry in WalkDir::new(&resolved)
        .into_iter()
        .take_while(|_| !is_canceled())
    {
        let entry = match entry {
            Ok(entry) => entry,
            Err(error) => {
                found.report(root, error);
                continue;
            }
        };
        let Some(name) = entry.file_name().to_str() else {
            continue;
        };
        if !entry.file_type().is_file() || !suffixes.iter().any(|suffix| name.ends_with(suffix)) {
            continue;
        }
        let Ok(relative) = entry.path().strip_prefix(&resolved) else {
            continue;
        };
        let parts = relative
            .components()
            .map(|component| component.as_os_str().to_str())
            .collect::<Option<Vec<_>>>();
        let Some(parts) = parts else { continue };
        let path = root.join(relative);
        match entry.metadata() {
            Ok(meta) => visit(&path, &parts, modified_at(&meta), found),
            Err(error) => found.report(&path, error),
        }
    }
}

fn modified_at(meta: &Metadata) -> i64 {
    meta.modified()
        .map(|time| chrono::DateTime::<chrono::Utc>::from(time).timestamp_millis())
        .unwrap_or_default()
}

impl Discovery {
    fn report(&mut self, path: &Path, error: impl fmt::Display) {
        self.errors.push(Error::Invalid(format!(
            "discovering {}: {error}",
            path.display()
        )));
    }
}

fn bounded_line(reader: &mut impl BufRead, line: &mut Vec<u8>) -> Result<bool, Error> {
    line.clear();
    let size = reader.take(MAX_LINE_BYTES + 1).read_until(b'\n', line)?;
    if size as u64 > MAX_LINE_BYTES {
        return Err(Error::Invalid("transcript line exceeds 256 MiB".into()));
    }
    Ok(size != 0)
}

fn pi_header(path: &Path) -> Result<Value, Error> {
    let mut reader = BufReader::new(File::open(path)?);
    let mut line = Vec::new();
    bounded_line(&mut reader, &mut line)?;
    let header: Value = serde_json::from_slice(&line)?;
    if s(&header, "type") != "session" || s(&header, "id").is_empty() {
        return Err(Error::Invalid(
            "Pi transcript does not start with a session header".into(),
        ));
    }
    Ok(header)
}
