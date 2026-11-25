use super::{
    Discovery, Harness, SessionSource, activity_time, bounded_line, modified_at, pi_header,
};
use crate::{Error, OpencodeDb, common::s, parse_codex_rollout_path, read_agy_summaries};
use serde::Deserialize;
use serde_json::Value;
use std::{
    collections::HashMap,
    fs::{self, File},
    io::{self, BufReader},
    path::{Path, PathBuf},
};

pub(super) fn claude_source(path: &Path, parts: &[&str], modified_at: i64, found: &mut Discovery) {
    let Some(name) = parts.last().and_then(|name| name.strip_suffix(".jsonl")) else {
        return;
    };
    let is_agent = name.starts_with("agent-");
    let mut source = SessionSource::new(Harness::ClaudeCode, name, path, modified_at);
    match parts {
        [_, _] => source.subagent = is_agent,
        [_, parent, "subagents", .., _] if is_agent => {
            source.subagent = true;
            source.parent_session_id = (*parent).into();
            if let Err(error) = mark_claude_fork(&mut source, parts.len() - 2) {
                found.report(path, error);
            }
        }
        _ => return,
    }
    found.sources.push(source);
}

#[derive(Default, Deserialize)]
#[serde(default, rename_all = "camelCase")]
struct ClaudeFork {
    is_fork: bool,
    tool_use_id: String,
    parent_agent_id: String,
}

fn mark_claude_fork(source: &mut SessionSource, parent_levels: usize) -> Result<(), Error> {
    let meta: ClaudeFork = serde_json::from_reader(BufReader::new(File::open(
        source.path.with_extension("meta.json"),
    )?))?;
    source.is_fork = meta.is_fork;
    if !meta.is_fork {
        return Ok(());
    }
    if meta.tool_use_id.is_empty() {
        return Err(Error::Invalid(
            "the fork names no spawning tool call".into(),
        ));
    }
    let session_dir = source
        .path
        .ancestors()
        .nth(parent_levels)
        .ok_or_else(|| Error::Invalid("missing Claude parent session directory".into()))?;
    let parent = if meta.parent_agent_id.is_empty() {
        session_dir.with_file_name(format!("{}.jsonl", source.parent_session_id))
    } else {
        if meta.parent_agent_id.contains(['/', '\\']) {
            return Err(Error::Invalid("invalid Claude parent agent ID".into()));
        }
        let name = format!("agent-{}.jsonl", meta.parent_agent_id);
        let alongside = source.path.with_file_name(&name);
        if alongside.is_file() {
            alongside
        } else {
            session_dir.join("subagents").join(name)
        }
    };
    source.forked_at = Some(claude_tool_time(&parent, &meta.tool_use_id)?);
    Ok(())
}

fn claude_tool_time(path: &Path, id: &str) -> Result<i64, Error> {
    let mut reader = BufReader::new(File::open(path)?);
    let mut line = Vec::new();
    let id_json = serde_json::to_string(id)?;
    while bounded_line(&mut reader, &mut line)? {
        if !line
            .windows(id_json.len())
            .any(|window| window == id_json.as_bytes())
        {
            continue;
        }
        let raw: Value = serde_json::from_slice(&line)?;
        let contains_call = raw["message"]["content"]
            .as_array()
            .into_iter()
            .flatten()
            .any(|part| s(part, "id") == id && s(part, "type") == "tool_use");
        if contains_call {
            return activity_time(&raw["timestamp"])?
                .ok_or_else(|| Error::Invalid("spawning tool call has no timestamp".into()));
        }
    }
    Err(Error::Invalid(format!(
        "spawning tool call {id} was not found in {}",
        path.display()
    )))
}

pub(super) fn codex_source(
    path: &Path,
    parts: &[&str],
    modified_at: i64,
    found: &mut Discovery,
    archived: bool,
) {
    let Some(name) = parts.last().filter(|name| name.starts_with("rollout-")) else {
        return;
    };
    let name = name.strip_suffix(".zst").unwrap_or(name);
    if let Ok(rollout) = parse_codex_rollout_path(name) {
        let mut source = SessionSource::new(Harness::Codex, &rollout.session_id, path, modified_at);
        source.archived = archived;
        found.sources.push(source);
    }
}

pub(super) fn pi_source(path: &Path, parts: &[&str], modified_at: i64, found: &mut Discovery) {
    let (parent, name, fork, subagent) = match parts {
        [_, name] => (None, Some(*name), false, false),
        [_, parent, "forks", name] => (Some(*parent), Some(*name), true, false),
        [_, parent, _, run, "session.jsonl"] if run.starts_with("run-") => {
            (Some(*parent), None, false, true)
        }
        _ => return,
    };
    let mut source = SessionSource::new(Harness::Pi, "", path, modified_at);
    source.is_fork = fork;
    source.subagent = subagent;
    if let Some(parent) = parent {
        let Some(parent) = pi_name_id(parent) else {
            return;
        };
        source.parent_session_id = parent.into();
    }
    if let Some(name) = name {
        let Some(id) = pi_name_id(name) else { return };
        source.session_id = id.into();
    }
    if subagent || fork {
        let header = match pi_header(path) {
            Ok(header) => header,
            Err(error) => {
                found.report(path, error);
                if fork {
                    found.sources.push(source);
                }
                return;
            }
        };
        source.cwd = s(&header, "cwd").into();
        if subagent {
            source.session_id = s(&header, "id").into();
        } else {
            match activity_time(&header["timestamp"]) {
                Ok(Some(at)) => source.forked_at = Some(at),
                Ok(None) => found.report(path, "Pi fork has no session header timestamp"),
                Err(error) => found.report(path, error),
            }
        }
    }
    found.sources.push(source);
}

