use agent_parser::{ClaudeCodeParser, CodexParser, parse_codex_line};
use serde_json::json;

#[test]
fn codex_reports_each_confirmed_change_and_preserves_moved_diff_lines() {
    let changes = json!({
        "old.sql": {
            "type":"update", "move_path":"new.sql",
            "unified_diff":"@@ -1,2 +1,2 @@\n SELECT 1;\n--- drop index\n+++ b/keep\n",
        },
        "added.sql": {"type":"add","content":"SELECT 2;"},
        "deleted.sql": {"type":"delete","content":"SELECT 3;"},
        "ignored.sql": {"type":"unknown"},
    });
    for payload in [
        json!({"type":"item_completed","item":{"type":"FileChange","id":"call","changes":changes,"status":"completed"}}),
        json!({"type":"patch_apply_end","call_id":"call","changes":changes,"success":true}),
    ] {
        let record = json!({"type":"event_msg","payload":payload});
        let events = parse_codex_line(record.to_string().as_bytes()).unwrap();
        assert_eq!(events.len(), 3);
        for event in &events {
            assert_eq!(event.event_type, "file_modification");
            assert_eq!(event.data.tool_call_id.as_deref(), Some("call"));
        }
        let modifications: Vec<_> = events
            .into_iter()
            .map(|event| event.data.file_modification.unwrap())
            .collect();
        assert_eq!(modifications[0].file_path, "added.sql");
        assert_eq!(modifications[0].action, "WRITE");
        assert_eq!(modifications[1].action, "DELETE");
        let moved = &modifications[2];
        assert_eq!(moved.file_path, "new.sql");
        assert_eq!(moved.previous_path, "old.sql");
        assert_eq!(moved.hunks[0].lines[1].content, "-- drop index");
        assert_eq!(moved.hunks[0].lines[2].content, "++ b/keep");
    }
    for payload in [
        json!({"type":"item_completed","item":{"type":"FileChange","changes":changes,"status":"declined"}}),
        json!({"type":"patch_apply_end","changes":changes,"success":false}),
    ] {
        let record = json!({"type":"event_msg","payload":payload});
        assert!(
            parse_codex_line(record.to_string().as_bytes())
                .unwrap()
                .is_empty()
        );
    }
    assert!(
        parse_codex_line(
            br#"{"type":"event_msg","payload":{"type":"patch_apply_end","changes":"bad"}}"#
        )
        .is_err()
    );
}

#[test]
fn claude_reports_absorbed_and_merged_user_message_ids() {
    let mut parser = ClaudeCodeParser::new();
    let prompt = br#"{"type":"user","sessionId":"s","uuid":"first","mergedSendUuids":["joined"],"message":{"role":"user","content":"start work"}}"#;
    let absorbed = br#"{"type":"queue-operation","sessionId":"s","operation":"remove","reason":"absorbed_mid_turn","commandUuid":"injected","content":"and test it"}"#;
    let ignored = br#"{"type":"queue-operation","sessionId":"s","operation":"remove","reason":"cancelled","commandUuid":"cancelled","content":"ignore this"}"#;
    let events = parser.parse_line(prompt).unwrap();
    let start = events.last().unwrap();
    assert_eq!(start.data.message_uuid.as_deref(), Some("first"));
    assert_eq!(start.data.merged_message_uuids, ["joined"]);
    let events = parser.parse_line(absorbed).unwrap();
    assert_eq!(events[0].event_type, "message_absorbed");
    assert_eq!(events[0].data.message_uuid.as_deref(), Some("injected"));
    assert_eq!(events[0].data.content.as_deref(), Some("and test it"));
    assert!(parser.parse_line(ignored).unwrap().is_empty());
}

#[test]
fn codex_turns_keep_the_first_prompt_and_report_absorbed_messages() {
    let mut parser = CodexParser::new();
    let mut parse =
        |record: serde_json::Value| parser.parse_line(record.to_string().as_bytes()).unwrap();
    parse(json!({"type":"session_meta","payload":{"id":"s","subagent_history_start_ordinal":7}}));
    assert!(parse(json!({"type":"response_item","ordinal":6,"payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"inherited"}]}})).is_empty());
    assert!(
        parse(
            json!({"type":"event_msg","ordinal":7,"payload":{"type":"task_started","started_at":2}})
        )
        .is_empty()
    );
    assert!(
        parse(json!({"type":"turn_context","payload":{"model":"gpt","cwd":"/repo"}})).is_empty()
    );
    assert!(parse(json!({"type":"compacted","payload":{"message":"summary"}})).is_empty());
    assert!(
        parse(json!({"type":"event_msg","payload":{"type":"user_message","message":"mirrored"}}))
            .is_empty()
    );
    let events = parse(
        json!({"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"first prompt"}]}}),
    );
    assert_eq!(
        events
            .iter()
            .map(|e| e.event_type.as_str())
            .collect::<Vec<_>>(),
        ["turn_start", "model_change", "compaction"]
    );
    assert_eq!(events[0].timestamp, 2000);
    assert_eq!(events[0].data.last_prompt.as_deref(), Some("first prompt"));
    let events = parse(
        json!({"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"follow-up"}]}}),
    );
    assert_eq!(events[0].event_type, "message_absorbed");
    assert_eq!(events[0].data.content.as_deref(), Some("follow-up"));
    assert!(events[0].data.last_prompt.is_none());
    let events = parse(
        json!({"type":"event_msg","payload":{"type":"turn_aborted","completed_at":3,"reason":"interrupted"}}),
    );
    assert_eq!(events[0].event_type, "turn_end");
    assert_eq!(events[0].data.stop_reason.as_deref(), Some("interrupted"));

    let mut parser = CodexParser::new();
    let mut parse =
        |record: serde_json::Value| parser.parse_line(record.to_string().as_bytes()).unwrap();
    parse(json!({"type":"session_meta","payload":{"id":"p","history_mode":"paginated"}}));
    parse(json!({"type":"event_msg","payload":{"type":"task_started"}}));
    assert!(parse(json!({"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"legacy"}]}})).is_empty());
    let events = parse(
        json!({"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","content":[{"type":"text","text":"paginated prompt"}]}}}),
    );
    assert_eq!(events[0].event_type, "turn_start");
    assert_eq!(
        events[0].data.last_prompt.as_deref(),
        Some("paginated prompt")
    );
}
