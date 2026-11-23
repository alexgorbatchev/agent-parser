use agent_parser::{read_agy_conversation_workspaces, read_agy_summaries};
use rusqlite::{Connection, params};
use std::path::PathBuf;

fn summaries(path: &std::path::Path) -> Connection {
    let db = Connection::open(path).unwrap();
    db.execute_batch("CREATE TABLE conversation_summaries (conversation_id TEXT, title TEXT, workspace_uris TEXT, status TEXT, parent_conversation_id TEXT, last_modified_time TEXT, not_fully_idle, killed);
        INSERT INTO conversation_summaries VALUES ('root','Title','[\"file:///tmp/a%20b\",\"/tmp/plain\",\"file:///tmp/a%20b\",\" \" ]','IDLE','','2026-11-22 00:00:00',1,'false');
        INSERT INTO conversation_summaries VALUES ('child',NULL,'file:///tmp/b,/tmp/plain','RUNNING','root','2026-11-22T01:00:00+01:00','true',1);
        INSERT INTO conversation_summaries VALUES ('','ignored','','','','invalid',NULL,NULL);").unwrap();
    db
}

#[test]
fn summaries_preserve_native_values_and_optional_parent_index_without_writes() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("summaries.db");
    let db = summaries(&path);
    let before = std::fs::read(&path).unwrap();
    let entries = read_agy_summaries(&path).unwrap();
    assert_eq!(entries.len(), 2);
    let root = &entries["root"];
    assert_eq!(root.title, "Title");
    assert_eq!(
        root.workspace_paths,
        [PathBuf::from("/tmp/a b"), PathBuf::from("/tmp/plain")]
    );
    assert_eq!(root.last_modified, Some(1795305600000));
    assert!(root.not_fully_idle);
    assert!(!root.killed);
    assert_eq!(entries["child"].last_modified, root.last_modified);
    assert!(entries["child"].killed);
    assert_eq!(std::fs::read(&path).unwrap(), before);
    db.execute_batch("CREATE TABLE conversation_parents(conversation_id TEXT,parent_id TEXT);
        INSERT INTO conversation_parents VALUES ('detached','root'),('child','wrong'),('','x'),('orphan','');").unwrap();
    let entries = read_agy_summaries(&path).unwrap();
    assert_eq!(entries["child"].parent_conversation_id, "root");
    assert_eq!(entries["detached"].parent_conversation_id, "root");
    assert_eq!(entries.len(), 3);
    db.execute_batch(
        "DROP TABLE conversation_parents; CREATE TABLE conversation_parents(wrong TEXT);",
    )
    .unwrap();
    assert!(read_agy_summaries(&path).is_err());
    let missing = dir.path().join("missing.db");
    assert!(read_agy_summaries(&missing).is_err());
    assert!(!missing.exists());
}

#[test]
fn trajectory_and_legacy_workspaces_are_read_only_and_validate_identifiers() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("session.db");
    let db = Connection::open(&path).unwrap();
    db.execute_batch("CREATE TABLE trajectory_metadata_blob(data BLOB)")
        .unwrap();
    // Native protobuf: field 1 -> workspace URI in field 1, plus field 7 URI.
    let first = b"file:///tmp/a%20b";
    let second = b"file:///tmp/b";
    let mut blob = vec![10, (first.len() + 2) as u8, 10, first.len() as u8];
    blob.extend(first);
    blob.extend([58, second.len() as u8]);
    blob.extend(second);
    db.execute(
        "INSERT INTO trajectory_metadata_blob VALUES (?1)",
        params![blob],
    )
    .unwrap();
    let before = std::fs::read(&path).unwrap();
    assert_eq!(
        read_agy_conversation_workspaces(dir.path(), "session").unwrap(),
        [PathBuf::from("/tmp/a b"), PathBuf::from("/tmp/b")]
    );
    assert_eq!(std::fs::read(&path).unwrap(), before);
    std::fs::write(
        dir.path().join("session.meta"),
        br#"{"cwd":" /tmp/legacy "}"#,
    )
    .unwrap();
    assert_eq!(
        read_agy_conversation_workspaces(dir.path(), "session").unwrap(),
        [PathBuf::from("/tmp/legacy")]
    );
    for data in ["{}", "not json", r#"{"cwd":2}"#] {
        std::fs::write(dir.path().join("session.meta"), data).unwrap();
        assert_eq!(
            read_agy_conversation_workspaces(dir.path(), "session")
                .unwrap()
                .len(),
            2
        );
    }
    for id in ["", "..", "../session", "/session", "nested/session"] {
        assert!(read_agy_conversation_workspaces(dir.path(), id).is_err());
    }
    assert!(read_agy_conversation_workspaces(dir.path(), "missing").is_err());
    assert!(!dir.path().join("missing.db").exists());
    db.execute(
        "UPDATE trajectory_metadata_blob SET data=?1",
        params![vec![255u8]],
    )
    .unwrap();
    assert!(read_agy_conversation_workspaces(dir.path(), "session").is_err());
    db.execute(
        "UPDATE trajectory_metadata_blob SET data=zeroblob(1048577)",
        [],
    )
    .unwrap();
    assert!(read_agy_conversation_workspaces(dir.path(), "session").is_err());
}
