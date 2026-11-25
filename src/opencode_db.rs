use crate::*;
use rusqlite::{Connection, OpenFlags, Row};
use std::{path::Path, time::Duration};
const SESSION_COLUMNS: &str = "id,project_id,parent_id,slug,directory,title,version,agent,model,cost,tokens_input,tokens_output,tokens_reasoning,tokens_cache_read,tokens_cache_write,time_created,time_updated,time_archived,metadata";
/// Native SQLite connection, opened read-only for filesystem databases.
pub struct OpencodeDb {
    connection: Connection,
}
impl OpencodeDb {
    pub fn open(path: impl AsRef<Path>) -> Result<Self, Error> {
        let connection = if path.as_ref() == Path::new(":memory:") {
            Connection::open_in_memory()?
        } else {
            Connection::open_with_flags(
                path,
                OpenFlags::SQLITE_OPEN_READ_ONLY | OpenFlags::SQLITE_OPEN_NO_MUTEX,
            )?
        };
        connection.busy_timeout(Duration::from_secs(5))?;
        connection.pragma_update(None, "foreign_keys", true)?;
        Ok(Self { connection })
    }
    /// Wrap a connection supplied by the caller; its access mode remains the caller's responsibility.
    pub fn new(connection: Connection) -> Self {
        Self { connection }
    }
    pub fn connection(&self) -> &Connection {
        &self.connection
    }
    pub fn interrupt_handle(&self) -> rusqlite::InterruptHandle {
        self.connection.get_interrupt_handle()
    }
    pub fn close(self) -> Result<(), Error> {
        self.connection.close().map_err(|(_, e)| e.into())
    }
    pub fn get_sessions(&self) -> Result<Vec<OpencodeSession>, Error> {
        let mut stmt = self.connection.prepare(&format!(
            "SELECT {SESSION_COLUMNS} FROM session ORDER BY time_updated DESC,id ASC"
        ))?;
        Ok(stmt
            .query_map([], scan_session)?
            .collect::<rusqlite::Result<_>>()?)
    }
    pub fn get_session_by_id(&self, id: &str) -> Result<OpencodeSession, Error> {
        Ok(self.connection.query_row(
            &format!("SELECT {SESSION_COLUMNS} FROM session WHERE id=?"),
            [id],
            scan_session,
        )?)
    }
    pub fn get_child_sessions(&self, parent: &str) -> Result<Vec<OpencodeSession>, Error> {
        let mut stmt=self.connection.prepare(&format!("SELECT {SESSION_COLUMNS} FROM session WHERE parent_id=? ORDER BY time_created ASC,id ASC"))?;
        Ok(stmt
            .query_map([parent], scan_session)?
            .collect::<rusqlite::Result<_>>()?)
    }
    pub fn get_messages(&self, session: &str) -> Result<Vec<OpencodeMessage>, Error> {
        let mut stmt=self.connection.prepare("SELECT id,session_id,time_created,data FROM message WHERE session_id=? ORDER BY time_created ASC,id ASC")?;
        let rows = stmt.query_map([session], |row| {
            Ok((
                row.get::<_, String>(0)?,
                row.get::<_, String>(1)?,
                row.get::<_, i64>(2)?,
                row.get::<_, String>(3)?,
            ))
        })?;
        rows.map(|row| {
            let (id, session_id, time_created, data) = row?;
            Ok(OpencodeMessage {
                id,
                session_id,
                time_created,
                data: parse_opencode_message_data(&data)?,
            })
        })
        .collect()
    }
    pub fn get_parts(&self, session: &str) -> Result<Vec<OpencodePart>, Error> {
        self.parts("session_id", session)
    }
    pub fn get_parts_by_message(&self, message: &str) -> Result<Vec<OpencodePart>, Error> {
        self.parts("message_id", message)
    }
    fn parts(&self, column: &str, id: &str) -> Result<Vec<OpencodePart>, Error> {
        let mut stmt=self.connection.prepare(&format!("SELECT id,message_id,session_id,time_created,data FROM part WHERE {column}=? ORDER BY time_created ASC,id ASC"))?;
        let rows = stmt.query_map([id], |row| {
            Ok((
                row.get::<_, String>(0)?,
                row.get::<_, String>(1)?,
                row.get::<_, String>(2)?,
                row.get::<_, i64>(3)?,
                row.get::<_, String>(4)?,
            ))
        })?;
        rows.map(|row| {
            let (id, message_id, session_id, time_created, data) = row?;
            Ok(OpencodePart {
                id,
                message_id,
                session_id,
                time_created,
                data: parse_opencode_part_data(&data)?,
            })
        })
        .collect()
    }
    pub fn get_projects(&self) -> Result<Vec<OpencodeProject>, Error> {
        let mut stmt = self
            .connection
            .prepare("SELECT id,name,worktree,vcs FROM project ORDER BY id ASC")?;
        Ok(stmt
            .query_map([], |r| {
                Ok(OpencodeProject {
                    id: r.get(0)?,
                    name: r.get(1)?,
                    worktree: r.get(2)?,
                    vcs: r.get(3)?,
                })
            })?
            .collect::<rusqlite::Result<_>>()?)
    }
    pub fn get_pending_permissions(&self, session: &str) -> Result<Vec<OpencodePermission>, Error> {
        let mut stmt=self.connection.prepare("SELECT id,session_id,type,data,time_created FROM permission WHERE session_id=? ORDER BY time_created ASC,id ASC")?;
        let rows = stmt.query_map([session], |r| {
            Ok((
                r.get::<_, String>(0)?,
                r.get::<_, String>(1)?,
                r.get::<_, String>(2)?,
                r.get::<_, String>(3)?,
                r.get::<_, i64>(4)?,
            ))
        })?;
        rows.map(|r| {
            let (id, session_id, permission_type, data, time_created) = r?;
            Ok(OpencodePermission {
                id,
                session_id,
                permission_type,
                data: if data.is_empty() {
                    Default::default()
                } else {
                    serde_json::from_str(&data)?
                },
                time_created,
            })
        })
        .collect()
    }
    pub fn reconstruct_session_events(&self, session: &str) -> Result<Vec<ParsedEvent>, Error> {
        let snapshot = self.session_snapshot(session)?;
        Ok(reconstruct_session_events(
            &snapshot.session,
            &snapshot.messages,
            &snapshot.parts,
        ))
    }
    pub fn session_snapshot(&self, session: &str) -> Result<OpencodeSessionSnapshot, Error> {
        let tx = self.connection.unchecked_transaction()?;
        let snapshot = OpencodeSessionSnapshot {
            session: self.get_session_by_id(session)?,
            messages: self.get_messages(session)?,
            parts: self.get_parts(session)?,
        };
        tx.commit()?;
        Ok(snapshot)
    }
}
fn scan_session(r: &Row<'_>) -> rusqlite::Result<OpencodeSession> {
    let parent_id: Option<String> = r.get(2)?;
    let archived: Option<i64> = r.get(17)?;
    let model: Option<String> = r.get(8)?;
    let (model, model_config) = parse_opencode_model(model.as_deref().unwrap_or(""));
    let (input, output, reasoning, read, write) = (
        r.get::<_, i64>(10)?,
        r.get::<_, i64>(11)?,
        r.get::<_, i64>(12)?,
        r.get::<_, i64>(13)?,
        r.get::<_, i64>(14)?,
    );
    let metadata: Option<String> = r.get(18)?;
    let version = r.get::<_, Option<String>>(6)?.unwrap_or_default();
    let extra = if output_includes_reasoning(&version) {
        0
    } else {
        reasoning
    };
    let total = i64::try_from(
        i128::from(input)
            + i128::from(output)
            + i128::from(read)
            + i128::from(write)
            + i128::from(extra),
    )
    .map_err(|error| {
        rusqlite::Error::FromSqlConversionFailure(
            10,
            rusqlite::types::Type::Integer,
            Box::new(error),
        )
    })?;
    Ok(OpencodeSession {
        id: r.get(0)?,
        project_id: r.get(1)?,
        is_subagent: parent_id.is_some(),
        parent_id,
        slug: r.get(3)?,
        directory: r.get(4)?,
        title: r.get::<_, Option<String>>(5)?.unwrap_or_default(),
        version,
        agent: r.get::<_, Option<String>>(7)?.unwrap_or_default(),
        model,
        model_config,
        cost: r.get::<_, Option<f64>>(9)?.unwrap_or_default(),
        tokens: OpencodeTokens {
            input,
            output,
            reasoning,
            cache: OpencodeCacheTokens { read, write },
            total,
        },
        time_created: r.get(15)?,
        time_updated: r.get(16)?,
        time_archived: archived,
        is_archived: archived.is_some(),
        metadata: metadata.and_then(|s| serde_json::from_str(&s).ok()),
    })
}
