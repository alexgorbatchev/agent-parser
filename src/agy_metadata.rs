use crate::Error;
use prost::Message;
use rusqlite::{Connection, OpenFlags, types::ValueRef};
use std::{
    collections::HashMap,
    path::{Component, Path, PathBuf},
    time::Duration,
};

/// Native conversation index metadata. Times are Unix milliseconds when present.
#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct AgyConversationSummary {
    pub conversation_id: String,
    pub title: String,
    pub status: String,
    pub parent_conversation_id: String,
    pub workspace_paths: Vec<PathBuf>,
    pub last_modified: Option<i64>,
    pub not_fully_idle: bool,
    pub killed: bool,
}

/// Read a consistent snapshot of `conversation_summaries.db` without creating or
/// modifying it. An absent `conversation_parents` table is supported; malformed
/// tables and other database errors are returned to the caller.
pub fn read_agy_summaries(
    path: impl AsRef<Path>,
) -> Result<HashMap<String, AgyConversationSummary>, Error> {
    let mut connection = open_read_only(path.as_ref())?;
    let transaction = connection.transaction()?;
    let mut entries = HashMap::new();
    {
        let mut statement = transaction.prepare("SELECT conversation_id, title, workspace_uris, status, parent_conversation_id, last_modified_time, not_fully_idle, killed FROM conversation_summaries")?;
        let mut rows = statement.query([])?;
        while let Some(row) = rows.next()? {
            let id: String = row.get::<_, Option<String>>(0)?.unwrap_or_default();
            if id.is_empty() {
                continue;
            }
            let raw: String = row.get::<_, Option<String>>(2)?.unwrap_or_default();
            let paths = serde_json::from_str::<Vec<String>>(&raw)
                .unwrap_or_else(|_| raw.split(',').map(str::to_owned).collect());
            let time: String = row.get::<_, Option<String>>(5)?.unwrap_or_default();
            let entry = AgyConversationSummary {
                conversation_id: id.clone(),
                title: row.get::<_, Option<String>>(1)?.unwrap_or_default(),
                status: row.get::<_, Option<String>>(3)?.unwrap_or_default(),
                parent_conversation_id: row.get::<_, Option<String>>(4)?.unwrap_or_default(),
                workspace_paths: workspace_paths(paths),
                last_modified: sqlite_time(&time),
                not_fully_idle: sqlite_bool(row.get_ref(6)?),
                killed: sqlite_bool(row.get_ref(7)?),
            };
            entries.insert(id, entry);
        }
    }
    let has_parents: bool = transaction.query_row("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='conversation_parents')", [], |row| row.get(0))?;
    if has_parents {
        let mut statement =
            transaction.prepare("SELECT conversation_id, parent_id FROM conversation_parents")?;
        let mut rows = statement.query([])?;
        while let Some(row) = rows.next()? {
            let id: String = row.get::<_, Option<String>>(0)?.unwrap_or_default();
            let parent: String = row.get::<_, Option<String>>(1)?.unwrap_or_default();
            if id.is_empty() || parent.is_empty() {
                continue;
            }
            let entry = entries
                .entry(id.clone())
                .or_insert_with(|| AgyConversationSummary {
                    conversation_id: id,
                    ..Default::default()
                });
            if entry.parent_conversation_id.is_empty() {
                entry.parent_conversation_id = parent;
            }
        }
    }
    transaction.commit()?;
    Ok(entries)
}

/// Read native workspace metadata from `conversations/<id>.meta` or `<id>.db`.
/// Legacy JSON takes precedence when its `cwd` is nonempty. Database reads are
/// read-only and metadata blobs are limited to 1 MiB. Invalid identifiers,
/// missing databases, invalid protobuf, and SQLite errors are returned.
pub fn read_agy_conversation_workspaces(
    directory: impl AsRef<Path>,
    id: &str,
) -> Result<Vec<PathBuf>, Error> {
    if id.contains(['/', '\\'])
        || !matches!(
            Path::new(id).components().collect::<Vec<_>>().as_slice(),
            [Component::Normal(_)]
        )
    {
        return Err(Error::Invalid(
            "conversation ID must be a single path component".into(),
        ));
    }
    let directory = directory.as_ref();
    match std::fs::read(directory.join(format!("{id}.meta"))) {
        Ok(data) => {
            if let Ok(value) = serde_json::from_slice::<serde_json::Value>(&data)
                && let Some(cwd) = value["cwd"]
                    .as_str()
                    .map(str::trim)
                    .filter(|v| !v.is_empty())
            {
                return Ok(vec![PathBuf::from(cwd)]);
            }
        }
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
        Err(error) => return Err(error.into()),
    }
    let connection = open_read_only(&directory.join(format!("{id}.db")))?;
    let data: Vec<u8> = connection.query_row(
        "SELECT data FROM trajectory_metadata_blob WHERE length(data) <= 1048576 LIMIT 1",
        [],
        |row| row.get(0),
    )?;
    let metadata = TrajectoryMetadata::decode(data.as_slice())?;
    Ok(workspace_paths(
        metadata
            .workspaces
            .into_iter()
            .map(|v| v.uri)
            .chain(metadata.workspace_uris),
    ))
}

fn open_read_only(path: &Path) -> Result<Connection, Error> {
    let connection = Connection::open_with_flags(
        path,
        OpenFlags::SQLITE_OPEN_READ_ONLY | OpenFlags::SQLITE_OPEN_NO_MUTEX,
    )?;
    connection.busy_timeout(Duration::from_millis(100))?;
    Ok(connection)
}

// Observed native TrajectoryMetadata fields; prost skips unknown fields.
#[derive(Clone, PartialEq, Message)]
struct TrajectoryMetadata {
    #[prost(message, repeated, tag = "1")]
    workspaces: Vec<Workspace>,
    #[prost(string, repeated, tag = "7")]
    workspace_uris: Vec<String>,
}

#[derive(Clone, PartialEq, Message)]
struct Workspace {
    #[prost(string, tag = "1")]
    uri: String,
}

fn workspace_paths(uris: impl IntoIterator<Item = String>) -> Vec<PathBuf> {
    let mut paths = Vec::new();
    for uri in uris {
        let uri = uri.trim();
        if uri.is_empty() {
            continue;
        }
        let path = if uri.starts_with("file://") {
            let Some(path) = url::Url::parse(uri)
                .ok()
                .and_then(|u| u.to_file_path().ok())
            else {
                continue;
            };
            path
        } else {
            PathBuf::from(uri)
        };
        if !paths.contains(&path) {
            paths.push(path);
        }
    }
    paths
}

fn sqlite_time(raw: &str) -> Option<i64> {
    let raw = raw.trim();
    chrono::DateTime::parse_from_rfc3339(raw)
        .or_else(|_| chrono::DateTime::parse_from_str(raw, "%Y-%m-%d %H:%M:%S%.f%:z"))
        .map(|v| v.timestamp_millis())
        .ok()
        .or_else(|| {
            chrono::NaiveDateTime::parse_from_str(raw, "%Y-%m-%d %H:%M:%S%.f")
                .ok()
                .map(|v| v.and_utc().timestamp_millis())
        })
}

fn sqlite_bool(value: ValueRef<'_>) -> bool {
    match value {
        ValueRef::Integer(v) => v != 0,
        ValueRef::Real(v) => v != 0.0,
        ValueRef::Text(v) => std::str::from_utf8(v)
            .is_ok_and(|v| v.trim() == "1" || v.trim().eq_ignore_ascii_case("true")),
        _ => false,
    }
}
