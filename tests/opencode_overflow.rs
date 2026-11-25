use agent_parser::{Error, OpencodeDb};
use rusqlite::{Connection, params};
use std::fs;

#[test]
fn sqlite_token_total_overflow_returns_a_typed_conversion_error() {
    fs::create_dir_all(".tmp").unwrap();
    let temp = tempfile::tempdir_in(".tmp").unwrap();
    let path = temp.path().join("opencode.db");
    let db = Connection::open(&path).unwrap();
    db.execute_batch("CREATE TABLE session(id TEXT,project_id TEXT,parent_id TEXT,slug TEXT,directory TEXT,title TEXT,version TEXT,agent TEXT,model TEXT,cost REAL,tokens_input INTEGER,tokens_output INTEGER,tokens_reasoning INTEGER,tokens_cache_read INTEGER,tokens_cache_write INTEGER,time_created INTEGER,time_updated INTEGER,time_archived INTEGER,metadata TEXT);").unwrap();
    db.execute("INSERT INTO session VALUES('s',NULL,NULL,NULL,'/repo',NULL,NULL,NULL,NULL,0,?,1,0,0,0,1000,2000,NULL,NULL)", [i64::MAX]).unwrap();
    drop(db);
    let reader = OpencodeDb::open(&path).unwrap();
    assert!(matches!(
        reader.get_sessions(),
        Err(Error::Sqlite(rusqlite::Error::FromSqlConversionFailure(
            _,
            rusqlite::types::Type::Integer,
            _
        )))
    ));
    assert!(matches!(
        reader.get_session_by_id("s"),
        Err(Error::Sqlite(_))
    ));
    drop(reader);
    let db = Connection::open(&path).unwrap();
    db.execute(
        "UPDATE session SET tokens_input=?,tokens_output=?",
        params![i64::MIN, -1],
    )
    .unwrap();
    drop(db);
    assert!(matches!(
        OpencodeDb::open(&path).unwrap().get_sessions(),
        Err(Error::Sqlite(_))
    ));
}

#[test]
fn exact_boundary_totals_preserve_zero_and_negative_source_values_without_overflow() {
    let db = Connection::open_in_memory().unwrap();
    db.execute_batch("CREATE TABLE session(id TEXT,project_id TEXT,parent_id TEXT,slug TEXT,directory TEXT,title TEXT,version TEXT,agent TEXT,model TEXT,cost REAL,tokens_input INTEGER,tokens_output INTEGER,tokens_reasoning INTEGER,tokens_cache_read INTEGER,tokens_cache_write INTEGER,time_created INTEGER,time_updated INTEGER,time_archived INTEGER,metadata TEXT);").unwrap();
    db.execute("INSERT INTO session VALUES('s',NULL,NULL,NULL,'/repo',NULL,'1.3.15',NULL,NULL,0,?,0,123,0,0,1000,2000,NULL,NULL)",[i64::MAX]).unwrap();
    let reader = OpencodeDb::new(db);
    assert_eq!(reader.get_sessions().unwrap()[0].tokens.total, i64::MAX);
    reader
        .connection()
        .execute(
            "UPDATE session SET tokens_output=1,tokens_cache_read=-1",
            [],
        )
        .unwrap();
    assert_eq!(reader.get_sessions().unwrap()[0].tokens.total, i64::MAX);
    reader.connection().execute("UPDATE session SET tokens_input=?,tokens_output=0,tokens_cache_read=0,tokens_cache_write=0",[i64::MIN]).unwrap();
    assert_eq!(reader.get_sessions().unwrap()[0].tokens.total, i64::MIN);
    reader
        .connection()
        .execute("UPDATE session SET tokens_input=0", [])
        .unwrap();
    assert_eq!(reader.get_sessions().unwrap()[0].tokens.total, 0);
}
