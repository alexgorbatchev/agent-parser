use agent_parser::*;
use rusqlite::Connection;
use serde_json::json;
const SCHEMA: &str = "CREATE TABLE session(id TEXT,project_id TEXT,parent_id TEXT,slug TEXT,directory TEXT,title TEXT,version TEXT,agent TEXT,model TEXT,cost REAL,tokens_input INTEGER,tokens_output INTEGER,tokens_reasoning INTEGER,tokens_cache_read INTEGER,tokens_cache_write INTEGER,time_created INTEGER,time_updated INTEGER,time_archived INTEGER,metadata TEXT); CREATE TABLE message(id TEXT,session_id TEXT,time_created INTEGER,data TEXT);CREATE TABLE part(id TEXT,message_id TEXT,session_id TEXT,time_created INTEGER,data TEXT);CREATE TABLE project(id TEXT,name TEXT,worktree TEXT,vcs TEXT);CREATE TABLE permission(id TEXT,session_id TEXT,type TEXT,data TEXT,time_created INTEGER);";
#[test]
fn database_reads_real_rows_without_writing() {
    let temp = tempfile::tempdir().unwrap();
    let path = temp.path().join("opencode.db");
    let connection = Connection::open(&path).unwrap();
    connection.execute_batch(SCHEMA).unwrap();
    connection.execute("INSERT INTO session VALUES('s','p',NULL,NULL,'/project','Title',NULL,NULL,?,NULL,10,5,2,3,1,1000,2000,NULL,'{}')",[r#"{"id":"model","providerID":"provider"}"#]).unwrap();
    connection.execute_batch("INSERT INTO project VALUES('p','Project',NULL,'git');INSERT INTO message VALUES('u','s',1100,'{\"role\":\"user\"}');INSERT INTO message VALUES('a','s',1200,'{\"role\":\"assistant\",\"finish\":\"stop\"}');INSERT INTO part VALUES('p1','u','s',1100,'{\"type\":\"text\",\"text\":\"Hello\"}');INSERT INTO part VALUES('p2','a','s',1300,'{\"type\":\"text\",\"text\":\"Done\"}');INSERT INTO permission VALUES('perm','s','file','{\"path\":\"a\"}',1500);").unwrap();
    drop(connection);
    let db = OpencodeDb::open(&path).unwrap();
    assert!(db.connection().execute("DELETE FROM session", []).is_err());
    let sessions = db.get_sessions().unwrap();
    assert_eq!(sessions[0].tokens.total, 21);
    assert_eq!(sessions[0].model, "model");
    assert!(db.get_child_sessions("s").unwrap().is_empty());
    assert_eq!(db.get_session_by_id("s").unwrap(), sessions[0]);
    assert_eq!(db.get_messages("s").unwrap().len(), 2);
    assert_eq!(db.get_parts("s").unwrap().len(), 2);
    assert_eq!(db.get_parts_by_message("a").unwrap()[0].data.text, "Done");
    assert_eq!(db.get_projects().unwrap()[0].vcs.as_deref(), Some("git"));
    assert_eq!(
        db.get_pending_permissions("s").unwrap()[0].data["path"],
        "a"
    );
    let events = db.reconstruct_session_events("s").unwrap();
    let snapshot = db.session_snapshot("s").unwrap();
    assert_eq!(snapshot.session, sessions[0]);
    assert_eq!(snapshot.messages.len(), 2);
    assert_eq!(snapshot.parts.len(), 2);
    assert_eq!(
        events,
        reconstruct_session_events(&snapshot.session, &snapshot.messages, &snapshot.parts)
    );
    assert!(db.session_snapshot("absent").is_err());
    assert!(db.session_snapshot("s").is_ok());
    assert_eq!(
        events
            .iter()
            .map(|e| e.event_type.as_str())
            .collect::<Vec<_>>(),
        ["run_started", "turn_start", "agent_message", "turn_end"]
    );
    assert!(db.get_session_by_id("absent").is_err());
    db.close().unwrap();
    assert!(OpencodeDb::open(temp.path().join("absent")).is_err());
}
#[test]
fn reconstruction_orders_events_and_preserves_structured_errors() {
    let session = OpencodeSession {
        id: "s".into(),
        directory: "/project".into(),
        model: "m".into(),
        time_created: 1000,
        ..Default::default()
    };
    let message=OpencodeMessage{id:"a".into(),time_created:1100,data:parse_opencode_message_data(r#"{"role":"assistant","tokens":{"input":2,"output":1},"finish":"error","error":{"name":"APIError","data":{"statusCode":429,"message":"slow","responseHeaders":{"retry-after":"2"}}}}"#).unwrap(),..Default::default()};
    let tool=OpencodePart{message_id:"a".into(),time_created:1200,data:parse_opencode_part_data(r#"{"type":"tool","tool":"edit","callID":"call","state":{"status":"completed","input":{"path":"a","old_string":"x","new_string":"y"},"output":"done","time":{"start":1150,"end":1300}}}"#).unwrap(),..Default::default()};
    let events = reconstruct_session_events(&session, &[message], &[tool]);
    assert_eq!(events[1].event_type, "tool_call");
    assert_eq!(
        events[1].data.file_modification.as_ref().unwrap().file_path,
        "a"
    );
    assert_eq!(
        events
            .iter()
            .find(|e| e.event_type == "tool_result")
            .unwrap()
            .data
            .tool_output
            .as_deref(),
        Some("done")
    );
    let err = events.iter().find(|e| e.event_type == "error").unwrap();
    assert_eq!(err.data.is_rate_limit, Some(true));
    assert_eq!(err.data.rate_limit_reset_at, Some(3100));
    assert_eq!(err.data.content.as_deref(), Some("slow"));
    assert!(events.windows(2).all(|w| w[0].timestamp <= w[1].timestamp));
    assert!(parse_opencode_message_data("bad").is_err());
    assert!(parse_opencode_part_data("bad").is_err());
    let (id, cfg) = parse_opencode_model(&json!({"id":"x","providerID":"p"}).to_string());
    assert_eq!(id, "x");
    assert_eq!(cfg.unwrap().provider_id, "p");
}

#[test]
fn reads_opencode_reasoning_totals_across_session_versions() {
    for (version, recorded_total, output, total) in [
        ("1.3.16-rc.1", 0, 12, 26),
        ("v1.3.16", 0, 17, 31),
        ("1.3.15", 31, 17, 31),
        ("1.3.17", 26, 12, 26),
        ("unknown", 0, 17, 31),
    ] {
        let session = OpencodeSession {
            version: version.into(),
            ..Default::default()
        };
        let data = parse_opencode_message_data(
            &json!({"role":"assistant","tokens":{"input":10,"output":12,"reasoning":5,"cache":{"read":3,"write":1},"total":recorded_total}}).to_string(),
        )
        .unwrap();
        let events = reconstruct_session_events(
            &session,
            &[OpencodeMessage {
                data,
                ..Default::default()
            }],
            &[],
        );
        let usage = events[1].data.usage.as_ref().unwrap();
        assert_eq!(
            (usage.output, usage.total, usage.reasoning),
            (output, total, 5),
            "{version}"
        );
    }
}

#[test]
fn parsed_opencode_payloads_keep_sparse_wire_contracts() {
    let message = parse_opencode_message_data(
        r#"{"role":"assistant","modelID":"m","tokens":{"input":3,"output":2}}"#,
    )
    .unwrap();
    let encoded = serde_json::to_value(&message).unwrap();
    assert_eq!(encoded["modelID"], "m");
    assert!(encoded.get("providerID").is_none());
    assert!(encoded["tokens"].get("reasoning").is_none());
    assert_eq!(
        parse_opencode_message_data(&encoded.to_string()).unwrap(),
        message
    );
    let part = parse_opencode_part_data(
        r#"{"type":"tool","tool":"read","state":{"status":"pending","input":{}}}"#,
    )
    .unwrap();
    let encoded = serde_json::to_value(&part).unwrap();
    assert!(encoded.get("text").is_none());
    assert!(encoded["state"].get("input").is_none());
    assert!(encoded["state"].get("error").is_none());
}

#[test]
fn sqlite_reports_corruption_and_interrupts_running_queries() {
    let db = OpencodeDb::open(":memory:").unwrap();
    db.connection().execute_batch(SCHEMA).unwrap();
    db.connection()
        .execute_batch("INSERT INTO permission VALUES('perm','s','file','broken',1);")
        .unwrap();
    assert!(matches!(
        db.get_pending_permissions("s"),
        Err(Error::Json(_))
    ));
    let interrupt = db.interrupt_handle();
    let worker = std::thread::spawn(move || {
        db.connection().query_row(
            "WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n) SELECT SUM(x) FROM n",
            [],
            |r| r.get::<_, i64>(0),
        )
    });
    // Repeat until the query starts or finishes, avoiding a race with SQLite's
    // documented behavior of ignoring an interrupt before a statement starts.
    while !worker.is_finished() {
        interrupt.interrupt();
        std::thread::yield_now();
    }
    assert_eq!(
        worker.join().unwrap().unwrap_err().sqlite_error_code(),
        Some(rusqlite::ErrorCode::OperationInterrupted)
    );
}