fn pi_name_id(name: &str) -> Option<&str> {
    let name = name.strip_suffix(".jsonl").unwrap_or(name);
    let (_, id) = name.split_once('_')?;
    (!id.is_empty()).then_some(id)
}

pub(super) fn opencode_paths(
    explicit: Option<PathBuf>,
    native: Option<PathBuf>,
    data: &Path,
    found: &mut Discovery,
) -> Vec<PathBuf> {
    if let Some(path) = explicit {
        return path.is_file().then_some(path).into_iter().collect();
    }
    if let Some(path) = native {
        if path.as_os_str() == ":memory:" {
            return vec![];
        }
        let path = data.join(path);
        return path.is_file().then_some(path).into_iter().collect();
    }
    entries(data, found)
        .into_iter()
        .map(|entry| entry.path())
        .filter(|path| {
            path.file_name()
                .and_then(|name| name.to_str())
                .is_some_and(|name| {
                    name == "opencode.db"
                        || (name.starts_with("opencode-") && name.ends_with(".db"))
                })
                && path.is_file()
        })
        .collect()
}

pub(super) fn opencode_sources(path: &Path, found: &mut Discovery) {
    match OpencodeDb::open(path).and_then(|database| database.get_sessions()) {
        Ok(sessions) => found.sources.extend(sessions.into_iter().map(|session| {
            let mut source =
                SessionSource::new(Harness::Opencode, &session.id, path, session.time_updated);
            source.parent_session_id = session.parent_id.unwrap_or_default();
            source.cwd = session.directory.into();
            source.subagent = session.is_subagent;
            source
        })),
        Err(error) => found.report(path, error),
    }
}

pub(super) fn agy_sources(root: &Path, is_canceled: &dyn Fn() -> bool, found: &mut Discovery) {
    let resolved = match fs::canonicalize(root) {
        Ok(path) => path,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return,
        Err(error) => {
            found.report(root, error);
            return;
        }
    };
    let conversations = entries(&resolved, found);
    let mut parents = resolved
        .parent()
        .and_then(|parent| read_agy_summaries(parent.join("conversation_summaries.db")).ok())
        .into_iter()
        .flatten()
        .filter_map(|(id, summary)| {
            (!summary.parent_conversation_id.is_empty())
                .then_some((id, summary.parent_conversation_id))
        })
        .collect::<HashMap<_, _>>();
    for entry in conversations
        .iter()
        .take_while(|_| !is_canceled())
        .filter(|entry| is_directory(entry))
    {
        let subagents = entry.path().join(".system_generated/subagents");
        let Ok(children) = fs::read_dir(subagents) else {
            continue;
        };
        let parent = entry.file_name();
        for child in children.flatten().filter(|child| !is_directory(child)) {
            if let Some(id) = child
                .file_name()
                .to_str()
                .and_then(|name| name.strip_suffix(".json"))
                .filter(|id| !id.is_empty())
                && let Some(parent) = parent.to_str()
            {
                parents.insert(id.into(), parent.into());
            }
        }
    }
    for entry in conversations
        .into_iter()
        .take_while(|_| !is_canceled())
        .filter(is_directory)
    {
        let id = entry.file_name();
        let Some(id) = id.to_str() else { continue };
        let logs = root.join(id).join(".system_generated/logs");
        let Some((path, meta)) = ["transcript_full.jsonl", "transcript.jsonl"]
            .into_iter()
            .find_map(|name| {
                let path = logs.join(name);
                fs::metadata(&path)
                    .ok()
                    .filter(|meta| meta.is_file())
                    .map(|meta| (path, meta))
            })
        else {
            continue;
        };
        let mut source = SessionSource::new(Harness::Agy, id, &path, modified_at(&meta));
        if let Some(parent) = parents.get(id) {
            source.parent_session_id = parent.clone();
            source.subagent = true;
        }
        found.sources.push(source);
    }
}

fn is_directory(entry: &fs::DirEntry) -> bool {
    entry.file_type().is_ok_and(|kind| kind.is_dir())
}

fn entries(path: &Path, found: &mut Discovery) -> Vec<fs::DirEntry> {
    match fs::read_dir(path) {
        Ok(entries) => entries
            .filter_map(|entry| match entry {
                Ok(entry) => Some(entry),
                Err(error) => {
                    found.report(path, error);
                    None
                }
            })
            .collect(),
        Err(error) => {
            if error.kind() != io::ErrorKind::NotFound {
                found.report(path, error);
            }
            vec![]
        }
    }
}
